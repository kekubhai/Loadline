package registry

import (
	"context"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
	"github.com/kekubhai/Loadline/apps/oast/internal/evidence"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
)

var testNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func testConfig() MemoryConfig {
	return MemoryConfig{
		MaxActiveTests:          10,
		MaxTestLifetime:         time.Hour,
		MaxEvidencePerTest:      100,
		MaxEvidenceBytesPerTest: 1024 * 1024,
	}
}

func newTestStore(t *testing.T, cfg MemoryConfig) (*Memory, *clock.Manual) {
	t.Helper()
	clk := clock.NewManual(testNow)
	store, err := NewMemory(cfg, clk, token.CryptoGenerator{})
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	return store, clk
}

func createTest(t *testing.T, store *Memory, ttl time.Duration) *Test {
	t.Helper()
	got, err := store.Create(context.Background(), CreateRequest{
		Target:           "lab.local",
		AuthorizationRef: "engagement-123",
		TTL:              ttl,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return got
}

func dnsRecord() evidence.Record {
	return evidence.Record{
		Protocol: evidence.ProtocolDNS,
		SourceIP: "192.0.2.9",
		DNS: &evidence.DNSInteraction{
			Name:      "token.oast.local.",
			Type:      1,
			TypeName:  "A",
			Class:     1,
			ClassName: "IN",
		},
	}
}

func TestNewMemoryValidation(t *testing.T) {
	clk := clock.NewManual(testNow)
	gen := token.CryptoGenerator{}
	if _, err := NewMemory(MemoryConfig{}, clk, gen); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("empty config error = %v", err)
	}
	if _, err := NewMemory(testConfig(), nil, gen); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("nil clock error = %v", err)
	}
	if _, err := NewMemory(testConfig(), clk, nil); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("nil generator error = %v", err)
	}
}

func TestCreateAndTokenShape(t *testing.T) {
	store, _ := newTestStore(t, testConfig())
	first := createTest(t, store, time.Minute)
	second := createTest(t, store, time.Minute)

	if !token.IsValid(first.Token) || !token.IsValid(second.Token) {
		t.Fatalf("invalid tokens: %q / %q", first.Token, second.Token)
	}
	if first.Token == second.Token {
		t.Fatal("tokens must be distinct")
	}
	if first.ID == second.ID {
		t.Fatal("test IDs must be distinct")
	}
	if first.State != StateActive {
		t.Fatalf("state = %s, want active", first.State)
	}
	if first.Target != "lab.local" || first.AuthorizationRef != "engagement-123" {
		t.Fatalf("stored request = %#v", first)
	}
	if !first.ExpiresAt.Equal(testNow.Add(time.Minute)) {
		t.Fatalf("ExpiresAt = %s", first.ExpiresAt)
	}

	byToken, err := store.GetByToken(context.Background(), first.Token)
	if err != nil {
		t.Fatalf("GetByToken() error = %v", err)
	}
	if byToken.ID != first.ID {
		t.Fatalf("GetByToken() ID = %q, want %q", byToken.ID, first.ID)
	}
	if _, err := store.GetByToken(context.Background(), "not-a-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByToken(unknown) error = %v", err)
	}
}

func TestCreateValidationAndLimits(t *testing.T) {
	cfg := testConfig()
	cfg.MaxActiveTests = 1
	store, _ := newTestStore(t, cfg)

	if _, err := store.Create(context.Background(), CreateRequest{AuthorizationRef: "x", TTL: time.Minute}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing target error = %v", err)
	}
	if _, err := store.Create(context.Background(), CreateRequest{Target: "lab.local", TTL: time.Minute}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing authorization error = %v", err)
	}
	if _, err := store.Create(context.Background(), CreateRequest{Target: "lab.local", AuthorizationRef: "x"}); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("zero ttl error = %v", err)
	}
	if _, err := store.Create(context.Background(), CreateRequest{Target: "lab.local", AuthorizationRef: "x", TTL: 2 * time.Hour}); !errors.Is(err, ErrTTLTooLong) {
		t.Fatalf("long ttl error = %v", err)
	}

	first := createTest(t, store, time.Minute)
	if _, err := store.Create(context.Background(), CreateRequest{Target: "lab.local", AuthorizationRef: "x", TTL: time.Minute}); !errors.Is(err, ErrActiveLimit) {
		t.Fatalf("second create error = %v, want ErrActiveLimit", err)
	}
	if err := store.Close(context.Background(), first.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.Create(context.Background(), CreateRequest{Target: "lab.local", AuthorizationRef: "x", TTL: time.Minute}); err != nil {
		t.Fatalf("create after close error = %v", err)
	}
}

