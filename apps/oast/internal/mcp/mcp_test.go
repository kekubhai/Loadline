package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
	"github.com/kekubhai/Loadline/apps/oast/internal/evidence"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
	"github.com/kekubhai/Loadline/apps/oast/internal/service"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
)

func newTestServer(t *testing.T) (*Server, *registry.Memory) {
	t.Helper()
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          10,
		MaxTestLifetime:         time.Hour,
		MaxEvidencePerTest:      100,
		MaxEvidenceBytesPerTest: 1024 * 1024,
	}, clock.System{}, token.CryptoGenerator{})
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	scopes, err := scope.New([]string{"lab.local", "*.apps.lab.test"})
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
	server, err := New(Config{
		Service:      svc,
		PublicDomain: "oast.local",
		HTTPPort:     8081,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server, store
}

func connectClient(t *testing.T, server *Server) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		t.Fatalf("server connect error = %v", err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
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

func callTool[T any](t *testing.T, session *sdk.ClientSession, name string, arguments any) (T, envelope[T], bool) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("CallTool(%s) error = %v", name, err)
	}
	if res.StructuredContent == nil {
		t.Fatalf("CallTool(%s) returned no structured content", name)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var env envelope[T]
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	var value T
	if env.Data != nil {
		value = *env.Data
	}
	return value, env, res.IsError
}

func createTest(t *testing.T, session *sdk.ClientSession, target string) createOutput {
	t.Helper()
	out, env, isErr := callTool[createOutput](t, session, "oast_create_test", map[string]any{
		"target":            target,
		"authorization_ref": "ENG-123",
	})
	if isErr || env.Error != nil {
		t.Fatalf("create test error = %+v", env.Error)
	}
	return out
}

func appendHTTP(t *testing.T, store *registry.Memory, testID string) {
	t.Helper()
	test, err := store.Get(context.Background(), testID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	_, err = store.AppendEvidence(context.Background(), test.Token, evidence.Record{
		Protocol: evidence.ProtocolHTTP,
		SourceIP: "192.0.2.10",
		HTTP: &evidence.HTTPInteraction{
			Method: "GET",
			Host:   "oast.local",
			Path:   "/probe",
		},
	})
	if err != nil {
		t.Fatalf("AppendEvidence() error = %v", err)
	}
}

func appendDNS(t *testing.T, store *registry.Memory, testID string) {
	t.Helper()
	test, err := store.Get(context.Background(), testID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	_, err = store.AppendEvidence(context.Background(), test.Token, evidence.Record{
		Protocol: evidence.ProtocolDNS,
		SourceIP: "192.0.2.53",
		DNS: &evidence.DNSInteraction{
			Name:      test.Token + ".oast.local.",
			Type:      1,
			TypeName:  "A",
			Class:     1,
			ClassName: "IN",
		},
	})
	if err != nil {
		t.Fatalf("AppendEvidence() error = %v", err)
	}
}

func TestCreateTestReturnsCallbackLocations(t *testing.T) {
	server, _ := newTestServer(t)
	session := connectClient(t, server)

	out := createTest(t, session, "lab.local")
	if out.TestID == "" || out.State != string(registry.StateActive) {
		t.Fatalf("create output = %+v", out)
	}
	if out.Assessment != evidence.AssessmentObservedOnly {
		t.Fatalf("Assessment = %q", out.Assessment)
	}
	tokenPart, ok := strings.CutSuffix(out.DNSCallbackHostname, ".oast.local")
	if !ok || len(tokenPart) != 26 {
		t.Fatalf("DNS hostname = %q", out.DNSCallbackHostname)
	}
	wantURL := "http://" + out.DNSCallbackHostname + ":8081/"
	if out.HTTPCallbackURL != wantURL {
		t.Fatalf("HTTPCallbackURL = %q, want %q", out.HTTPCallbackURL, wantURL)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal create output: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "authorization") {
		t.Fatalf("create output leaked authorization reference: %s", raw)
	}
}

func TestCreateRejectsOutOfScopeTarget(t *testing.T) {
	server, _ := newTestServer(t)
	session := connectClient(t, server)

	_, env, isErr := callTool[createOutput](t, session, "oast_create_test", map[string]any{
		"target":            "evil.example",
		"authorization_ref": "ENG-123",
	})
	if !isErr || env.Error == nil || env.Error.Code != "unauthorized" {
		t.Fatalf("out-of-scope result: isError=%v env=%+v", isErr, env)
	}
}

func TestGetEvidenceCursorAndLongPoll(t *testing.T) {
	server, store := newTestServer(t)
	session := connectClient(t, server)
	created := createTest(t, session, "lab.local")
	appendHTTP(t, store, created.TestID)

	out, env, isErr := callTool[getEvidenceOutput](t, session, "oast_get_evidence", map[string]any{
		"test_id": created.TestID,
	})
	if isErr || env.Error != nil {
		t.Fatalf("get evidence error = %+v", env.Error)
	}
	if len(out.Evidence) != 1 || out.Evidence[0].Sequence != 0 {
		t.Fatalf("evidence = %+v", out.Evidence)
	}
	if out.NextAfterSequence == nil || *out.NextAfterSequence != 0 {
		t.Fatalf("NextAfterSequence = %v", out.NextAfterSequence)
	}

	empty, _, isErr := callTool[getEvidenceOutput](t, session, "oast_get_evidence", map[string]any{
		"test_id":        created.TestID,
		"after_sequence": 0,
	})
	if isErr || len(empty.Evidence) != 0 {
		t.Fatalf("cursor result = %+v", empty)
	}

	start := time.Now()
	go func() {
		time.Sleep(50 * time.Millisecond)
		appendHTTP(t, store, created.TestID)
	}()
	waited, env, isErr := callTool[getEvidenceOutput](t, session, "oast_get_evidence", map[string]any{
		"test_id":        created.TestID,
		"after_sequence": 0,
		"wait_seconds":   2,
	})
	if isErr || env.Error != nil {
		t.Fatalf("wait evidence error = %+v", env.Error)
	}
	if len(waited.Evidence) != 1 || waited.Evidence[0].Sequence != 1 {
		t.Fatalf("waited evidence = %+v", waited.Evidence)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("long poll took %s", elapsed)
	}
}

func TestListAndClose(t *testing.T) {
	server, store := newTestServer(t)
	session := connectClient(t, server)
	first := createTest(t, session, "lab.local")
	second := createTest(t, session, "app.apps.lab.test")
	appendHTTP(t, store, first.TestID)

	active, env, isErr := callTool[listOutput](t, session, "oast_list_tests", map[string]any{})
	if isErr || env.Error != nil || active.Count != 2 {
		t.Fatalf("active list = %+v, err=%+v", active, env.Error)
	}

	closed, closeEnv, closeIsErr := callTool[closeOutput](t, session, "oast_close_test", map[string]any{
		"test_id": first.TestID,
	})
	if closeIsErr || closeEnv.Error != nil {
		t.Fatalf("close error = %+v", closeEnv.Error)
	}
	if closed.State != string(registry.StateClosed) || closed.EvidenceCount != 1 {
		t.Fatalf("close output = %+v", closed)
	}

	active, _, _ = callTool[listOutput](t, session, "oast_list_tests", map[string]any{})
	if active.Count != 1 || active.Tests[0].ID != second.TestID {
		t.Fatalf("active list after close = %+v", active)
	}
	all, _, _ := callTool[listOutput](t, session, "oast_list_tests", map[string]any{"include_closed": true})
	if all.Count != 2 {
		t.Fatalf("all list = %+v", all)
	}
	limited, _, _ := callTool[listOutput](t, session, "oast_list_tests", map[string]any{
		"include_closed": true,
		"limit":          1,
	})
	if limited.Count != 1 {
		t.Fatalf("limited list = %+v", limited)
	}

	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	firstTest, err := store.Get(context.Background(), first.TestID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if strings.Contains(string(raw), firstTest.Token) {
		t.Fatalf("list leaked callback token")
	}

	closedEvidence, evidenceEnv, evidenceIsErr := callTool[getEvidenceOutput](t, session, "oast_get_evidence", map[string]any{
		"test_id": first.TestID,
	})
	if evidenceIsErr || evidenceEnv.Error != nil || closedEvidence.State != string(registry.StateClosed) || len(closedEvidence.Evidence) != 1 {
		t.Fatalf("closed evidence result = %+v, err=%+v", closedEvidence, evidenceEnv.Error)
	}
}

func TestCloseIsIdempotentAndRejectsNewEvidence(t *testing.T) {
	server, store := newTestServer(t)
	session := connectClient(t, server)
	created := createTest(t, session, "lab.local")

	for i := 0; i < 2; i++ {
		out, env, isErr := callTool[closeOutput](t, session, "oast_close_test", map[string]any{
			"test_id": created.TestID,
		})
		if isErr || env.Error != nil || out.State != string(registry.StateClosed) {
			t.Fatalf("close %d result = %+v, err=%+v", i, out, env.Error)
		}
	}

	test, err := store.Get(context.Background(), created.TestID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	_, err = store.AppendEvidence(context.Background(), test.Token, evidence.Record{
		Protocol: evidence.ProtocolHTTP,
		HTTP:     &evidence.HTTPInteraction{Method: "GET"},
	})
	if !errors.Is(err, registry.ErrClosed) {
		t.Fatalf("AppendEvidence() after close error = %v", err)
	}
}

func TestToolErrorsAreStructured(t *testing.T) {
	server, _ := newTestServer(t)
	session := connectClient(t, server)

	_, missingEnv, missingIsErr := callTool[getEvidenceOutput](t, session, "oast_get_evidence", map[string]any{
		"test_id": "missing",
	})
	if !missingIsErr || missingEnv.Error == nil || missingEnv.Error.Code != "test_not_found" {
		t.Fatalf("missing test result: isError=%v env=%+v", missingIsErr, missingEnv)
	}

	_, waitEnv, waitIsErr := callTool[getEvidenceOutput](t, session, "oast_get_evidence", map[string]any{
		"test_id":      "missing",
		"wait_seconds": 31,
	})
	if !waitIsErr || waitEnv.Error == nil || waitEnv.Error.Code != "invalid_input" {
		t.Fatalf("invalid wait result: isError=%v env=%+v", waitIsErr, waitEnv)
	}

	_, limitEnv, limitIsErr := callTool[listOutput](t, session, "oast_list_tests", map[string]any{
		"limit": service.MaxListLimit + 1,
	})
	if !limitIsErr || limitEnv.Error == nil || limitEnv.Error.Code != "invalid_input" {
		t.Fatalf("invalid limit result: isError=%v env=%+v", limitIsErr, limitEnv)
	}
}

func TestGetEvidenceReturnsDNSCallback(t *testing.T) {
	server, store := newTestServer(t)
	session := connectClient(t, server)
	created := createTest(t, session, "lab.local")
	appendDNS(t, store, created.TestID)

	out, env, isErr := callTool[getEvidenceOutput](t, session, "oast_get_evidence", map[string]any{
		"test_id": created.TestID,
	})
	if isErr || env.Error != nil {
		t.Fatalf("get evidence error = %+v", env.Error)
	}
	if len(out.Evidence) != 1 {
		t.Fatalf("evidence = %+v", out.Evidence)
	}
	rec := out.Evidence[0]
	if rec.Protocol != evidence.ProtocolDNS || rec.DNS == nil {
		t.Fatalf("expected DNS evidence, got %+v", rec)
	}
	if rec.DNS.TypeName != "A" || rec.DNS.ClassName != "IN" {
		t.Fatalf("DNS interaction = %+v", rec.DNS)
	}
	if rec.Assessment != evidence.AssessmentObservedOnly {
		t.Fatalf("assessment = %q", rec.Assessment)
	}
}

func TestGetEvidenceWaitTimesOutWithoutEvidence(t *testing.T) {
	server, _ := newTestServer(t)
	session := connectClient(t, server)
	created := createTest(t, session, "lab.local")

	start := time.Now()
	out, env, isErr := callTool[getEvidenceOutput](t, session, "oast_get_evidence", map[string]any{
		"test_id":      created.TestID,
		"wait_seconds": 1,
	})
	elapsed := time.Since(start)
	if isErr || env.Error != nil {
		t.Fatalf("get evidence error = %+v", env.Error)
	}
	if len(out.Evidence) != 0 {
		t.Fatalf("expected no evidence, got %+v", out.Evidence)
	}
	if out.State != string(registry.StateActive) {
		t.Fatalf("state = %q", out.State)
	}
	// The call must wait roughly the requested bound and then return, not
	// return instantly and not exceed the bound by a wide margin.
	if elapsed < 750*time.Millisecond {
		t.Fatalf("returned too early after %s; long poll did not wait", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("long poll exceeded its bound: %s", elapsed)
	}
}

func TestServerFailsClosedWithoutAuthorizedScopes(t *testing.T) {
	// Scope configuration is the access boundary. Neither the scope
	// validator nor the service can be constructed without it, so the MCP
	// adapter can never be wired to an unscoped engine.
	if _, err := scope.New(nil); !errors.Is(err, scope.ErrNoScopes) {
		t.Fatalf("scope.New(nil) error = %v, want ErrNoScopes", err)
	}
	if _, err := scope.New([]string{}); !errors.Is(err, scope.ErrNoScopes) {
		t.Fatalf("scope.New([]) error = %v, want ErrNoScopes", err)
	}

	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          10,
		MaxTestLifetime:         time.Hour,
		MaxEvidencePerTest:      100,
		MaxEvidenceBytesPerTest: 1024 * 1024,
	}, clock.System{}, token.CryptoGenerator{})
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	if _, err := service.New(store, nil, service.Config{
		DefaultTTL: time.Minute,
		MaxTTL:     time.Hour,
	}); err == nil {
		t.Fatal("service.New() with nil scope validator must fail closed")
	}
}

func TestActiveTestLimitIsNotBypassedViaMCP(t *testing.T) {
	// newTestServer configures MaxActiveTests = 10. The MCP create tool must
	// surface the same registry limit rather than bypassing lifecycle checks.
	server, _ := newTestServer(t)
	session := connectClient(t, server)

	for i := 0; i < 10; i++ {
		createTest(t, session, "lab.local")
	}
	_, env, isErr := callTool[createOutput](t, session, "oast_create_test", map[string]any{
		"target":            "lab.local",
		"authorization_ref": "ENG-123",
	})
	if !isErr || env.Error == nil || env.Error.Code != "limit_exceeded" {
		t.Fatalf("expected limit_exceeded, got isError=%v env=%+v", isErr, env)
	}
}

func TestListDoesNotLeakRawBodyOrToken(t *testing.T) {
	server, store := newTestServer(t)
	session := connectClient(t, server)
	created := createTest(t, session, "lab.local")

	const marker = "SECRET-BODY-MARKER-9f3a"
	test, err := store.Get(context.Background(), created.TestID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if _, err := store.AppendEvidence(context.Background(), test.Token, evidence.Record{
		Protocol: evidence.ProtocolHTTP,
		SourceIP: "192.0.2.10",
		HTTP: &evidence.HTTPInteraction{
			Method: "POST",
			Host:   "oast.local",
			Path:   "/probe",
			Body:   []byte(marker),
		},
	}); err != nil {
		t.Fatalf("AppendEvidence() error = %v", err)
	}

	out, env, isErr := callTool[listOutput](t, session, "oast_list_tests", map[string]any{
		"include_closed": true,
	})
	if isErr || env.Error != nil {
		t.Fatalf("list error = %+v", env.Error)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	if strings.Contains(string(raw), marker) {
		t.Fatalf("list leaked raw request body: %s", raw)
	}
	if strings.Contains(string(raw), test.Token) {
		t.Fatalf("list leaked callback token: %s", raw)
	}
}
