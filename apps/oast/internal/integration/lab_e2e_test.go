package integration

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kekubhai/Loadline/apps/oast/internal/callback"
	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
	"github.com/kekubhai/Loadline/apps/oast/internal/evidence"
	"github.com/kekubhai/Loadline/apps/oast/internal/mcp"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
	"github.com/kekubhai/Loadline/apps/oast/internal/service"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
	"github.com/kekubhai/Loadline/apps/oast/lab"
)

// Minimal mirrors of the MCP tool JSON contracts. The real output structs are
// unexported in internal/mcp, so this external test parses the structured
// result by its documented JSON shape.
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

type evidenceOut struct {
	TestID            string            `json:"test_id"`
	State             string            `json:"state"`
	Evidence          []evidence.Record `json:"evidence"`
	NextAfterSequence *uint64           `json:"next_after_sequence"`
}

// TestLabEndToEndThroughMCP runs the full Phase 3 acceptance workflow:
// an in-memory MCP client creates a scoped test, the intentionally vulnerable
// lab is driven to produce real HTTP and DNS callbacks against the live OAST
// receivers, and the evidence is retrieved back through MCP.
func TestLabEndToEndThroughMCP(t *testing.T) {
	// --- 1. OAST engine with isolated local configuration -------------------
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          10,
		MaxTestLifetime:         time.Hour,
		MaxEvidencePerTest:      50,
		MaxEvidenceBytesPerTest: 1024 * 1024,
	}, clock.System{}, token.CryptoGenerator{})
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	scopes, err := scope.New([]string{"lab.local"})
	if err != nil {
		t.Fatalf("scope.New() error = %v", err)
	}
	svc, err := service.New(store, scopes, service.Config{
		DefaultTTL: 10 * time.Minute,
		MaxTTL:     time.Hour,
	})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}

	// Live HTTP callback receiver.
	httpHandler, err := callback.NewHTTPHandler(callback.HTTPConfig{
		PublicDomain:   domain,
		MaxBodyBytes:   4096,
		MaxHeaderBytes: 8192,
		MaxQueryBytes:  1024,
	}, store, nil, nil)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}
	httpSrv := httptest.NewServer(httpHandler)
	defer httpSrv.Close()
	httpPort := mustPort(t, httpSrv.URL)

	// Live authoritative DNS callback receiver on a random loopback port.
	dnsHandler, err := callback.NewDNSHandler(callback.DNSConfig{
		Zone: domain,
		IPv4: net.ParseIP(callbackHost),
		IPv6: net.ParseIP("::1"),
	}, store, nil, nil)
	if err != nil {
		t.Fatalf("NewDNSHandler() error = %v", err)
	}
	dnsAddr := startDNS(t, dnsHandler)

	// --- 2. MCP server + in-memory client -----------------------------------
	mcpServer, err := mcp.New(mcp.Config{
		Service:      svc,
		PublicDomain: domain,
		HTTPPort:     httpPort,
	})
	if err != nil {
		t.Fatalf("mcp.New() error = %v", err)
	}
	client := connectMCP(t, mcpServer)

	// --- 3. Create an authorized test through MCP ---------------------------
	var created createOut
	callMCP(t, client, "oast_create_test", map[string]any{
		"target":            "http://lab.local/checkout",
		"authorization_ref": "engagement-e2e",
	}, &created)
	if created.TestID == "" || created.State != "active" {
		t.Fatalf("unexpected create output: %+v", created)
	}

	// --- Start the intentionally vulnerable lab with demos enabled ----------
	labCfg := lab.Defaults()
	labCfg.CallbackZone = domain
	labCfg.OASTHTTPTarget = httpSrv.Listener.Addr().String()
	labCfg.OASTDNSAddr = dnsAddr
	labCfg.EnableHTTPCallback = true
	labCfg.EnableDNSCallback = true
	labCfg.HTTPTimeout = 2 * time.Second
	labCfg.DNSTimeout = 2 * time.Second
	labServer, err := lab.NewServer(labCfg, nil)
	if err != nil {
		t.Fatalf("lab.NewServer() error = %v", err)
	}
	labSrv := httptest.NewServer(labServer.Handler())
	defer labSrv.Close()

	// --- 4/5. Trigger the lab's controlled HTTP callback --------------------
	labPost(t, labSrv.URL+"/lab/http-fetch", map[string]any{
		"url": created.HTTPCallbackURL,
	}, http.StatusOK)

	// --- 6/7. Retrieve HTTP evidence and verify test + protocol -------------
	var httpEv evidenceOut
	callMCP(t, client, "oast_get_evidence", map[string]any{
		"test_id":      created.TestID,
		"wait_seconds": 3,
	}, &httpEv)
	if len(httpEv.Evidence) == 0 {
		t.Fatal("no HTTP evidence retrieved through MCP")
	}
	first := httpEv.Evidence[0]
	if first.TestID != created.TestID {
		t.Fatalf("evidence test id = %q, want %q", first.TestID, created.TestID)
	}
	if first.Protocol != evidence.ProtocolHTTP || first.HTTP == nil {
		t.Fatalf("expected HTTP evidence, got %+v", first)
	}
	if first.Assessment != evidence.AssessmentObservedOnly {
		t.Fatalf("assessment = %q", first.Assessment)
	}
	if first.HTTP.Path != "/" {
		t.Fatalf("unexpected callback path %q", first.HTTP.Path)
	}

	// --- 8. Trigger the lab's controlled DNS callback -----------------------
	labPost(t, labSrv.URL+"/lab/dns-lookup", map[string]any{
		"hostname": created.DNSCallbackHostname,
	}, http.StatusOK)

	// Retrieve only new evidence via the sequence cursor and verify DNS.
	var dnsEv evidenceOut
	callMCP(t, client, "oast_get_evidence", map[string]any{
		"test_id":        created.TestID,
		"after_sequence": *httpEv.NextAfterSequence,
		"wait_seconds":   3,
	}, &dnsEv)
	if len(dnsEv.Evidence) == 0 {
		t.Fatal("no DNS evidence retrieved through MCP")
	}
	sawDNS := false
	for _, rec := range dnsEv.Evidence {
		if rec.TestID != created.TestID {
			t.Fatalf("DNS evidence test id = %q, want %q", rec.TestID, created.TestID)
		}
		if rec.Protocol == evidence.ProtocolDNS && rec.DNS != nil {
			sawDNS = true
			if !strings.HasPrefix(strings.ToLower(rec.DNS.Name), strings.ToLower(created.DNSCallbackHostname)) {
				t.Fatalf("DNS evidence name = %q, want prefix %q", rec.DNS.Name, created.DNSCallbackHostname)
			}
		}
	}
	if !sawDNS {
		t.Fatalf("no DNS-protocol evidence among %d new records", len(dnsEv.Evidence))
	}
}

