package lab

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	c := Defaults()
	c.EnableHTTPCallback = true
	c.EnableDNSCallback = true
	c.HTTPTimeout = time.Second
	c.DNSTimeout = time.Second
	return c
}

func newServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return s
}

func postJSON(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestConfigFailsClosedOnNonLoopbackTarget(t *testing.T) {
	cfg := Defaults()
	cfg.OASTHTTPTarget = "93.184.216.34:80"
	if _, err := NewServer(cfg, nil); err == nil {
		t.Fatal("expected NewServer to reject a non-loopback HTTP target")
	}
	cfg = Defaults()
	cfg.OASTDNSAddr = "8.8.8.8:53"
	if _, err := NewServer(cfg, nil); err == nil {
		t.Fatal("expected NewServer to reject a non-loopback DNS address")
	}
}

func TestHTTPFetchDisabledByDefault(t *testing.T) {
	cfg := Defaults() // both demos disabled
	server := newServer(t, cfg)
	rec := postJSON(t, server.Handler(), "/lab/http-fetch", httpFetchRequest{
		URL: "http://abc.oast.local:8081/",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "LAB_ENABLE_HTTP_CALLBACK") {
		t.Fatalf("missing enable hint: %s", rec.Body.String())
	}
}

func TestDNSLookupDisabledByDefault(t *testing.T) {
	server := newServer(t, Defaults())
	rec := postJSON(t, server.Handler(), "/lab/dns-lookup", dnsLookupRequest{
		Hostname: "abc.oast.local",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHTTPFetchRejectsOutOfZoneWithoutDialing(t *testing.T) {
	cfg := testConfig()
	// Point the pinned dialer at a server that would record a hit, so we can
	// prove an out-of-zone request is rejected *before* any connection.
	var dialed bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dialed = true
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	cfg.OASTHTTPTarget = backend.Listener.Addr().String()
	server := newServer(t, cfg)

	cases := []string{
		"http://metadata.google.internal/",
		"http://169.254.169.254/latest/meta-data/",
		"http://evil.example/",
		"http://oast.local/",             // apex, not a single label under zone
		"http://a.b.oast.local:8081/",    // multi-label
		"https://abc.oast.local:8081/",   // wrong scheme
		"http://user:pw@abc.oast.local/", // credentials
	}
	for _, url := range cases {
		rec := postJSON(t, server.Handler(), "/lab/http-fetch", httpFetchRequest{URL: url})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("url %q: status = %d, want 400 (body %s)", url, rec.Code, rec.Body.String())
		}
	}
	if dialed {
		t.Fatal("an out-of-zone request reached the pinned backend; validation did not fail closed")
	}
}

func TestHTTPFetchDialsOnlyPinnedTargetAndCapsBody(t *testing.T) {
	cfg := testConfig()
	cfg.MaxResponseBytes = 8
	var gotHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("0123456789ABCDEF")) // 16 bytes, exceeds cap
	}))
	defer backend.Close()
	cfg.OASTHTTPTarget = backend.Listener.Addr().String()
	server := newServer(t, cfg)

	// A valid in-zone callback URL whose authority the lab must never resolve;
	// the pinned dialer sends the connection to the backend regardless.
	_, port, _ := net.SplitHostPort(backend.Listener.Addr().String())
	rec := postJSON(t, server.Handler(), "/lab/http-fetch", httpFetchRequest{
		URL: "http://" + strings.Repeat("a", 26) + ".oast.local:" + port + "/probe",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var out httpFetchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.BytesRead != 8 || !out.Truncated {
		t.Fatalf("response cap not applied: %+v", out)
	}
	// The outbound request carried the in-zone Host, which is how the real
	// receiver maps the callback to a test token.
	if !strings.HasPrefix(gotHost, strings.Repeat("a", 26)+".oast.local") {
		t.Fatalf("backend saw Host %q, want the in-zone callback host", gotHost)
	}
}

func TestHTTPFetchRefusesRedirects(t *testing.T) {
	cfg := testConfig()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "http://elsewhere.oast.local/", http.StatusFound)
	}))
	defer backend.Close()
	cfg.OASTHTTPTarget = backend.Listener.Addr().String()
	server := newServer(t, cfg)

	rec := postJSON(t, server.Handler(), "/lab/http-fetch", httpFetchRequest{
		URL: "http://" + strings.Repeat("b", 26) + ".oast.local:9/",
	})
	// CheckRedirect returns an error, so client.Do fails -> 502 from the lab.
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 on redirect refusal (body %s)", rec.Code, rec.Body.String())
	}
}

func TestDNSLookupRejectsOutOfZone(t *testing.T) {
	server := newServer(t, testConfig())
	for _, host := range []string{"example.com", "a.b.oast.local", "oast.local", "127.0.0.1"} {
		rec := postJSON(t, server.Handler(), "/lab/dns-lookup", dnsLookupRequest{Hostname: host})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("host %q: status = %d, want 400", host, rec.Code)
		}
	}
}

func TestHealthReportsEnabledState(t *testing.T) {
	server := newServer(t, testConfig())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var h healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !h.HTTPCallbackReady || !h.DNSCallbackReady || h.Status != "ok" {
		t.Fatalf("health = %+v", h)
	}
}
