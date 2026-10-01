// Package arena implements LOADLINE's benchmark layer: version-controlled
// system-design challenges, server-authoritative benchmark execution, and a
// deterministic, explainable score.
//
// Design rules (see AGENTS.md and the Arena specification):
//
//   - A challenge fully defines the benchmark: workload, failure scenarios,
//     requirements, targets, and scoring weights. The client supplies ONLY a
//     display name and an architecture version id.
//   - The benchmark reuses the existing simulation engine unchanged. Nothing
//     here computes a metric; metrics come from sim.Simulate.
//   - Scoring is a pure function of measured results. No LLM, no subjective
//     judgement, no invented numbers.
//   - Identical (challenge, architecture, seed) reproduces identical results,
//     because the seed is fixed by the challenge and recorded per submission.
//
// Canonical challenge definitions are the JSON files under definitions/,
// embedded into the binary. They are the single source of truth; the
// challenges table is a queryable projection of them (see the repository),
// never a second hand-edited copy.
package arena

import (
	"fmt"
	"strings"

	"github.com/kekubhai/Loadline/apps/simulator/sim"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// Difficulty is the challenge's intended skill level.
type Difficulty string

const (
	DifficultyBeginner     Difficulty = "beginner"
	DifficultyIntermediate Difficulty = "intermediate"
	DifficultyAdvanced     Difficulty = "advanced"
	DifficultyExpert       Difficulty = "expert"
)

// Category groups challenges by problem domain.
type Category string

const (
	CategoryAPI         Category = "api"
	CategorySocial      Category = "social"
	CategoryEcommerce   Category = "ecommerce"
	CategorySaaS        Category = "saas"
	CategoryRealtime    Category = "realtime"
	CategoryDistributed Category = "distributed"
	CategoryCloud       Category = "cloud"
)

// Status controls whether a challenge accepts submissions.
type Status string

const (
	StatusDraft  Status = "draft"
	StatusActive Status = "active"
	StatusClosed Status = "closed"
)

// Definition is one complete, versioned benchmark definition. It maps
// one-to-one onto the JSON files under definitions/.
type Definition struct {
	// Slug is the stable, human-readable identity ("social-feed").
	Slug string `json:"slug"`
	// Version identifies the benchmark definition. A changed definition is a
	// NEW version; historical submissions keep the version they ran, so an
	// edited challenge never rewrites an old leaderboard.
	Version int `json:"version"`

	Name        string     `json:"name"`
	Description string     `json:"description"`
	Difficulty  Difficulty `json:"difficulty"`
	Category    Category   `json:"category"`
	Status      Status     `json:"status"`

	// Workload is the standardized load every submission receives.
	Workload Workload `json:"workload"`
	// Constraints are the targets the score is measured against.
	Constraints Constraints `json:"constraints"`
	// Requirements are hard structural rules a submission must satisfy.
	Requirements Requirements `json:"requirements"`
	// Failures are the standardized failure scenarios, applied by the
	// server. Targets are generic KINDS ("cache"), resolved to the
	// submitted architecture's component of that kind.
	Failures []FailureScenario `json:"failures"`
	// Scoring holds the weights used by the scoring package.
	Scoring Weights `json:"scoring"`
	// Seed is the fixed benchmark seed: every submission to this challenge
	// runs with it, so results are reproducible and comparable.
	Seed uint64 `json:"seed"`

	// Run holds optional simulation-engine configuration (retry/timeout and
	// the sampling window). Zero values fall back to documented defaults.
	Run RunConfig `json:"run"`
}

// Workload is the challenge's standardized load definition. It is the same
// WorkloadSpec the engine consumes, plus the simulated horizon.
type Workload struct {
	TotalUsers            int64   `json:"totalUsers"`
	DAU                   int64   `json:"dau"`
	RequestsPerUserPerDay float64 `json:"requestsPerUserPerDay"`
	PeakMultiplier        float64 `json:"peakMultiplier"`
	ReadWriteRatio        float64 `json:"readWriteRatio"`
	PayloadBytes          int64   `json:"payloadBytes"`
	// DurationMS is the simulated horizon. 0 → DefaultDurationMS.
	DurationMS float64 `json:"durationMs"`
}

// Spec converts the challenge workload into the engine's workload.Spec.
func (w Workload) Spec() workload.Spec {
	return workload.Spec{
		TotalUsers:            w.TotalUsers,
		DAU:                   w.DAU,
		RequestsPerUserPerDay: w.RequestsPerUserPerDay,
		PeakMultiplier:        w.PeakMultiplier,
		ReadWriteRatio:        w.ReadWriteRatio,
		PayloadBytes:          w.PayloadBytes,
	}
}

// Constraints are performance/cost targets. They do not accept or reject a
// submission; they are the reference points the score is measured against.
// A zero target means "not assessed" for that dimension.
type Constraints struct {
	TargetThroughputRPS float64 `json:"targetThroughputRps"`
	TargetP95MS         float64 `json:"targetP95Ms"`
	TargetP99MS         float64 `json:"targetP99Ms"`
	TargetErrorRate     float64 `json:"targetErrorRate"`
	MonthlyBudgetUSD    float64 `json:"monthlyBudgetUsd"`
}

// Requirements are hard structural rules. Only the existing architecture
// model is used — generic component kinds and their counts.
type Requirements struct {
	RequiredKinds []KindRequirement `json:"requiredKinds"`
}

// KindRequirement requires at least Min components of a generic kind.
type KindRequirement struct {
	Kind string `json:"kind"`
	Min  int    `json:"min"`
}

// FailureScenario is one standardized injection. TargetKind names a generic
// component kind; the server resolves it to the submitted architecture's
// first component of that kind.
type FailureScenario struct {
	TargetKind string `json:"targetKind"`
	// Type is one of the engine's failure types: crash, increased_latency,
	// increased_error_rate, network_failure.
	Type           string  `json:"type"`
	StartMS        float64 `json:"startMs"`
	DurationMS     float64 `json:"durationMs"`
	AddedLatencyMS float64 `json:"addedLatencyMs"`
	ErrorRate      float64 `json:"errorRate"`
	PacketLossRate float64 `json:"packetLossRate"`
	PassThrough    bool    `json:"passThrough"`
}

// Weights are the scoring weights. They need not sum to 1: they are
// normalized by the scoring package.
type Weights struct {
	Throughput      float64 `json:"throughput"`
	Latency         float64 `json:"latency"`
	Reliability     float64 `json:"reliability"`
	Cost            float64 `json:"cost"`
	FailureRecovery float64 `json:"failureRecovery"`
}

// RunConfig is the optional engine tuning for a challenge.
type RunConfig struct {
	MaxRetries    int     `json:"maxRetries"`
	BackoffBaseMS float64 `json:"backoffBaseMs"`
	TimeoutMS     float64 `json:"timeoutMs"`
	// WindowMS is the trend-sampling window (diagnosis). 0 → default.
	WindowMS float64 `json:"windowMs"`
}

// Defaults applied when a definition leaves a value unset.
const (
	DefaultDurationMS      = 60_000
	DefaultWindowMS        = 1_000
	DefaultMaxRetries      = 2
	DefaultBackoffBaseMS   = 5
	DefaultTimeoutMS       = 50
	maxDisplayNameLen      = 32
	minDisplayNameLen      = 2
	benchmarkSchemaVersion = 1
)

// Validate reports whether the definition is internally consistent. It is
// the single gate every embedded definition must pass at load time, so a
// malformed challenge can never reach a user.
func (d Definition) Validate() error {
	if strings.TrimSpace(d.Slug) == "" {
		return fmt.Errorf("arena: challenge slug is required")
	}
	if d.Version <= 0 {
		return fmt.Errorf("arena: challenge %s: version must be > 0", d.Slug)
	}
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("arena: challenge %s: name is required", d.Slug)
	}
	switch d.Difficulty {
	case DifficultyBeginner, DifficultyIntermediate, DifficultyAdvanced, DifficultyExpert:
	default:
		return fmt.Errorf("arena: challenge %s: unknown difficulty %q", d.Slug, d.Difficulty)
	}
	switch d.Category {
	case CategoryAPI, CategorySocial, CategoryEcommerce, CategorySaaS,
		CategoryRealtime, CategoryDistributed, CategoryCloud:
	default:
		return fmt.Errorf("arena: challenge %s: unknown category %q", d.Slug, d.Category)
	}
	switch d.Status {
	case StatusDraft, StatusActive, StatusClosed:
	default:
		return fmt.Errorf("arena: challenge %s: unknown status %q", d.Slug, d.Status)
	}
	if err := d.Workload.Spec().Validate(); err != nil {
		return fmt.Errorf("arena: challenge %s: workload: %w", d.Slug, err)
	}
	if d.Workload.DurationMS < 0 {
		return fmt.Errorf("arena: challenge %s: duration cannot be negative", d.Slug)
	}
	for i, r := range d.Requirements.RequiredKinds {
		if !validKind(r.Kind) {
			return fmt.Errorf("arena: challenge %s: requirement %d: unknown component kind %q", d.Slug, i, r.Kind)
		}
		if r.Min < 1 {
			return fmt.Errorf("arena: challenge %s: requirement %d: min must be >= 1", d.Slug, i)
		}
	}
	for i, f := range d.Failures {
		if !validKind(f.TargetKind) {
			return fmt.Errorf("arena: challenge %s: failure %d: unknown target kind %q", d.Slug, i, f.TargetKind)
		}
		if !validFailureType(f.Type) {
			return fmt.Errorf("arena: challenge %s: failure %d: unknown failure type %q", d.Slug, i, f.Type)
		}
		if f.StartMS < 0 {
			return fmt.Errorf("arena: challenge %s: failure %d: startMs cannot be negative", d.Slug, i)
		}
		if f.DurationMS <= 0 {
			return fmt.Errorf("arena: challenge %s: failure %d: durationMs must be > 0", d.Slug, i)
		}
	}
	if d.Scoring.total() <= 0 {
		return fmt.Errorf("arena: challenge %s: scoring weights must sum to > 0", d.Slug)
	}
	if d.Seed == 0 {
		return fmt.Errorf("arena: challenge %s: a benchmark seed is required", d.Slug)
	}
	return nil
}

