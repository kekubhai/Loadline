package callback

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
	"github.com/kekubhai/Loadline/apps/oast/internal/evidence"
	"github.com/kekubhai/Loadline/apps/oast/internal/ratelimit"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
)

const testDomain = "oast.local"

func newHTTPStore(t *testing.T) (*registry.Memory, *clock.Manual) {
	t.Helper()
	clk := clock.NewManual(time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC))
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          10,
		MaxTestLifetime:         time.Hour,
		MaxEvidencePerTest:      50,
		MaxEvidenceBytesPerTest: 1024 * 1024,
	}, clk, token.CryptoGenerator{})
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	return store, clk
}

func newHTTPTest(t *testing.T, store *registry.Memory, limiter *ratelimit.Limiter) *HTTPHandler {
	t.Helper()
	h, err := NewHTTPHandler(HTTPConfig{
		PublicDomain:   testDomain,
		MaxBodyBytes:   1024,
		MaxHeaderBytes: 4096,
		MaxQueryBytes:  512,
	}, store, limiter, nil)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}
	return h
}

func createCallbackTest(t *testing.T, store *registry.Memory, ttl time.Duration) *registry.Test {
	t.Helper()
	created, err := store.Create(context.Background(), registry.CreateRequest{
		Target:           "lab.local",
		AuthorizationRef: "engagement",
		TTL:              ttl,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return created
}

func callbackRequest(method, host, path string, body string) *http.Request {
	target := "http://" + testDomain + path
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = host
	req.RemoteAddr = "203.0.113.7:5555"
	return req
}

func TestHTTPCallbackCapturesEvidence(t *testing.T) {
	store, _ := newHTTPStore(t)
	created := createCallbackTest(t, store, time.Minute)
	h := newHTTPTest(t, store, nil)

	req := callbackRequest(http.MethodPost,
		created.Token+"."+testDomain+":8081",
		"/callback%2Fpath?a=1&a=2&b=hello",
		"name=value",
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "oast-test")
	req.Header.Set("Authorization", "Bearer supersecret")
	req.Header.Set("Cookie", "session=supersecret")
	req.Header.Set("Set-Cookie", "session=supersecret")
	req.Header.Set("X-Api-Key", "supersecret")
	req.Header.Set("Proxy-Authorization", "Basic supersecret")
	req.Header.Set("X-Forwarded-For", "198.51.100.99")
	req.Header.Set("X-Forwarded-Host", "evil.example.net")
	req.Header.Set("Forwarded", "for=198.51.100.99;host=evil.example.net")
	req.Header.Set("X-Test", "visible")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "ok\n" {
		t.Fatalf("body = %q, want static ok", rr.Body.String())
	}
	if rr.Header().Get("Location") != "" || rr.Header().Get("Set-Cookie") != "" {
		t.Fatalf("unexpected response headers: %#v", rr.Header())
	}

	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 1 {
		t.Fatalf("evidence count = %d, want 1", len(got.Evidence))
	}
	rec := got.Evidence[0]
	if rec.Protocol != evidence.ProtocolHTTP || rec.Assessment != evidence.AssessmentObservedOnly {
		t.Fatalf("record metadata = %#v", rec)
	}
	if rec.SourceIP != "203.0.113.7" {
		t.Fatalf("SourceIP = %q, forwarded headers must not be trusted", rec.SourceIP)
	}
	if rec.HTTP == nil {
		t.Fatal("missing HTTP interaction")
	}
	if rec.HTTP.Method != http.MethodPost {
		t.Fatalf("Method = %q", rec.HTTP.Method)
	}
	if rec.HTTP.Host != created.Token+"."+testDomain {
		t.Fatalf("Host = %q", rec.HTTP.Host)
	}
	if rec.HTTP.RawHost != created.Token+"."+testDomain+":8081" {
		t.Fatalf("RawHost = %q", rec.HTTP.RawHost)
	}
	if rec.HTTP.Path != "/callback%2Fpath" {
		t.Fatalf("Path = %q", rec.HTTP.Path)
	}
	if got := rec.HTTP.Query["a"]; len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("Query[a] = %#v", got)
	}
	if got := rec.HTTP.Query["b"]; len(got) != 1 || got[0] != "hello" {
		t.Fatalf("Query[b] = %#v", got)
	}
	if string(rec.HTTP.Body) != "name=value" {
		t.Fatalf("Body = %q", rec.HTTP.Body)
	}
	if rec.HTTP.BodySHA256 != evidence.HashBody([]byte("name=value")) {
		t.Fatalf("BodySHA256 = %q", rec.HTTP.BodySHA256)
	}
	if rec.HTTP.BodyCapturedBytes != len("name=value") || rec.HTTP.BodyTruncated {
		t.Fatalf("body metadata = %#v", rec.HTTP)
	}
	if rec.HTTP.ContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("ContentType = %q", rec.HTTP.ContentType)
	}
	if !rec.HTTP.HeadersRedacted {
		t.Fatal("HeadersRedacted = false")
	}
	for _, name := range []string{"Authorization", "Cookie", "Set-Cookie", "X-Api-Key", "Proxy-Authorization"} {
		values := rec.HTTP.Headers[name]
		if len(values) == 0 || values[0] != "[REDACTED]" {
			t.Fatalf("header %s = %#v, want redacted", name, values)
		}
	}
	if values := rec.HTTP.Headers["X-Test"]; len(values) != 1 || values[0] != "visible" {
		t.Fatalf("X-Test = %#v", values)
	}
	raw, _ := json.Marshal(rec)
	if strings.Contains(string(raw), "supersecret") {
		t.Fatal("sensitive header value leaked into evidence JSON")
	}
}

