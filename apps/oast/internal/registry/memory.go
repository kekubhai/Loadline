package registry

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
	"github.com/kekubhai/Loadline/apps/oast/internal/evidence"
	"github.com/kekubhai/Loadline/apps/oast/internal/token"
)

// MemoryConfig bounds the in-memory registry.
type MemoryConfig struct {
	MaxActiveTests          int
	MaxTestLifetime         time.Duration
	MaxEvidencePerTest      int
	MaxEvidenceBytesPerTest int64
}

type testRecord struct {
	id               string
	token            string
	target           string
	authorizationRef string
	createdAt        time.Time
	expiresAt        time.Time
	state            State
	evidence         []evidence.Record
	evidenceBytes    int64
	nextSequence     uint64
}

// Memory is a concurrency-safe in-memory Store.
type Memory struct {
	mu      sync.Mutex
	cfg     MemoryConfig
	clock   clock.Clock
	tokens  token.Generator
	byID    map[string]*testRecord
	byToken map[string]*testRecord
	change  chan struct{}
}

// NewMemory creates an empty registry.
func NewMemory(cfg MemoryConfig, clk clock.Clock, tokens token.Generator) (*Memory, error) {
	switch {
	case cfg.MaxActiveTests <= 0:
		return nil, fmt.Errorf("%w: MaxActiveTests must be > 0", ErrInvalidConfiguration)
	case cfg.MaxTestLifetime <= 0:
		return nil, fmt.Errorf("%w: MaxTestLifetime must be > 0", ErrInvalidConfiguration)
	case cfg.MaxEvidencePerTest <= 0:
		return nil, fmt.Errorf("%w: MaxEvidencePerTest must be > 0", ErrInvalidConfiguration)
	case cfg.MaxEvidenceBytesPerTest <= 0:
		return nil, fmt.Errorf("%w: MaxEvidenceBytesPerTest must be > 0", ErrInvalidConfiguration)
	case clk == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfiguration)
	case tokens == nil:
		return nil, fmt.Errorf("%w: token generator is required", ErrInvalidConfiguration)
	}
	return &Memory{
		cfg:     cfg,
		clock:   clk,
		tokens:  tokens,
		byID:    make(map[string]*testRecord, cfg.MaxActiveTests),
		byToken: make(map[string]*testRecord, cfg.MaxActiveTests),
		change:  make(chan struct{}),
	}, nil
}

// Create registers a new active test.
func (m *Memory) Create(ctx context.Context, req CreateRequest) (*Test, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	target := strings.TrimSpace(req.Target)
	authRef := strings.TrimSpace(req.AuthorizationRef)
	if target == "" || authRef == "" {
		return nil, fmt.Errorf("%w: target and authorization reference are required", ErrInvalidRequest)
	}
	if req.TTL <= 0 {
		return nil, ErrInvalidTTL
	}
	if req.TTL > m.cfg.MaxTestLifetime {
		return nil, ErrTTLTooLong
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock.Now().UTC()
	m.expireLocked(now)
	if m.activeCountLocked() >= m.cfg.MaxActiveTests {
		return nil, ErrActiveLimit
	}

	var (
		lastErr error
		rec     *testRecord
	)
	for attempt := 0; attempt < 8; attempt++ {
		idPart, err := m.tokens.New()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTokenGeneration, err)
		}
		callbackToken, err := m.tokens.New()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTokenGeneration, err)
		}
		id := "oast_" + idPart
		if !token.IsValid(callbackToken) {
			lastErr = ErrTokenGeneration
			continue
		}
		if _, exists := m.byToken[callbackToken]; exists {
			lastErr = ErrTokenCollision
			continue
		}
		if _, exists := m.byID[id]; exists {
			lastErr = ErrTestIDCollision
			continue
		}
		rec = &testRecord{
			id:               id,
			token:            callbackToken,
			target:           target,
			authorizationRef: authRef,
			createdAt:        now,
			expiresAt:        now.Add(req.TTL),
			state:            StateActive,
		}
		break
	}
	if rec == nil {
		if lastErr == nil {
			lastErr = ErrTokenCollision
		}
		return nil, lastErr
	}
	m.byID[rec.id] = rec
	m.byToken[rec.token] = rec
	m.notifyLocked()
	return m.snapshot(rec), nil
}

// Get returns a test by ID.
func (m *Memory) Get(ctx context.Context, id string) (*Test, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	if m.expireRecordLocked(rec, m.clock.Now().UTC()) {
		m.notifyLocked()
	}
	return m.snapshot(rec), nil
}

// GetByToken returns a test by its callback token.
func (m *Memory) GetByToken(ctx context.Context, callbackToken string) (*Test, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.byToken[callbackToken]
	if !ok {
		return nil, ErrNotFound
	}
	if m.expireRecordLocked(rec, m.clock.Now().UTC()) {
		m.notifyLocked()
	}
	return m.snapshot(rec), nil
}

