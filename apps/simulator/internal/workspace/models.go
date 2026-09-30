// Package workspace holds LOADLINE's persisted document model: projects,
// architectures, architecture versions, workloads, simulation runs, and
// their result summaries.
//
// These types are deliberately plain data. They carry no database handles
// and import nothing from the simulator, so they can be used by the API
// layer, the repository layer, and tests without pulling in pgx or the
// engine. The result payloads (metrics, bottlenecks, capacity, cost) are
// stored verbatim as the engine emitted them; nothing here computes a
// simulation number.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// ErrNotFound is returned by repositories when a document does not exist.
// The API layer maps it to a "not found" status instead of leaking a
// database error.
var ErrNotFound = errors.New("workspace: not found")

// ErrInvalidID is returned when an identifier is not a canonical UUID. It
// is checked in Go before any query runs, so malformed input never reaches
// PostgreSQL as a cast error.
var ErrInvalidID = errors.New("workspace: invalid id")

// Project is a LOADLINE workspace: a container for architectures.
type Project struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Validate reports whether the project can be persisted.
func (p Project) Validate() error {
	return validateName("project", p.Name)
}

// Architecture is a logical architecture inside a project. Its content
// lives in ArchitectureVersion rows; this row is the stable identity the
// user sees and renames.
type Architecture struct {
	ID          string
	ProjectID   string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// VersionCount and LatestVersion are read-only summaries filled by
	// queries that join architecture_versions. LatestVersion is 0 when the
	// architecture has no versions yet.
	VersionCount  int
	LatestVersion int
}

// Validate reports whether the architecture can be persisted.
func (a Architecture) Validate() error {
	if err := validateName("architecture", a.Name); err != nil {
		return err
	}
	if a.ProjectID == "" {
		return fmt.Errorf("workspace: architecture: project id is required")
	}
	return ValidateID(a.ProjectID)
}

// ArchitectureVersion is an immutable snapshot of an architecture.
//
// Definition is the canonical architecture document as JSON — the API's
// Architecture message (schema_version, name, components, links). A version
// is never updated in place: modifying an architecture appends a new
// version, so a stored simulation run always refers to exactly the
// architecture it ran.
type ArchitectureVersion struct {
	ID             string
	ArchitectureID string
	// Version is the 1-based sequence number, assigned by the repository.
	Version    int
	Definition json.RawMessage
	CreatedAt  time.Time
}

// Workload is a load definition attached to one architecture version.
//
// Configuration is the canonical workload document as JSON — the API's
// WorkloadSpec message (total users, DAU, requests per user per day, peak
// multiplier, read/write ratio, payload size). The derived load plan is not
// stored here: it is a pure function of this spec and is stored per run.
type Workload struct {
	ID                    string
	ArchitectureVersionID string
	Name                  string
	Configuration         json.RawMessage
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Validate reports whether the workload can be persisted.
func (w Workload) Validate() error {
	if err := validateName("workload", w.Name); err != nil {
		return err
	}
	if w.ArchitectureVersionID == "" {
		return fmt.Errorf("workspace: workload: architecture version id is required")
	}
	if err := ValidateID(w.ArchitectureVersionID); err != nil {
		return err
	}
	if !isJSONObject(w.Configuration) {
		return fmt.Errorf("workspace: workload: configuration must be a JSON object")
	}
	return nil
}

// RunStatus is the persistable lifecycle state of a simulation run. It
// mirrors the simulation engine's own states; transient control states
// (paused, stopping) are live-process concerns and are not persisted.
type RunStatus string

const (
	// RunStatusPending: recorded, not started.
	RunStatusPending RunStatus = "pending"
	// RunStatusRunning: executing on the simulation engine.
	RunStatusRunning RunStatus = "running"
	// RunStatusCompleted: the horizon was reached; results are complete.
	RunStatusCompleted RunStatus = "completed"
	// RunStatusFailed: validation or engine error; Error explains it.
	RunStatusFailed RunStatus = "failed"
	// RunStatusStopped: stopped before the horizon; metrics are partial.
	RunStatusStopped RunStatus = "stopped"
)

// Valid reports whether the status is one the schema accepts.
func (s RunStatus) Valid() bool {
	switch s {
	case RunStatusPending, RunStatusRunning, RunStatusCompleted, RunStatusFailed, RunStatusStopped:
		return true
	default:
		return false
	}
}

// MaxSeed is the largest seed the schema can store (bigint). The engine
// takes a uint64, but no realistic seed approaches this bound, and silently
// truncating a seed would break reproducibility.
const MaxSeed = math.MaxInt64

// SimulationRun is one executed simulation.
type SimulationRun struct {
	ID                    string
	ArchitectureVersionID string
	// WorkloadID is empty when the workload was deleted; the run keeps its
	// history.
	WorkloadID string
	Status     RunStatus
	// Seed is persisted for every run so a stored run is reproducible.
	Seed uint64
	// DurationMS is the simulated horizon that was actually measured.
	DurationMS float64
	// Error is set when Status is failed.
	Error      string
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// Validate reports whether the run can be persisted.
func (r SimulationRun) Validate() error {
	if r.ArchitectureVersionID == "" {
		return fmt.Errorf("workspace: run: architecture version id is required")
	}
	if err := ValidateID(r.ArchitectureVersionID); err != nil {
		return err
	}
	if r.WorkloadID != "" {
		if err := ValidateID(r.WorkloadID); err != nil {
			return err
		}
	}
	if !r.Status.Valid() {
		return fmt.Errorf("workspace: run: unknown status %q", r.Status)
	}
	if r.Seed > MaxSeed {
		return fmt.Errorf("workspace: run: seed %d exceeds the storable maximum %d", r.Seed, uint64(MaxSeed))
	}
	if r.DurationMS < 0 || math.IsNaN(r.DurationMS) || math.IsInf(r.DurationMS, 0) {
		return fmt.Errorf("workspace: run: duration must be a finite value >= 0, got %v", r.DurationMS)
	}
	return nil
}

// SimulationResult is the stored summary of one finished simulation run.
//
// Every field is JSON produced during the run and stored verbatim:
//
//   - Summary:     engine counters and stop reason (RunSummary).
//   - Plan:        the derived workload chain (LoadPlan).
//   - Metrics:     system + per-component metrics (SystemMetrics).
//   - Capacity:    per-component modeled ceilings (CapacityReport[]).
//   - Bottlenecks: the diagnosis with its computed reasons (Diagnosis).
//   - Failures:    the per-request failure log (FailureRecord[]).
//   - Cost:        the monthly estimate and its assumptions (CostEstimate).
//
// Absent payloads stay nil and are stored as the empty JSON value for their
// shape, so a failed run never carries fabricated metrics.
type SimulationResult struct {
	ID              string
	SimulationRunID string
	Summary         json.RawMessage
	Plan            json.RawMessage
	Metrics         json.RawMessage
	Capacity        json.RawMessage
	Bottlenecks     json.RawMessage
	Failures        json.RawMessage
	Cost            json.RawMessage
	CreatedAt       time.Time
}

// validateName enforces the non-empty-name rule the schema also enforces.
func validateName(kind, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("workspace: %s: name is required", kind)
	}
	return nil
}

// isJSONObject reports whether raw is a JSON object (or empty, which the
// schema stores as an empty object).
func isJSONObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return true
	}
	return strings.HasPrefix(trimmed, "{")
}