func TestExpirationAndClose(t *testing.T) {
	store, clk := newTestStore(t, testConfig())
	expiring := createTest(t, store, time.Minute)

	clk.Advance(59 * time.Second)
	got, err := store.Get(context.Background(), expiring.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.State != StateActive {
		t.Fatalf("state before expiry = %s", got.State)
	}
	if _, err := store.AppendEvidence(context.Background(), expiring.Token, dnsRecord()); err != nil {
		t.Fatalf("AppendEvidence() before expiry error = %v", err)
	}

	clk.Advance(2 * time.Second)
	if _, err := store.AppendEvidence(context.Background(), expiring.Token, dnsRecord()); !errors.Is(err, ErrExpired) {
		t.Fatalf("AppendEvidence() after expiry error = %v, want ErrExpired", err)
	}
	got, err = store.Get(context.Background(), expiring.ID)
	if err != nil {
		t.Fatalf("Get() after expiry error = %v", err)
	}
	if got.State != StateExpired {
		t.Fatalf("state after expiry = %s", got.State)
	}
	if err := store.Close(context.Background(), expiring.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("Close(expired) error = %v", err)
	}

	closed := createTest(t, store, time.Minute)
	if err := store.Close(context.Background(), closed.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.AppendEvidence(context.Background(), closed.Token, dnsRecord()); !errors.Is(err, ErrClosed) {
		t.Fatalf("AppendEvidence(closed) error = %v, want ErrClosed", err)
	}
	if err := store.Close(context.Background(), closed.ID); err != nil {
		t.Fatalf("Close() idempotent error = %v", err)
	}
}

func TestEvidenceSequenceAssessmentAndLimits(t *testing.T) {
	cfg := testConfig()
	cfg.MaxEvidencePerTest = 2
	store, _ := newTestStore(t, cfg)
	created := createTest(t, store, time.Minute)

	rec := dnsRecord()
	rec.Assessment = "ssrf_confirmed"
	first, err := store.AppendEvidence(context.Background(), created.Token, rec)
	if err != nil {
		t.Fatalf("AppendEvidence() error = %v", err)
	}
	if first.Sequence != 0 || first.EvidenceID != created.ID+"-0" {
		t.Fatalf("first evidence = %#v", first)
	}
	if first.Assessment != evidence.AssessmentObservedOnly {
		t.Fatalf("assessment = %q, want observed_interaction_only", first.Assessment)
	}
	if first.TestID != created.ID || first.ObservedAt.IsZero() {
		t.Fatalf("registry did not stamp evidence: %#v", first)
	}

	second, err := store.AppendEvidence(context.Background(), created.Token, dnsRecord())
	if err != nil {
		t.Fatalf("second AppendEvidence() error = %v", err)
	}
	if second.Sequence != 1 {
		t.Fatalf("second sequence = %d, want 1", second.Sequence)
	}
	if _, err := store.AppendEvidence(context.Background(), created.Token, dnsRecord()); !errors.Is(err, ErrEvidenceLimit) {
		t.Fatalf("third AppendEvidence() error = %v, want ErrEvidenceLimit", err)
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(got.Evidence))
	}
}

func TestAppendRejectsMismatchedEvidence(t *testing.T) {
	store, _ := newTestStore(t, testConfig())
	created := createTest(t, store, time.Minute)

	tests := []evidence.Record{
		{Protocol: evidence.ProtocolHTTP},
		{Protocol: evidence.ProtocolDNS},
		{Protocol: "smtp", HTTP: &evidence.HTTPInteraction{}},
	}
	for _, rec := range tests {
		if _, err := store.AppendEvidence(context.Background(), created.Token, rec); !errors.Is(err, ErrInvalidEvidence) {
			t.Fatalf("AppendEvidence(%#v) error = %v, want ErrInvalidEvidence", rec, err)
		}
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 0 {
		t.Fatalf("invalid evidence count = %d", len(got.Evidence))
	}
}

func TestEvidenceByteLimit(t *testing.T) {
	cfg := testConfig()
	cfg.MaxEvidenceBytesPerTest = 300
	store, _ := newTestStore(t, cfg)
	created := createTest(t, store, time.Minute)

	rec := evidence.Record{
		Protocol: evidence.ProtocolHTTP,
		HTTP: &evidence.HTTPInteraction{
			Method: "POST",
			Body:   make([]byte, 1024),
		},
	}
	if _, err := store.AppendEvidence(context.Background(), created.Token, rec); !errors.Is(err, ErrEvidenceLimit) {
		t.Fatalf("AppendEvidence() error = %v, want ErrEvidenceLimit", err)
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 0 {
		t.Fatalf("evidence count = %d, want 0", len(got.Evidence))
	}
}

func TestSnapshotIsolation(t *testing.T) {
	store, _ := newTestStore(t, testConfig())
	created := createTest(t, store, time.Minute)
	rec := evidence.Record{
		Protocol: evidence.ProtocolHTTP,
		HTTP: &evidence.HTTPInteraction{
			Method:  "GET",
			Headers: map[string][]string{"X-Test": {"original"}},
			Body:    []byte("body"),
		},
	}
	if _, err := store.AppendEvidence(context.Background(), created.Token, rec); err != nil {
		t.Fatalf("AppendEvidence() error = %v", err)
	}

	first, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	first.Evidence[0].HTTP.Headers["X-Test"][0] = "mutated"
	first.Evidence[0].HTTP.Body[0] = 'X'

	second, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if second.Evidence[0].HTTP.Headers["X-Test"][0] != "original" {
		t.Fatal("mutating a snapshot changed registry headers")
	}
	if string(second.Evidence[0].HTTP.Body) != "body" {
		t.Fatal("mutating a snapshot changed registry body")
	}
}

func TestConcurrentAppendAndClose(t *testing.T) {
	store, _ := newTestStore(t, testConfig())
	created := createTest(t, store, time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.AppendEvidence(context.Background(), created.Token, dnsRecord())
		}()
	}
	wg.Wait()

	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Evidence) != 50 {
		t.Fatalf("evidence count = %d, want 50", len(got.Evidence))
	}
	for i, rec := range got.Evidence {
		if rec.Sequence != uint64(i) {
			t.Fatalf("evidence[%d].Sequence = %d", i, rec.Sequence)
		}
	}

	if err := store.Close(context.Background(), created.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.AppendEvidence(context.Background(), created.Token, dnsRecord()); !errors.Is(err, ErrClosed) {
		t.Fatalf("AppendEvidence() after close error = %v", err)
	}
}

func TestConcurrentCreateRespectsActiveLimit(t *testing.T) {
	cfg := testConfig()
	cfg.MaxActiveTests = 5
	store, _ := newTestStore(t, cfg)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var success, limited int
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Create(context.Background(), CreateRequest{
				Target:           "lab.local",
				AuthorizationRef: "engagement",
				TTL:              time.Minute,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case errors.Is(err, ErrActiveLimit):
				limited++
			default:
				t.Errorf("unexpected Create() error = %v", err)
			}
		}()
	}
	wg.Wait()
	if success != 5 || limited != 20 {
		t.Fatalf("success/limited = %d/%d, want 5/20", success, limited)
	}
}

type duplicateTokenGen struct {
	mu  sync.Mutex
	n   uint64
	dup string
}

func (g *duplicateTokenGen) New() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	if g.n%2 == 0 {
		return g.dup, nil
	}
	return validTestToken(g.n), nil
}

