// Package service applies authorization and lifecycle policy around the
// registry. The future MCP layer should call this package rather than the
// store directly.
package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/scope"
)

const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

var (
	ErrTargetRequired        = errors.New("target is required")
	ErrAuthorizationRequired = errors.New("authorization reference is required")
	ErrInvalidAuthorization  = errors.New("invalid authorization reference")
	ErrInvalidConfiguration  = errors.New("invalid service configuration")
	ErrInvalidListOptions    = errors.New("invalid list options")
)

// ListOptions filters and bounds test summaries.
type ListOptions struct {
	IncludeClosed bool
	Limit         int
}

// Config controls token lifetimes at the service boundary.
type Config struct {
	DefaultTTL time.Duration
	MaxTTL     time.Duration
}

// CreateRequest is the caller-facing creation input.
type CreateRequest struct {
	Target           string
	AuthorizationRef string
	TTL              time.Duration
}

// Service coordinates scope checks and the registry.
type Service struct {
	store  registry.Store
	scopes *scope.Validator
	cfg    Config
}

// New creates a service. The target allowlist must already be non-empty.
func New(store registry.Store, scopes *scope.Validator, cfg Config) (*Service, error) {
	switch {
	case store == nil:
		return nil, fmt.Errorf("%w: store is required", ErrInvalidConfiguration)
	case scopes == nil:
		return nil, fmt.Errorf("%w: scope validator is required", ErrInvalidConfiguration)
	case cfg.DefaultTTL <= 0:
		return nil, fmt.Errorf("%w: DefaultTTL must be > 0", ErrInvalidConfiguration)
	case cfg.MaxTTL <= 0:
		return nil, fmt.Errorf("%w: MaxTTL must be > 0", ErrInvalidConfiguration)
	case cfg.DefaultTTL > cfg.MaxTTL:
		return nil, fmt.Errorf("%w: DefaultTTL exceeds MaxTTL", ErrInvalidConfiguration)
	}
	return &Service{store: store, scopes: scopes, cfg: cfg}, nil
}

// CreateTest validates the target against the configured allowlist and
// registers a new active test.
//
// The authorization reference is stored as caller-provided context. It is
// not independently verified and must not be treated as proof of ownership.
func (s *Service) CreateTest(ctx context.Context, req CreateRequest) (*registry.Test, error) {
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return nil, ErrTargetRequired
	}
	authRef := strings.TrimSpace(req.AuthorizationRef)
	if authRef == "" {
		return nil, ErrAuthorizationRequired
	}
	if len(authRef) > 512 || strings.ContainsRune(authRef, '\x00') {
		return nil, ErrInvalidAuthorization
	}

	authorized, err := s.scopes.Authorize(target)
	if err != nil {
		return nil, err
	}

	ttl := req.TTL
	if ttl == 0 {
		ttl = s.cfg.DefaultTTL
	}
	if ttl < 0 {
		return nil, registry.ErrInvalidTTL
	}
	if ttl > s.cfg.MaxTTL {
		return nil, registry.ErrTTLTooLong
	}

	return s.store.Create(ctx, registry.CreateRequest{
		Target:           authorized.Raw,
		AuthorizationRef: authRef,
		TTL:              ttl,
	})
}

// GetTest returns a snapshot by test ID.
func (s *Service) GetTest(ctx context.Context, id string) (*registry.Test, error) {
	return s.store.Get(ctx, id)
}

// ListTests returns retained test summaries newest first. By default only
// active tests are returned. A positive limit keeps at most that many of the
// newest matching summaries.
func (s *Service) ListTests(ctx context.Context, opts ListOptions) ([]registry.Summary, error) {
	if opts.Limit < 0 || opts.Limit > MaxListLimit {
		return nil, fmt.Errorf("%w: limit must be between 0 and %d", ErrInvalidListOptions, MaxListLimit)
	}
	all, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]registry.Summary, 0, len(all))
	for _, summary := range all {
		if !opts.IncludeClosed && summary.State != registry.StateActive {
			continue
		}
		filtered = append(filtered, summary)
	}
	slices.SortFunc(filtered, func(a, b registry.Summary) int {
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}
	return filtered, nil
}

// CloseTest stops new evidence from being accepted.
func (s *Service) CloseTest(ctx context.Context, id string) error {
	return s.store.Close(ctx, id)
}

// Expire applies deadlines. A zero now uses the registry clock.
func (s *Service) Expire(ctx context.Context, now time.Time) (int, error) {
	return s.store.Expire(ctx, now)
}

// WaitForChange blocks until registry state changes or the timeout elapses.
func (s *Service) WaitForChange(ctx context.Context, timeout time.Duration) error {
	return s.store.WaitForChange(ctx, timeout)
}
