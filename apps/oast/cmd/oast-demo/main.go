// Command oast-demo runs the full OAST workflow end-to-end in a single
// process, with no LLM API key and no external network access.
//
// It is the deterministic, reviewer-facing demonstration: it starts the OAST
// engine (HTTP + authoritative DNS callback receivers) on loopback, starts the
// intentionally vulnerable lab with its demo behaviors explicitly enabled,
// connects a real MCP client to a real MCP server over an in-process
// transport, and then drives the documented workflow:
//
//  1. configure a local authorized scope
//  2. start the OAST service
//  3. start the isolated lab with demo behaviors enabled
//  4. connect through an MCP client
//  5. create a test through MCP
//  6. trigger a controlled HTTP callback
//  7. retrieve the HTTP evidence through MCP
//  8. create a separate test and trigger a DNS callback
//  9. retrieve the DNS evidence through MCP
//  10. close both tests and show the resulting lifecycle state
//
// Every listener and session is cleaned up on exit, including on failure. Any
// failed step aborts the demo with a non-zero exit code; there is no silent
// fallback to a weaker path.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	demoDomain = "oast.local"
	demoHost   = "127.0.0.1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nDEMO FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nDEMO OK: full HTTP and DNS OAST callbacks were observed through MCP.")
}

// cleanup collects teardown functions run in reverse order on exit.
type cleanup struct{ fns []func() }

func (c *cleanup) add(fn func()) { c.fns = append(c.fns, fn) }
func (c *cleanup) run() {
	for i := len(c.fns) - 1; i >= 0; i-- {
		c.fns[i]()
	}
}