// List returns deterministic test summaries ordered by creation time and ID.
func (m *Memory) List(ctx context.Context) ([]Summary, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.expireLocked(m.clock.Now().UTC()) > 0 {
		m.notifyLocked()
	}
	out := make([]Summary, 0, len(m.byID))
	for _, rec := range m.byID {
		out = append(out, Summary{
			ID:            rec.id,
			Target:        rec.target,
			CreatedAt:     rec.createdAt,
			ExpiresAt:     rec.expiresAt,
			State:         rec.state,
			EvidenceCount: len(rec.evidence),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// Close stops new evidence from being accepted. It is idempotent for an
// already-closed test.
func (m *Memory) Close(ctx context.Context, id string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.byID[id]
	if !ok {
		return ErrNotFound
	}
	if m.expireRecordLocked(rec, m.clock.Now().UTC()) {
		m.notifyLocked()
	}
	switch rec.state {
	case StateExpired:
		return ErrExpired
	case StateClosed:
		return nil
	default:
		rec.state = StateClosed
		m.notifyLocked()
		return nil
	}
}

// Expire marks all active tests whose deadline has passed. A zero now uses
// the registry clock.
func (m *Memory) Expire(ctx context.Context, now time.Time) (int, error) {
	if err := contextErr(ctx); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = m.clock.Now()
	}
	count := m.expireLocked(now.UTC())
	if count > 0 {
		m.notifyLocked()
	}
	return count, nil
}

// AppendEvidence records one interaction against an active test.
func (m *Memory) AppendEvidence(ctx context.Context, callbackToken string, rec evidence.Record) (evidence.Record, error) {
	if err := contextErr(ctx); err != nil {
		return evidence.Record{}, err
	}
	switch rec.Protocol {
	case evidence.ProtocolHTTP:
		if rec.HTTP == nil {
			return evidence.Record{}, ErrInvalidEvidence
		}
	case evidence.ProtocolDNS:
		if rec.DNS == nil {
			return evidence.Record{}, ErrInvalidEvidence
		}
	default:
		return evidence.Record{}, ErrInvalidEvidence
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	tr, ok := m.byToken[callbackToken]
	if !ok {
		return evidence.Record{}, ErrNotFound
	}
	now := m.clock.Now().UTC()
	m.expireRecordLocked(tr, now)
	switch tr.state {
	case StateClosed:
		return evidence.Record{}, ErrClosed
	case StateExpired:
		return evidence.Record{}, ErrExpired
	}

	if len(tr.evidence) >= m.cfg.MaxEvidencePerTest {
		return evidence.Record{}, ErrEvidenceLimit
	}

	rec.TestID = tr.id
	rec.Sequence = tr.nextSequence
	rec.EvidenceID = tr.id + "-" + strconv.FormatUint(tr.nextSequence, 10)
	rec.ObservedAt = now
	rec.Assessment = evidence.AssessmentObservedOnly
	size := evidence.Size(rec)
	if tr.evidenceBytes+size > m.cfg.MaxEvidenceBytesPerTest {
		return evidence.Record{}, ErrEvidenceLimit
	}

	stored := rec.Clone()
	tr.evidence = append(tr.evidence, stored)
	tr.evidenceBytes += size
	tr.nextSequence++
	m.notifyLocked()
	return stored.Clone(), nil
}

// WaitForChange blocks until the registry changes, the timeout elapses, or
// the context is canceled. A non-positive timeout returns immediately.
func (m *Memory) WaitForChange(ctx context.Context, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if timeout <= 0 {
		return nil
	}

	m.mu.Lock()
	change := m.change
	m.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-change:
		return nil
	case <-timer.C:
		return nil
	}
}

func (m *Memory) notifyLocked() {
	close(m.change)
	m.change = make(chan struct{})
}

func (m *Memory) activeCountLocked() int {
	n := 0
	for _, rec := range m.byID {
		if rec.state == StateActive {
			n++
		}
	}
	return n
}

func (m *Memory) expireLocked(now time.Time) int {
	count := 0
	for _, rec := range m.byID {
		if m.expireRecordLocked(rec, now) {
			count++
		}
	}
	return count
}

func (m *Memory) expireRecordLocked(rec *testRecord, now time.Time) bool {
	if rec.state == StateActive && !now.Before(rec.expiresAt) {
		rec.state = StateExpired
		return true
	}
	return false
}

func (m *Memory) snapshot(rec *testRecord) *Test {
	out := &Test{
		ID:               rec.id,
		Token:            rec.token,
		Target:           rec.target,
		AuthorizationRef: rec.authorizationRef,
		CreatedAt:        rec.createdAt,
		ExpiresAt:        rec.expiresAt,
		State:            rec.state,
		Evidence:         make([]evidence.Record, len(rec.evidence)),
	}
	for i, ev := range rec.evidence {
		out.Evidence[i] = ev.Clone()
	}
	return out
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
