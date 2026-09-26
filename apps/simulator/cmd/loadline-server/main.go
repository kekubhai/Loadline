// Command loadline-server serves the loadline.v1 SimulationService over
// HTTP: Connect protocol (browser + cURL friendly), gRPC, and gRPC-Web —
// the three protocols ConnectRPC handlers support for free.
//
// This binary is deliberately thin: it only wires transports (routes,
// CORS, compression) around the API layer. All simulation behavior lives
// in the engine/sim/workload packages, which know nothing about HTTP.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/cors"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
	"github.com/kekubhai/Loadline/apps/simulator/loadlinev1"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	store := loadlinev1.NewStore()
	path, handler := lv1connect.NewSimulationServiceHandler(
		loadlinev1.NewService(store),
		connect.WithCompressMinBytes(1024),
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)

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
	log.Printf("loadline-server listening on %s (connect + grpc + grpc-web)", *addr)
	log.Fatal(srv.ListenAndServe())
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