// KindRequirementCount returns the required minimum for a kind, or 0.
func (d Definition) KindRequirementCount(kind string) int {
	for _, r := range d.Requirements.RequiredKinds {
		if r.Kind == kind {
			return r.Min
		}
	}
	return 0
}

func (w Weights) total() float64 {
	return w.Throughput + w.Latency + w.Reliability + w.Cost + w.FailureRecovery
}

// validKind reports whether kind names one of the engine's generic
// component kinds. Challenge definitions and failure targets use these
// strings so a definition stays independent of any provider catalog.
func validKind(kind string) bool {
	switch sim.ComponentKind(kind) {
	case sim.KindClient, sim.KindLoadBalancer, sim.KindAPIServer, sim.KindCache,
		sim.KindQueue, sim.KindWorker, sim.KindDatabase, sim.KindObjectStorage,
		sim.KindNetwork:
		return true
	default:
		return false
	}
}

// validFailureType reports whether t names one of the engine's failure types.
func validFailureType(t string) bool {
	switch sim.FailureType(t) {
	case sim.FailureCrash, sim.FailureLatency, sim.FailureErrorRate, sim.FailureNetwork:
		return true
	default:
		return false
	}
}

// ValidateDisplayName enforces the anonymous submission name rules.
func ValidateDisplayName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if len(trimmed) < minDisplayNameLen {
		return "", fmt.Errorf("display name must be at least %d characters", minDisplayNameLen)
	}
	if len([]rune(trimmed)) > maxDisplayNameLen {
		return "", fmt.Errorf("display name must be at most %d characters", maxDisplayNameLen)
	}
	for _, r := range trimmed {
		if r == '\n' || r == '\r' || r == '\t' || (r < 0x20) {
			return "", fmt.Errorf("display name contains control characters")
		}
	}
	return trimmed, nil
}
