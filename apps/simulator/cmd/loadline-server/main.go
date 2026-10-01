// Command loadline-server serves LOADLINE's API over HTTP: Connect protocol
// (browser + cURL friendly), gRPC, and gRPC-Web — the three protocols
// ConnectRPC handlers support for free.
//
// It serves two services:
//
//	loadline.v1.SimulationService — ephemeral in-memory simulations
//	loadline.v1.WorkspaceService  — persisted projects, architectures,
//	                                versions, workloads, and runs
//
// This binary is deliberately thin: it only wires transports (routes, CORS,
// compression), the database handle, and process lifecycle around the API
// layer. All simulation behavior lives in the engine/sim/workload packages,
// which know nothing about HTTP or PostgreSQL.
//
// The workspace service is only registered when DATABASE_URL is configured.
// Without it the server still serves the simulation service — running a
// what-if simulation never depends on a database — and reports
// "database": "disabled" on /healthz.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/cors"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/kekubhai/Loadline/apps/simulator/internal/db"
	"github.com/kekubhai/Loadline/apps/simulator/internal/repositories"
	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
	"github.com/kekubhai/Loadline/apps/simulator/loadlinev1"
)

// startupTimeout bounds the database connect + migrate step so a bad
// connection string or an unreachable host fails fast at boot.
const startupTimeout = 30 * time.Second

// shutdownTimeout bounds how long in-flight requests get to finish.
const shutdownTimeout = 15 * time.Second

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	requireDB := flag.Bool("require-database", false, "exit if DATABASE_URL is not configured")
	flag.Parse()

	db.LoadDotEnv()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := openWorkspaceDatabase(ctx)
	if err != nil {
		log.Fatalf("loadline-server: %v", err)
	}
	if database == nil && *requireDB {
		log.Fatalf("loadline-server: -require-database was set but %s is empty", db.EnvDatabaseURL)
	}
	if database != nil {
		defer database.Close()
	}

	mux := http.NewServeMux()

	// SimulationService: in-memory what-if runs. Never needs PostgreSQL.
	simPath, simHandler := lv1connect.NewSimulationServiceHandler(
		loadlinev1.NewService(loadlinev1.NewStore()),
		connect.WithCompressMinBytes(1024),
	)
	mux.Handle(simPath, simHandler)

	// Repositories are shared by the persistence-backed services and are
	// nil when no database is configured.
	var (
		projects      repositories.ProjectRepository
		architectures repositories.ArchitectureRepository
		workloads     repositories.WorkloadRepository
		simulations   repositories.SimulationRepository
		challenges    repositories.ChallengeRepository
	)
	if database != nil {
		projects = repositories.NewProjectRepository(database)
		architectures = repositories.NewArchitectureRepository(database)
		workloads = repositories.NewWorkloadRepository(database)
		simulations = repositories.NewSimulationRepository(database)
		challenges = repositories.NewChallengeRepository(database)
		log.Printf("workspace persistence enabled (%s configured)", db.EnvDatabaseURL)
	} else {
		log.Printf("workspace persistence disabled: %s is not set", db.EnvDatabaseURL)
	}

	// WorkspaceService: persisted documents, only when a database is
	// configured. Registering handlers without a database would turn every
	// workspace call into an internal error, so it is better to leave the
	// routes absent and say so in the logs and on /healthz.
	if database != nil {
		workspacePath, workspaceHandler := lv1connect.NewWorkspaceServiceHandler(
			loadlinev1.NewWorkspaceService(projects, architectures, workloads, simulations),
			connect.WithCompressMinBytes(1024),
		)
		mux.Handle(workspacePath, workspaceHandler)
	}

	// ArenaService: challenge definitions are served from the embedded
	// version-controlled files, so browsing Arena works without a database;
	// submissions and leaderboards need one. Registration is therefore
	// unconditional, with a clear precondition error when persistence is
	// unavailable.
	arenaPath, arenaHandler := lv1connect.NewArenaServiceHandler(
		loadlinev1.NewArenaService(challenges, architectures, simulations),
		connect.WithCompressMinBytes(1024),
	)
	mux.Handle(arenaPath, arenaHandler)

	// Project the canonical challenge definitions into the database so
	// submissions can reference them. Idempotent and safe to run every boot.
	if database != nil {
		if err := loadlinev1.SyncChallenges(ctx, challenges); err != nil {
			log.Fatalf("loadline-server: sync challenges: %v", err)
		}
		log.Printf("arena: challenge definitions synced")
	}

	mux.HandleFunc("/healthz", healthHandler(database))

	// Browser origins (Next.js dev + prod); override with
	// LOADLINE_ALLOWED_ORIGINS (comma-separated).
	h := cors.New(cors.Options{
		AllowedOrigins: originsFromEnv("LOADLINE_ALLOWED_ORIGINS", "http://localhost:3000"),
		AllowedMethods: []string{http.MethodPost, http.MethodGet, http.MethodOptions},
		AllowedHeaders: []string{"*"},
	}).Handler(mux)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           h2c.NewHandler(h, &http2.Server{}), // gRPC needs h2c
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown: stop accepting, let in-flight requests finish,
	// then close the pool (the deferred Close above runs after this).
	errCh := make(chan error, 1)
	go func() {
		log.Printf("loadline-server listening on %s (connect + grpc + grpc-web)", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		log.Fatalf("loadline-server: %v", err)
	case <-ctx.Done():
		log.Print("loadline-server: shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("loadline-server: shutdown: %v", err)
	}
}

// openWorkspaceDatabase connects to PostgreSQL and applies migrations when
// DATABASE_URL is set, and returns (nil, nil) when it is not.
//
// A configured-but-broken database is a startup error: serving a workspace
// API against a database that cannot be reached would fail every request.
// An unconfigured database is not an error — the simulation service is
// complete on its own.
func openWorkspaceDatabase(ctx context.Context) (*db.Database, error) {
	if strings.TrimSpace(os.Getenv(db.EnvDatabaseURL)) == "" {
		return nil, nil
	}
	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	database, err := db.OpenFromEnv(startupCtx)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(startupCtx, database.Pool); err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
}

// healthHandler reports the three independent things a caller cares about:
// the API process, PostgreSQL, and the simulation engine.
//
//   - the API is healthy whenever it answers at all;
//   - the database is "ok", "unavailable", or "disabled" (not configured);
//   - the simulation engine is reported available without running a
//     simulation, because a run is pure computation with no external
//     dependency — its availability is the binary's own.
func healthHandler(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := map[string]string{
			"api":              "ok",
			"simulationEngine": "ok",
			"database":         "disabled",
		}
		status := http.StatusOK

		if database != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			if err := database.Ping(ctx); err != nil {
				// The cause is logged, never returned: a connection error
				// can carry the connection string, and therefore credentials.
				log.Printf("loadline-server: health: database ping failed: %v", err)
				body["database"] = "unavailable"
				status = http.StatusServiceUnavailable
			} else {
				body["database"] = "ok"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
}

// originsFromEnv parses the comma-separated origin list, falling back to
// the given default.
func originsFromEnv(key, def string) []string {
	v := os.Getenv(key)
	if v == "" {
		v = def
	}
	var out []string
	for _, o := range strings.Split(v, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