func validTestToken(n uint64) string {
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[8:], n)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf[:]))
}

func TestDuplicateTokenIsRejected(t *testing.T) {
	clk := clock.NewManual(testNow)
	gen := &duplicateTokenGen{dup: validTestToken(999)}
	store, err := NewMemory(testConfig(), clk, gen)
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	first, err := store.Create(context.Background(), CreateRequest{
		Target: "lab.local", AuthorizationRef: "x", TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	if first.Token != gen.dup {
		t.Fatalf("first token = %q, want duplicate fixture %q", first.Token, gen.dup)
	}
	if _, err := store.Create(context.Background(), CreateRequest{
		Target: "lab.local", AuthorizationRef: "x", TTL: time.Minute,
	}); !errors.Is(err, ErrTokenCollision) {
		t.Fatalf("duplicate Create() error = %v, want ErrTokenCollision", err)
	}
}

func TestListAndExpire(t *testing.T) {
	store, clk := newTestStore(t, testConfig())
	first := createTest(t, store, time.Minute)
	second := createTest(t, store, 2*time.Minute)

	list, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i].CreatedAt.Before(list[i-1].CreatedAt) {
			t.Fatal("List() is not ordered by creation time")
		}
	}

	clk.Advance(90 * time.Second)
	n, err := store.Expire(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("Expire() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("Expire() = %d, want 1", n)
	}
	got, err := store.Get(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("Get(first) error = %v", err)
	}
	if got.State != StateExpired {
		t.Fatalf("first state = %s", got.State)
	}
	got, err = store.Get(context.Background(), second.ID)
	if err != nil {
		t.Fatalf("Get(second) error = %v", err)
	}
	if got.State != StateActive {
		t.Fatalf("second state = %s", got.State)
	}
}
