package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
)

func newTestService(t *testing.T) (*Service, *registry.Memory) {
	t.Helper()
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          10,
		MaxTestLifetime:         2 * time.Hour,
		MaxEvidencePerTest:      100,
		MaxEvidenceBytesPerTest: 1024 * 1024,
	}, clock.NewManual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), token.CryptoGenerator{})
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	scopes, err := scope.New([]string{"lab.local", "*.apps.lab.test", "10.0.0.0/8", "192.0.2.10"})
	if err != nil {
		t.Fatalf("scope.New() error = %v", err)
	}
	svc, err := New(store, scopes, Config{DefaultTTL: 10 * time.Minute, MaxTTL: time.Hour})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return svc, store
}

func TestCreateTestAuthorizationAndDefaults(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTest(context.Background(), CreateRequest{
		Target:           "https://lab.local/checkout",
		AuthorizationRef: "engagement-abc",
	})
	if err != nil {
		t.Fatalf("CreateTest() error = %v", err)
	}
	if created.Target != "https://lab.local/checkout" {
		t.Fatalf("Target = %q", created.Target)
	}
	if created.AuthorizationRef != "engagement-abc" {
		t.Fatalf("AuthorizationRef = %q", created.AuthorizationRef)
	}
	if got := created.ExpiresAt.Sub(created.CreatedAt); got != 10*time.Minute {
		t.Fatalf("default TTL = %s, want 10m", got)
	}
	if created.State != registry.StateActive {
		t.Fatalf("State = %s", created.State)
	}
}

func TestCreateTestRequiresTargetAndAuthorization(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.CreateTest(context.Background(), CreateRequest{AuthorizationRef: "x"}); !errors.Is(err, ErrTargetRequired) {
		t.Fatalf("missing target error = %v", err)
	}
	if _, err := svc.CreateTest(context.Background(), CreateRequest{Target: "lab.local"}); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("missing authorization error = %v", err)
	}
	if _, err := svc.CreateTest(context.Background(), CreateRequest{
		Target: "lab.local", AuthorizationRef: "bad\x00ref",
	}); !errors.Is(err, ErrInvalidAuthorization) {
		t.Fatalf("invalid authorization error = %v", err)
	}
}

func TestCreateTestScopeValidation(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.CreateTest(context.Background(), CreateRequest{
		Target: "evil.example.net", AuthorizationRef: "x",
	}); !errors.Is(err, scope.ErrOutOfScope) {
		t.Fatalf("out-of-scope error = %v", err)
	}
	if _, err := svc.CreateTest(context.Background(), CreateRequest{
		Target: "not a valid target", AuthorizationRef: "x",
	}); !errors.Is(err, scope.ErrInvalidTarget) {
		t.Fatalf("invalid target error = %v", err)
	}
	if _, err := svc.CreateTest(context.Background(), CreateRequest{
		Target: "10.9.8.7", AuthorizationRef: "x", TTL: time.Minute,
	}); err != nil {
		t.Fatalf("CIDR-authorized target error = %v", err)
	}
}

func TestCreateTestTTLValidation(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.CreateTest(context.Background(), CreateRequest{
		Target: "lab.local", AuthorizationRef: "x", TTL: 2 * time.Hour,
	}); !errors.Is(err, registry.ErrTTLTooLong) {
		t.Fatalf("long TTL error = %v", err)
	}
	if _, err := svc.CreateTest(context.Background(), CreateRequest{
		Target: "lab.local", AuthorizationRef: "x", TTL: -time.Second,
	}); !errors.Is(err, registry.ErrInvalidTTL) {
		t.Fatalf("negative TTL error = %v", err)
	}
}

func TestLifecycleDelegation(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.CreateTest(context.Background(), CreateRequest{
		Target: "lab.local", AuthorizationRef: "x",
	})
	if err != nil {
		t.Fatalf("CreateTest() error = %v", err)
	}
	got, err := svc.GetTest(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetTest() error = %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("GetTest() ID = %q", got.ID)
	}
	list, err := svc.ListTests(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListTests() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListTests() len = %d, want 1", len(list))
	}
	if err := svc.CloseTest(context.Background(), created.ID); err != nil {
		t.Fatalf("CloseTest() error = %v", err)
	}
	list, err = svc.ListTests(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("active ListTests() error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("active ListTests() len = %d, want 0", len(list))
	}
	list, err = svc.ListTests(context.Background(), ListOptions{IncludeClosed: true})
	if err != nil {
		t.Fatalf("all ListTests() error = %v", err)
	}
	if len(list) != 1 || list[0].State != registry.StateClosed {
		t.Fatalf("all ListTests() = %+v", list)
	}
	if _, err := svc.ListTests(context.Background(), ListOptions{Limit: MaxListLimit + 1}); !errors.Is(err, ErrInvalidListOptions) {
		t.Fatalf("invalid limit error = %v", err)
	}
	got, err = svc.GetTest(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetTest() after close error = %v", err)
	}
	if got.State != registry.StateClosed {
		t.Fatalf("State = %s, want closed", got.State)
	}
}

func TestNewValidation(t *testing.T) {
	store, err := registry.NewMemory(registry.MemoryConfig{
		MaxActiveTests:          1,
		MaxTestLifetime:         time.Hour,
		MaxEvidencePerTest:      1,
		MaxEvidenceBytesPerTest: 1024,
	}, clock.NewManual(time.Now()), token.CryptoGenerator{})
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	scopes, _ := scope.New([]string{"lab.local"})
	if _, err := New(nil, scopes, Config{DefaultTTL: time.Minute, MaxTTL: time.Hour}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("nil store error = %v", err)
	}
	if _, err := New(store, nil, Config{DefaultTTL: time.Minute, MaxTTL: time.Hour}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("nil scopes error = %v", err)
	}
	if _, err := New(store, scopes, Config{}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("bad config error = %v", err)
	}
	if _, err := New(store, scopes, Config{DefaultTTL: 2 * time.Hour, MaxTTL: time.Hour}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("default > max error = %v", err)
	}
}