func run() (err error) {
	var cl cleanup
	defer cl.run()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil)) // keep demo output clean

	// --- 1. Configure a local authorized scope -----------------------------
	step(1, "configure a local authorized scope: lab.local")
	scopes, err := scope.New([]string{"lab.local"})
	if err != nil {
		return fmt.Errorf("scope.New: %w", err)
	}

	// --- 2. Start the OAST service (registry + receivers) -------------------
	step(2, "start the OAST service (HTTP + DNS callback receivers)")
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          100,
		MaxTestLifetime:         time.Hour,
		MaxEvidencePerTest:      500,
		MaxEvidenceBytesPerTest: 1 << 20,
	}, clock.System{}, token.CryptoGenerator{})
	if err != nil {
		return fmt.Errorf("registry.NewMemory: %w", err)
	}
	svc, err := service.New(store, scopes, service.Config{DefaultTTL: 15 * time.Minute, MaxTTL: time.Hour})
	if err != nil {
		return fmt.Errorf("service.New: %w", err)
	}

	httpAddr, err := startHTTPReceiver(&cl, store, logger)
	if err != nil {
		return fmt.Errorf("start HTTP receiver: %w", err)
	}
	httpPort := portOf(httpAddr)
	dnsAddr, err := startDNSReceiver(&cl, store, logger)
	if err != nil {
		return fmt.Errorf("start DNS receiver: %w", err)
	}
	fmt.Printf("    HTTP receiver: http://%s   DNS receiver: %s (zone %s)\n", httpAddr, dnsAddr, demoDomain)

	// --- 3. Start the isolated lab with demo behaviors enabled --------------
	step(3, "start the isolated lab (HTTP + DNS demos explicitly enabled)")
	labCfg := lab.Defaults()
	labCfg.CallbackZone = demoDomain
	labCfg.OASTHTTPTarget = httpAddr
	labCfg.OASTDNSAddr = dnsAddr
	labCfg.EnableHTTPCallback = true
	labCfg.EnableDNSCallback = true
	labServer, err := lab.NewServer(labCfg, logger)
	if err != nil {
		return fmt.Errorf("lab.NewServer: %w", err)
	}
	labAddr, err := startListener(&cl, labServer.Handler())
	if err != nil {
		return fmt.Errorf("start lab: %w", err)
	}
	fmt.Printf("    lab control endpoint: http://%s (pinned to the loopback OAST receivers only)\n", labAddr)

	// --- 4. Connect through an MCP client -----------------------------------
	step(4, "connect an MCP client to the OAST MCP server")
	mcpServer, err := mcp.New(mcp.Config{Service: svc, PublicDomain: demoDomain, HTTPPort: httpPort, Logger: logger})
	if err != nil {
		return fmt.Errorf("mcp.New: %w", err)
	}
	client, err := connectMCP(&cl, mcpServer)
	if err != nil {
		return fmt.Errorf("connect MCP: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// --- 5. Create a test through MCP (HTTP scenario) -----------------------
	step(5, "create a test through MCP (oast_create_test)")
	httpTest, err := createTest(ctx, client, "http://lab.local/checkout", "engagement-demo-http")
	if err != nil {
		return err
	}
	fmt.Printf("    test_id=%s\n    http_callback_url=%s\n", httpTest.TestID, httpTest.HTTPCallbackURL)

	// --- 6. Trigger a controlled HTTP callback ------------------------------
	step(6, "trigger the lab's controlled HTTP callback (Scenario A)")
	if err := labPost(ctx, "http://"+labAddr+"/lab/http-fetch", map[string]any{"url": httpTest.HTTPCallbackURL}); err != nil {
		return fmt.Errorf("lab http-fetch: %w", err)
	}

	// --- 7. Retrieve the HTTP evidence through MCP --------------------------
	step(7, "retrieve HTTP evidence through MCP (oast_get_evidence)")
	httpEv, err := getEvidence(ctx, client, httpTest.TestID, nil, 5)
	if err != nil {
		return err
	}
	if err := requireProtocol(httpEv, httpTest.TestID, "http"); err != nil {
		return err
	}
	fmt.Printf("    observed %d HTTP record(s); first assessment=%q\n", len(httpEv.Evidence), httpEv.Evidence[0].Assessment)

	// --- 8. Create a separate test and trigger a DNS callback ---------------
	step(8, "create a separate test and trigger the lab's DNS callback (Scenario B)")
	dnsTest, err := createTest(ctx, client, "lab.local", "engagement-demo-dns")
	if err != nil {
		return err
	}
	fmt.Printf("    test_id=%s\n    dns_callback_hostname=%s\n", dnsTest.TestID, dnsTest.DNSCallbackHostname)
	if err := labPost(ctx, "http://"+labAddr+"/lab/dns-lookup", map[string]any{"hostname": dnsTest.DNSCallbackHostname}); err != nil {
		return fmt.Errorf("lab dns-lookup: %w", err)
	}

	// --- 9. Retrieve the DNS evidence through MCP ---------------------------
	step(9, "retrieve DNS evidence through MCP (oast_get_evidence)")
	dnsEv, err := getEvidence(ctx, client, dnsTest.TestID, nil, 5)
	if err != nil {
		return err
	}
	if err := requireProtocol(dnsEv, dnsTest.TestID, "dns"); err != nil {
		return err
	}
	fmt.Printf("    observed %d DNS record(s); first name=%q\n", len(dnsEv.Evidence), firstDNSName(dnsEv))

	// --- 10. Close both tests and show the lifecycle state ------------------
	step(10, "close both tests (oast_close_test) and show lifecycle state")
	for _, tid := range []string{httpTest.TestID, dnsTest.TestID} {
		out, err := closeTest(ctx, client, tid)
		if err != nil {
			return err
		}
		fmt.Printf("    %s -> state=%s evidence_count=%d\n", out.TestID, out.State, out.EvidenceCount)
		if out.State != "closed" {
			return fmt.Errorf("test %s did not reach closed state (got %q)", tid, out.State)
		}
	}
	return nil
}

func step(n int, msg string) { fmt.Printf("[%2d/10] %s\n", n, msg) }

// --- engine wiring helpers --------------------------------------------------

func startHTTPReceiver(cl *cleanup, store registry.EvidenceSink, logger *slog.Logger) (string, error) {
	h, err := callback.NewHTTPHandler(callback.HTTPConfig{
		PublicDomain: demoDomain, MaxBodyBytes: 64 << 10, MaxHeaderBytes: 16 << 10, MaxQueryBytes: 4 << 10,
	}, store, nil, logger)
	if err != nil {
		return "", err
	}
	return startListener(cl, h)
}

func startListener(cl *cleanup, handler http.Handler) (string, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(demoHost, "0"))
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	cl.add(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return ln.Addr().String(), nil
}

func startDNSReceiver(cl *cleanup, store registry.EvidenceSink, logger *slog.Logger) (string, error) {
	h, err := callback.NewDNSHandler(callback.DNSConfig{
		Zone: demoDomain, IPv4: net.ParseIP(demoHost), IPv6: net.ParseIP("::1"),
	}, store, nil, logger)
	if err != nil {
		return "", err
	}
	udp, err := net.ListenPacket("udp", net.JoinHostPort(demoHost, "0"))
	if err != nil {
		return "", err
	}
	port := udp.LocalAddr().(*net.UDPAddr).Port
	tcp, err := net.Listen("tcp", net.JoinHostPort(demoHost, strconv.Itoa(port)))
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
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		return nil, err
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "oast-demo", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		return nil, err
	}
	cl.add(func() { _ = clientSession.Close(); _ = serverSession.Close() })
	return clientSession, nil
}