func TestHTTPCallbackBodyTruncation(t *testing.T) {
	store, _ := newHTTPStore(t)
	created := createCallbackTest(t, store, time.Minute)
	h, err := NewHTTPHandler(HTTPConfig{
		PublicDomain:   testDomain,
		MaxBodyBytes:   5,
		MaxHeaderBytes: 4096,
		MaxQueryBytes:  512,
	}, store, nil, nil)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}

	req := callbackRequest(http.MethodPost, created.Token+"."+testDomain, "/", "0123456789")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	rec := got.Evidence[0].HTTP
	if len(got.Evidence) != 1 || rec == nil {
		t.Fatalf("missing evidence: %#v", got.Evidence)
	}
	if string(rec.Body) != "01234" {
		t.Fatalf("Body = %q", rec.Body)
	}
	if !rec.BodyTruncated || rec.BodyCapturedBytes != 5 || rec.DeclaredContentLength != 10 {
		t.Fatalf("body metadata = %#v", rec)
	}
	if rec.BodySHA256 != evidence.HashBody([]byte("01234")) {
		t.Fatalf("BodySHA256 = %q", rec.BodySHA256)
	}
}

func TestHTTPCallbackRejectsOversizedHeadersAndURI(t *testing.T) {
	store, _ := newHTTPStore(t)
	created := createCallbackTest(t, store, time.Minute)
	h, err := NewHTTPHandler(HTTPConfig{
		PublicDomain:   testDomain,
		MaxBodyBytes:   1024,
		MaxHeaderBytes: 256,
		MaxQueryBytes:  128,
	}, store, nil, nil)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}

	req := callbackRequest(http.MethodGet, created.Token+"."+testDomain, "/", "")
	req.Header.Set("X-Large", strings.Repeat("a", 512))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("header status = %d, want 431", rr.Code)
	}

	req = callbackRequest(http.MethodGet, created.Token+"."+testDomain, "/"+strings.Repeat("a", 300), "")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestURITooLong {
		t.Fatalf("uri status = %d, want 414", rr.Code)
	}

	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 0 {
		t.Fatalf("oversized requests created %d evidence records", len(got.Evidence))
	}
}

