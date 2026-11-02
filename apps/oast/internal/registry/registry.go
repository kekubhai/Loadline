// Package registry owns OAST test lifecycle and evidence storage.
//
// The Store interface is deliberately small so an in-memory implementation
// can be replaced by a persistent one without changing the callback
// receivers or the future MCP layer.
package registry

import (
	"context"
	"errors"
	"time"

	"github.com/kekubhai/Loadline/apps/oast/internal/evidence"
)

// State is the explicit lifecycle state of a test.
type State string

const (
	StateActive  State = "active"
	StateClosed  State = "closed"
	StateExpired State = "expired"
)

var (
	ErrNotFound             = errors.New("test not found")
	ErrClosed               = errors.New("test is closed")
	ErrExpired              = errors.New("test is expired")
	ErrInvalidRequest       = errors.New("invalid create request")
	ErrInvalidTTL           = errors.New("invalid test lifetime")
	ErrTTLTooLong           = errors.New("test lifetime exceeds the configured maximum")
	ErrActiveLimit          = errors.New("active test limit reached")
	ErrEvidenceLimit        = errors.New("evidence limit reached")
	ErrInvalidEvidence      = errors.New("invalid evidence record")
	ErrTokenCollision       = errors.New("callback token collision")
	ErrTestIDCollision      = errors.New("test ID collision")
	ErrTokenGeneration      = errors.New("token generation failed")
	ErrInvalidConfiguration = errors.New("invalid registry configuration")
)

// CreateRequest is the validated input to Create. Scope authorization is
// performed by the service layer before this request reaches the store.
type CreateRequest struct {
	Target           string
	AuthorizationRef string
	TTL              time.Duration
}

// Test is a full snapshot of a registered test.
type Test struct {
	ID               string
	Token            string
	Target           string
	AuthorizationRef string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	State            State
	Evidence         []evidence.Record
}

// Summary is a non-sensitive view used by list operations.
type Summary struct {
	ID            string
	Target        string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	State         State
	EvidenceCount int
}

// Store is the lifecycle and evidence contract.
type Store interface {
	Create(ctx context.Context, req CreateRequest) (*Test, error)
	Get(ctx context.Context, id string) (*Test, error)
	GetByToken(ctx context.Context, token string) (*Test, error)
	List(ctx context.Context) ([]Summary, error)
	Close(ctx context.Context, id string) error
	Expire(ctx context.Context, now time.Time) (int, error)
	AppendEvidence(ctx context.Context, token string, rec evidence.Record) (evidence.Record, error)
	WaitForChange(ctx context.Context, timeout time.Duration) error
}

// EvidenceSink is the narrow interface callback receivers need.
type EvidenceSink interface {
	AppendEvidence(ctx context.Context, token string, rec evidence.Record) (evidence.Record, error)
}

var _ Store = (*Memory)(nil)
var _ EvidenceSink = (*Memory)(nil)
