// Command oast-console is a LOCAL-ONLY web console for exercising the OAST
// engine through its MCP tools, by hand, from a browser.
//
// It wires the whole stack in one process — the HTTP and DNS callback
// receivers, the intentionally vulnerable lab (demos enabled), an MCP server,
// and a real in-process MCP client — and serves a small page whose buttons call
// JSON endpoints that invoke the actual MCP tools (oast_create_test,
// oast_get_evidence, oast_list_tests, oast_close_test). Callbacks are produced
// by driving the real lab, exactly as an agent would.
//
// It is a developer aid, not a product surface:
//   - It binds loopback only and refuses any other bind.
//   - It enables the intentionally vulnerable lab. Do not expose it.
//   - It needs no API key and no external network.
//
// Scope enforcement is unchanged: create requests still go through the service
// layer's authorized-scope check, so out-of-scope targets are rejected here too.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/miekg/dns"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kekubhai/Loadline/apps/oast/internal/callback"
	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
	"github.com/kekubhai/Loadline/apps/oast/internal/mcp"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
	"github.com/kekubhai/Loadline/apps/oast/internal/service"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
	"github.com/kekubhai/Loadline/apps/oast/lab"
)

const (
	consoleDomain = "oast.local"
	consoleHost   = "127.0.0.1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "oast-console: %v\n", err)
		os.Exit(1)
	}
}

type cleanup struct{ fns []func() }

func (c *cleanup) add(fn func()) { c.fns = append(c.fns, fn) }
func (c *cleanup) run() {
	for i := len(c.fns) - 1; i >= 0; i-- {
		c.fns[i]()
	}
}

func run() error {
	// Console listen address: loopback only, fail closed otherwise.
	addr := envOr("OAST_CONSOLE_ADDR", "127.0.0.1:8099")
	if err := requireLoopback(addr); err != nil {
		return err
	}
	scopeList := envOr("OAST_AUTHORIZED_SCOPES", "lab.local")

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	var cl cleanup
	defer cl.run()

	scopes, err := scope.New(splitScopes(scopeList))
	if err != nil {
		return fmt.Errorf("authorized scopes: %w", err)
	}
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests: 100, MaxTestLifetime: time.Hour,
		MaxEvidencePerTest: 500, MaxEvidenceBytesPerTest: 1 << 20,
	}, clock.System{}, token.CryptoGenerator{})
	if err != nil {
		return err
	}
	svc, err := service.New(store, scopes, service.Config{DefaultTTL: 15 * time.Minute, MaxTTL: time.Hour})
	if err != nil {
		return err
	}

	httpRecv, err := callback.NewHTTPHandler(callback.HTTPConfig{
		PublicDomain: consoleDomain, MaxBodyBytes: 64 << 10, MaxHeaderBytes: 16 << 10, MaxQueryBytes: 4 << 10,
	}, store, nil, logger)
	if err != nil {
		return err
	}
	httpAddr, err := serve(&cl, httpRecv)
	if err != nil {
		return err
	}
	dnsAddr, err := serveDNS(&cl, store, logger)
	if err != nil {
		return err
	}

	labCfg := lab.Defaults()
	labCfg.CallbackZone = consoleDomain
	labCfg.OASTHTTPTarget = httpAddr
	labCfg.OASTDNSAddr = dnsAddr
	labCfg.EnableHTTPCallback = true
	labCfg.EnableDNSCallback = true
	labServer, err := lab.NewServer(labCfg, logger)
	if err != nil {
		return err
	}
	labAddr, err := serve(&cl, labServer.Handler())
	if err != nil {
		return err
	}

	mcpServer, err := mcp.New(mcp.Config{Service: svc, PublicDomain: consoleDomain, HTTPPort: portOf(httpAddr), Logger: logger})
	if err != nil {
		return err
	}
	session, err := connectMCP(&cl, mcpServer)
	if err != nil {
		return err
	}

	c := &console{session: session, labBase: "http://" + labAddr, scopes: scopeList}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", c.page)
	mux.HandleFunc("GET /api/health", c.health)
	mux.HandleFunc("POST /api/create", c.create)
	mux.HandleFunc("POST /api/trigger-http", c.triggerHTTP)
	mux.HandleFunc("POST /api/trigger-dns", c.triggerDNS)
	mux.HandleFunc("POST /api/evidence", c.evidence)
	mux.HandleFunc("GET /api/tests", c.tests)
	mux.HandleFunc("POST /api/close", c.closeTest)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	logger.Warn("OAST console (LOCAL ONLY) — the intentionally vulnerable lab is enabled",
		"console", "http://"+ln.Addr().String(),
		"http_receiver", httpAddr, "dns_receiver", dnsAddr, "lab", labAddr, "scopes", scopeList)
	fmt.Printf("\n  OAST console ready:  http://%s\n  (press Ctrl+C to stop)\n\n", ln.Addr().String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	return nil
}

// console holds the shared MCP session (serialized) and the lab base URL.
type console struct {
	mu      sync.Mutex
	session *mcpsdk.ClientSession
	labBase string
	scopes  string
}

type toolErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type mcpEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *toolErr        `json:"error"`
}

// call invokes an MCP tool and returns its raw data payload or a tool error.
func (c *console) call(ctx context.Context, name string, args map[string]any) (json.RawMessage, *toolErr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	res, err := c.session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, nil, err
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return nil, nil, err
	}
	var env mcpEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, nil, err
	}
	if env.Error != nil {
		return nil, env.Error, nil
	}
	return env.Data, nil, nil
}

func (c *console) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "authorized_scopes": c.scopes,
		"warning": "local-only console; intentionally vulnerable lab is enabled",
	})
}

func (c *console) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Target           string `json:"target"`
		AuthorizationRef string `json:"authorization_ref"`
		TTLSeconds       int    `json:"ttl_seconds"`
	}
	if !decode(w, r, &in) {
		return
	}
	args := map[string]any{"target": in.Target, "authorization_ref": in.AuthorizationRef}
	if in.TTLSeconds > 0 {
		args["ttl_seconds"] = in.TTLSeconds
	}
	c.proxy(w, r, "oast_create_test", args)
}

func (c *console) evidence(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TestID        string  `json:"test_id"`
		AfterSequence *uint64 `json:"after_sequence"`
	}
	if !decode(w, r, &in) {
		return
	}
	args := map[string]any{"test_id": in.TestID}
	if in.AfterSequence != nil {
		args["after_sequence"] = *in.AfterSequence
	}
	c.proxy(w, r, "oast_get_evidence", args)
}

func (c *console) tests(w http.ResponseWriter, r *http.Request) {
	c.proxy(w, r, "oast_list_tests", map[string]any{"include_closed": true})
}

func (c *console) closeTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TestID string `json:"test_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	c.proxy(w, r, "oast_close_test", map[string]any{"test_id": in.TestID})
}

// proxy runs an MCP tool and relays its data or a structured error (HTTP 422).
func (c *console) proxy(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	data, terr, err := c.call(ctx, name, args)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]string{"code": "internal", "message": "MCP call failed"}})
		return
	}
	if terr != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": terr})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (c *console) triggerHTTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &in) {
		return
	}
	c.relayLab(w, r, "/lab/http-fetch", map[string]any{"url": in.URL})
}

func (c *console) triggerDNS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hostname string `json:"hostname"`
	}
	if !decode(w, r, &in) {
		return
	}
	c.relayLab(w, r, "/lab/dns-lookup", map[string]any{"hostname": in.Hostname})
}

// relayLab forwards a trigger to the vulnerable lab and relays its response.
func (c *console) relayLab(w http.ResponseWriter, r *http.Request, path string, body map[string]any) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.labBase+path, strings.NewReader(string(raw)))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not build lab request"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "lab unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(payload)
}

func (c *console) page(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, pageHTML)
}

// --- wiring helpers ---------------------------------------------------------

func serve(cl *cleanup, h http.Handler) (string, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(consoleHost, "0"))
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	cl.add(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return ln.Addr().String(), nil
}

func serveDNS(cl *cleanup, store registry.EvidenceSink, logger *slog.Logger) (string, error) {
	h, err := callback.NewDNSHandler(callback.DNSConfig{
		Zone: consoleDomain, IPv4: net.ParseIP(consoleHost), IPv6: net.ParseIP("::1"),
	}, store, nil, logger)
	if err != nil {
		return "", err
	}
	udp, err := net.ListenPacket("udp", net.JoinHostPort(consoleHost, "0"))
	if err != nil {
		return "", err
	}
	port := udp.LocalAddr().(*net.UDPAddr).Port
	tcp, err := net.Listen("tcp", net.JoinHostPort(consoleHost, strconv.Itoa(port)))
	if err != nil {
		_ = udp.Close()
		return "", err
	}
	udpSrv := &dns.Server{PacketConn: udp, Handler: h}
	tcpSrv := &dns.Server{Listener: tcp, Handler: h}
	go func() { _ = udpSrv.ActivateAndServe() }()
	go func() { _ = tcpSrv.ActivateAndServe() }()
	cl.add(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = udpSrv.ShutdownContext(ctx)
		_ = tcpSrv.ShutdownContext(ctx)
		_ = udp.Close()
		_ = tcp.Close()
	})
	return udp.LocalAddr().String(), nil
}

func connectMCP(cl *cleanup, server *mcp.Server) (*mcpsdk.ClientSession, error) {
	ctx := context.Background()
	st, ct := mcpsdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st)
	if err != nil {
		return nil, err
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "oast-console", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		_ = ss.Close()
		return nil, err
	}
	cl.add(func() { _ = cs.Close(); _ = ss.Close() })
	return cs, nil
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	if err := dec.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func splitScopes(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("OAST_CONSOLE_ADDR %q is not host:port: %w", addr, err)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("OAST_CONSOLE_ADDR must be loopback (the console enables a vulnerable lab); got %q", host)
		}
		return nil
	}
	if strings.ToLower(host) != "localhost" {
		return fmt.Errorf("OAST_CONSOLE_ADDR must be loopback or localhost; got %q", host)
	}
	return nil
}

func portOf(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}