func TestHTTPCallbackUnknownMalformedClosedAndExpired(t *testing.T) {
	store, clk := newHTTPStore(t)
	h := newHTTPTest(t, store, nil)

	unknown, err := token.CryptoGenerator{}.New()
	if err != nil {
		t.Fatalf("token generation error = %v", err)
	}
	malformedHosts := []string{
		"",
		testDomain,
		unknown + "." + testDomain + ".evil",
		"a." + unknown + "." + testDomain,
		unknown + "." + testDomain + ":bad",
		unknown + "." + testDomain + ":",
		"[" + unknown + "." + testDomain + "]",
		unknown + "@" + testDomain,
		"not-a-token." + testDomain,
	}
	for _, host := range malformedHosts {
		req := callbackRequest(http.MethodGet, host, "/", "")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("host %q status = %d, want 404", host, rr.Code)
		}
	}

	closed := createCallbackTest(t, store, time.Minute)
	if err := store.Close(context.Background(), closed.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, callbackRequest(http.MethodGet, closed.Token+"."+testDomain, "/", ""))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("closed status = %d, want 404", rr.Code)
	}

	expired := createCallbackTest(t, store, time.Minute)
	clk.Advance(2 * time.Minute)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, callbackRequest(http.MethodGet, expired.Token+"."+testDomain, "/", ""))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expired status = %d, want 404", rr.Code)
	}
}

func TestHTTPCallbackRateLimit(t *testing.T) {
	store, _ := newHTTPStore(t)
	created := createCallbackTest(t, store, time.Minute)
	clk := clock.NewManual(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	limiter := ratelimit.New(0.001, 1, clk)
	h := newHTTPTest(t, store, limiter)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, callbackRequest(http.MethodGet, created.Token+"."+testDomain, "/one", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("first status = %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, callbackRequest(http.MethodGet, created.Token+"."+testDomain, "/two", ""))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", rr.Code)
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 1 {
		t.Fatalf("evidence count = %d, want 1", len(got.Evidence))
	}
}

func TestHTTPCallbackQueryTruncation(t *testing.T) {
	store, _ := newHTTPStore(t)
	created := createCallbackTest(t, store, time.Minute)
	h, err := NewHTTPHandler(HTTPConfig{
		PublicDomain:   testDomain,
		MaxBodyBytes:   1024,
		MaxHeaderBytes: 4096,
		MaxQueryBytes:  10,
	}, store, nil, nil)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}
	req := callbackRequest(http.MethodGet, created.Token+"."+testDomain, "/?a=1234567890&b=2", "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].HTTP == nil {
		t.Fatal("missing evidence")
	}
	httpRec := got.Evidence[0].HTTP
	if !httpRec.QueryTruncated || len(httpRec.RawQuery) != 10 {
		t.Fatalf("query metadata = %#v", httpRec)
	}
}

func TestNewHTTPHandlerValidation(t *testing.T) {
	store, _ := newHTTPStore(t)
	tests := []HTTPConfig{
		{PublicDomain: "", MaxBodyBytes: 1, MaxHeaderBytes: 1, MaxQueryBytes: 1},
		{PublicDomain: "127.0.0.1", MaxBodyBytes: 1, MaxHeaderBytes: 1, MaxQueryBytes: 1},
		{PublicDomain: testDomain, MaxBodyBytes: 0, MaxHeaderBytes: 1, MaxQueryBytes: 1},
		{PublicDomain: testDomain, MaxBodyBytes: 1, MaxHeaderBytes: 0, MaxQueryBytes: 1},
		{PublicDomain: testDomain, MaxBodyBytes: 1, MaxHeaderBytes: 1, MaxQueryBytes: 0},
	}
	for _, cfg := range tests {
		if _, err := NewHTTPHandler(cfg, store, nil, nil); err == nil {
			t.Fatalf("NewHTTPHandler(%#v) error = nil", cfg)
		}
	}
	if _, err := NewHTTPHandler(HTTPConfig{
		PublicDomain: testDomain, MaxBodyBytes: 1, MaxHeaderBytes: 1, MaxQueryBytes: 1,
	}, nil, nil, nil); err == nil {
		t.Fatal("nil sink must be rejected")
	}
}