// TestLabDNSCallbackIsIsolatedPerTest creates two separate tests through MCP,
// drives a DNS callback for the second one only, and confirms the evidence is
// attributed to the correct test and does not leak into the first.
func TestLabDNSCallbackIsIsolatedPerTest(t *testing.T) {
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          10,
		MaxTestLifetime:         time.Hour,
		MaxEvidencePerTest:      50,
		MaxEvidenceBytesPerTest: 1024 * 1024,
	}, clock.System{}, token.CryptoGenerator{})
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	scopes, err := scope.New([]string{"lab.local"})
	if err != nil {
		t.Fatalf("scope.New() error = %v", err)
	}
	svc, err := service.New(store, scopes, service.Config{DefaultTTL: 10 * time.Minute, MaxTTL: time.Hour})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}

	httpHandler, err := callback.NewHTTPHandler(callback.HTTPConfig{
		PublicDomain: domain, MaxBodyBytes: 4096, MaxHeaderBytes: 8192, MaxQueryBytes: 1024,
	}, store, nil, nil)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}
	httpSrv := httptest.NewServer(httpHandler)
	defer httpSrv.Close()

	dnsHandler, err := callback.NewDNSHandler(callback.DNSConfig{
		Zone: domain, IPv4: net.ParseIP(callbackHost), IPv6: net.ParseIP("::1"),
	}, store, nil, nil)
	if err != nil {
		t.Fatalf("NewDNSHandler() error = %v", err)
	}
	dnsAddr := startDNS(t, dnsHandler)

	mcpServer, err := mcp.New(mcp.Config{Service: svc, PublicDomain: domain, HTTPPort: mustPort(t, httpSrv.URL)})
	if err != nil {
		t.Fatalf("mcp.New() error = %v", err)
	}
	client := connectMCP(t, mcpServer)

	var quiet, active createOut
	callMCP(t, client, "oast_create_test", map[string]any{"target": "lab.local", "authorization_ref": "ref-quiet"}, &quiet)
	callMCP(t, client, "oast_create_test", map[string]any{"target": "lab.local", "authorization_ref": "ref-active"}, &active)

	labCfg := lab.Defaults()
	labCfg.CallbackZone = domain
	labCfg.OASTHTTPTarget = httpSrv.Listener.Addr().String()
	labCfg.OASTDNSAddr = dnsAddr
	labCfg.EnableDNSCallback = true
	labServer, err := lab.NewServer(labCfg, nil)
	if err != nil {
		t.Fatalf("lab.NewServer() error = %v", err)
	}
	labSrv := httptest.NewServer(labServer.Handler())
	defer labSrv.Close()

	labPost(t, labSrv.URL+"/lab/dns-lookup", map[string]any{"hostname": active.DNSCallbackHostname}, http.StatusOK)

	var activeEv evidenceOut
	callMCP(t, client, "oast_get_evidence", map[string]any{"test_id": active.TestID, "wait_seconds": 3}, &activeEv)
	if len(activeEv.Evidence) == 0 {
		t.Fatal("no evidence for the active test")
	}
	for _, rec := range activeEv.Evidence {
		if rec.Protocol != evidence.ProtocolDNS {
			t.Fatalf("expected only DNS evidence, got %q", rec.Protocol)
		}
	}

	var quietEv evidenceOut
	callMCP(t, client, "oast_get_evidence", map[string]any{"test_id": quiet.TestID}, &quietEv)
	if len(quietEv.Evidence) != 0 {
		t.Fatalf("evidence leaked into the untouched test: %+v", quietEv.Evidence)
	}
}