// --- MCP call helpers (parse the documented JSON tool contracts) ------------

type mcpEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type createOut struct {
	TestID              string `json:"test_id"`
	State               string `json:"state"`
	HTTPCallbackURL     string `json:"http_callback_url"`
	DNSCallbackHostname string `json:"dns_callback_hostname"`
}

type evRecord struct {
	TestID     string `json:"test_id"`
	Protocol   string `json:"protocol"`
	Assessment string `json:"assessment"`
	DNS        *struct {
		Name string `json:"name"`
	} `json:"dns"`
}

type evidenceOut struct {
	TestID   string     `json:"test_id"`
	State    string     `json:"state"`
	Evidence []evRecord `json:"evidence"`
}

type closeOut struct {
	TestID        string `json:"test_id"`
	State         string `json:"state"`
	EvidenceCount int    `json:"evidence_count"`
}

func callTool(ctx context.Context, s *mcpsdk.ClientSession, name string, args any, dst any) error {
	res, err := s.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return fmt.Errorf("%s: transport error: %w", name, err)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return fmt.Errorf("%s: marshal result: %w", name, err)
	}
	var env mcpEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s: decode envelope: %w", name, err)
	}
	if res.IsError || env.Error != nil {
		return fmt.Errorf("%s: tool error: %+v", name, env.Error)
	}
	if dst != nil {
		if err := json.Unmarshal(env.Data, dst); err != nil {
			return fmt.Errorf("%s: decode data: %w", name, err)
		}
	}
	return nil
}

func createTest(ctx context.Context, s *mcpsdk.ClientSession, target, authRef string) (createOut, error) {
	var out createOut
	err := callTool(ctx, s, "oast_create_test", map[string]any{
		"target": target, "authorization_ref": authRef,
	}, &out)
	if err == nil && out.State != "active" {
		return out, fmt.Errorf("new test is not active: %q", out.State)
	}
	return out, err
}

func getEvidence(ctx context.Context, s *mcpsdk.ClientSession, testID string, after *uint64, waitSeconds int) (evidenceOut, error) {
	args := map[string]any{"test_id": testID, "wait_seconds": waitSeconds}
	if after != nil {
		args["after_sequence"] = *after
	}
	var out evidenceOut
	err := callTool(ctx, s, "oast_get_evidence", args, &out)
	return out, err
}

func closeTest(ctx context.Context, s *mcpsdk.ClientSession, testID string) (closeOut, error) {
	var out closeOut
	err := callTool(ctx, s, "oast_close_test", map[string]any{"test_id": testID}, &out)
	return out, err
}

func requireProtocol(ev evidenceOut, testID, proto string) error {
	if len(ev.Evidence) == 0 {
		return fmt.Errorf("no %s evidence was observed for test %s", proto, testID)
	}
	for _, rec := range ev.Evidence {
		if rec.TestID != testID {
			return fmt.Errorf("evidence attributed to %s, expected %s", rec.TestID, testID)
		}
		if rec.Protocol == proto {
			return nil
		}
	}
	return fmt.Errorf("no %s-protocol record among %d evidence record(s)", proto, len(ev.Evidence))
}

func firstDNSName(ev evidenceOut) string {
	for _, rec := range ev.Evidence {
		if rec.Protocol == "dns" && rec.DNS != nil {
			return rec.DNS.Name
		}
	}
	return ""
}

func labPost(ctx context.Context, endpoint string, body map[string]any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
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