// --- helpers ----------------------------------------------------------------

func mustPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("port from %q: %v", raw, err)
	}
	return p
}

func startDNS(t *testing.T, handler dns.Handler) string {
	t.Helper()
	udpConn, err := net.ListenPacket("udp", net.JoinHostPort(callbackHost, "0"))
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	port := udpConn.LocalAddr().(*net.UDPAddr).Port
	tcpListener, err := net.Listen("tcp", net.JoinHostPort(callbackHost, strconv.Itoa(port)))
	if err != nil {
		_ = udpConn.Close()
		t.Fatalf("Listen() error = %v", err)
	}
	udpServer := &dns.Server{PacketConn: udpConn, Handler: handler}
	tcpServer := &dns.Server{Listener: tcpListener, Handler: handler}
	go func() { _ = udpServer.ActivateAndServe() }()
	go func() { _ = tcpServer.ActivateAndServe() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = udpServer.ShutdownContext(ctx)
		_ = tcpServer.ShutdownContext(ctx)
		_ = udpConn.Close()
		_ = tcpListener.Close()
	})
	return udpConn.LocalAddr().String()
}

func connectMCP(t *testing.T, server *mcp.Server) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		t.Fatalf("server connect error = %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "e2e", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect error = %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession
}

func callMCP(t *testing.T, session *mcpsdk.ClientSession, name string, args any, dst any) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%s) transport error = %v", name, err)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal %s result: %v", name, err)
	}
	var env mcpEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode %s envelope: %v", name, err)
	}
	if res.IsError || env.Error != nil {
		t.Fatalf("CallTool(%s) returned error: isError=%v err=%+v", name, res.IsError, env.Error)
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		t.Fatalf("decode %s data: %v", name, err)
	}
}

func labPost(t *testing.T, endpoint string, body map[string]any, wantStatus int) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal lab body: %v", err)
	}
	resp, err := http.Post(endpoint, "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("POST %s error = %v", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s status = %d, want %d", endpoint, resp.StatusCode, wantStatus)
	}
}
