package loadlinev1

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	"github.com/kekubhai/Loadline/apps/simulator/providers"
	"github.com/kekubhai/Loadline/apps/simulator/sim"
)

// This file is the bridge between the simulation engine and persistence.
//
// It exists so the dependency direction stays one-way:
//
//	WorkspaceService (this package) → sim / providers → (pure engine)
//	WorkspaceService (this package) → repositories → db → pgx
//
// The repositories never import the engine, and the engine never imports
// either. Everything the API stores is the engine's own output, serialized
// once as canonical protojson of the API messages; reading a run back
// unmarshals the same messages instead of re-deriving anything.

// runOutput holds the proto-serializable result of one simulation. It is the
// single source of truth for both the response and the stored payload, so
// the two can never disagree.
type runOutput struct {
	plan      *v1.LoadPlan
	metrics   *v1.SystemMetrics
	failures  []*v1.FailureRecord
	summary   *v1.RunSummary
	diagnosis *v1.Diagnosis
	capacity  []*v1.CapacityReport
	cost      *v1.CostEstimate
}

// buildRunOutput converts a finished engine result into proto messages.
// Capacity and cost are only present when the architecture has
// provider-backed components: they are modeled from the catalog, and
// inventing them for an all-generic architecture would be fabricating
// numbers.
func buildRunOutput(res *sim.RunResult, resolved []providers.ResolvedSpec) *runOutput {
	out := &runOutput{
		plan:     domainPlanToProto(res.Plan),
		metrics:  metricsToProto(res.Metrics),
		failures: failureRecordsToProto(res.Metrics.Failures),
		summary:  runSummaryToProto(&res.Events),
	}
	if len(resolved) > 0 {
		reports := providers.EstimateCapacity(res, resolved)
		providers.SortCapacity(reports)
		out.capacity = capacityToProto(reports)
		out.cost = costToProto(providers.EstimateCost(res, resolved, res.Plan))
	}
	// The diagnosis is computed from the measured metrics (and the capacity
	// model when provider-backed components exist) — the same call the
	// on-demand GetDiagnosis endpoint makes, so a stored run's diagnosis
	// matches what the user would see live.
	out.diagnosis = diagnosisToProto(sim.Diagnose(res.Metrics))
	return out
}

// payload renders the output as the JSONB columns of simulation_results.
func (o *runOutput) payload() (workspace.SimulationResult, error) {
	var out workspace.SimulationResult
	var err error
	if out.Summary, err = marshalMessage(o.summary); err != nil {
		return workspace.SimulationResult{}, err
	}
	if out.Plan, err = marshalMessage(o.plan); err != nil {
		return workspace.SimulationResult{}, err
	}
	if out.Metrics, err = marshalMessage(o.metrics); err != nil {
		return workspace.SimulationResult{}, err
	}
	if out.Bottlenecks, err = marshalMessage(o.diagnosis); err != nil {
		return workspace.SimulationResult{}, err
	}
	if out.Cost, err = marshalMessage(o.cost); err != nil {
		return workspace.SimulationResult{}, err
	}
	if o.capacity != nil {
		list := &v1.CapacityReportList{Reports: o.capacity}
		if out.Capacity, err = marshalMessage(list); err != nil {
			return workspace.SimulationResult{}, err
		}
	}
	if o.failures != nil {
		list := &v1.FailureRecordList{Failures: o.failures}
		if out.Failures, err = marshalMessage(list); err != nil {
			return workspace.SimulationResult{}, err
		}
	}
	return out, nil
}

// proto renders the output as the response message for a stored run.
func (o *runOutput) proto(run workspace.SimulationRun) *v1.SimulationRunResult {
	return &v1.SimulationRunResult{
		Run:       runToProto(run),
		Plan:      o.plan,
		Metrics:   o.metrics,
		Failures:  o.failures,
		Summary:   o.summary,
		Diagnosis: o.diagnosis,
		Capacity:  o.capacity,
		Cost:      o.cost,
	}
}

// resultFromWorkspace decodes a stored result back into the API message.
//
// Absent payloads decode to nil rather than to zero-valued messages: a run
// that never produced metrics must not look like a run that produced all
// zeroes.
func resultFromWorkspace(run workspace.SimulationRun, res workspace.SimulationResult) (*v1.SimulationRunResult, error) {
	out := &v1.SimulationRunResult{Run: runToProto(run)}
	var err error
	if out.Plan, err = unmarshalPlan(res.Plan); err != nil {
		return nil, err
	}
	if out.Metrics, err = unmarshalMetrics(res.Metrics); err != nil {
		return nil, err
	}
	if out.Summary, err = unmarshalSummary(res.Summary); err != nil {
		return nil, err
	}
	if out.Diagnosis, err = unmarshalDiagnosis(res.Bottlenecks); err != nil {
		return nil, err
	}
	if out.Cost, err = unmarshalCost(res.Cost); err != nil {
		return nil, err
	}
	if out.Capacity, err = unmarshalCapacity(res.Capacity); err != nil {
		return nil, err
	}
	if out.Failures, err = unmarshalFailures(res.Failures); err != nil {
		return nil, err
	}
	return out, nil
}

// marshalMessage serializes a proto message for storage. A nil message
// yields nil so the caller can decide the column's empty value.
func marshalMessage(m proto.Message) (json.RawMessage, error) {
	if m == nil {
		return nil, nil
	}
	raw, err := protojson.MarshalOptions{}.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("workspace: marshal %T: %w", m, err)
	}
	return raw, nil
}

// ── decode helpers ───────────────────────────────────────────────────────
//
// Each returns nil for an absent payload, and a non-nil message only when
// the stored JSON actually holds one.

func unmarshalPlan(raw json.RawMessage) (*v1.LoadPlan, error) {
	if isEmptyJSON(raw) {
		return nil, nil
	}
	var m v1.LoadPlan
	if err := protojson.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("workspace: decode plan: %w", err)
	}
	return &m, nil
}

func unmarshalMetrics(raw json.RawMessage) (*v1.SystemMetrics, error) {
	if isEmptyJSON(raw) {
		return nil, nil
	}
	var m v1.SystemMetrics
	if err := protojson.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("workspace: decode metrics: %w", err)
	}
	return &m, nil
}

func unmarshalSummary(raw json.RawMessage) (*v1.RunSummary, error) {
	if isEmptyJSON(raw) {
		return nil, nil
	}
	var m v1.RunSummary
	if err := protojson.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("workspace: decode summary: %w", err)
	}
	return &m, nil
}

func unmarshalDiagnosis(raw json.RawMessage) (*v1.Diagnosis, error) {
	if isEmptyJSON(raw) {
		return nil, nil
	}
	var m v1.Diagnosis
	if err := protojson.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("workspace: decode diagnosis: %w", err)
	}
	return &m, nil
}

func unmarshalCost(raw json.RawMessage) (*v1.CostEstimate, error) {
	if isEmptyJSON(raw) {
		return nil, nil
	}
	var m v1.CostEstimate
	if err := protojson.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("workspace: decode cost: %w", err)
	}
	return &m, nil
}

func unmarshalCapacity(raw json.RawMessage) ([]*v1.CapacityReport, error) {
	if isEmptyJSON(raw) {
		return nil, nil
	}
	var list v1.CapacityReportList
	if err := protojson.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("workspace: decode capacity: %w", err)
	}
	return list.GetReports(), nil
}

func unmarshalFailures(raw json.RawMessage) ([]*v1.FailureRecord, error) {
	if isEmptyJSON(raw) {
		return nil, nil
	}
	var list v1.FailureRecordList
	if err := protojson.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("workspace: decode failures: %w", err)
	}
	return list.GetFailures(), nil
}

// isEmptyJSON reports whether a stored column carries no payload. Absent
// payloads are written as the empty JSON value for their shape ("{}", "[]"),
// so both are treated as absent.
func isEmptyJSON(raw json.RawMessage) bool {
	switch string(raw) {
	case "", "{}", "[]", "null":
		return true
	default:
		return false
	}
}

// ── timestamp helpers ────────────────────────────────────────────────────

// timestampFromTime renders a domain timestamp, mapping the zero time (an
// unset column) to nil.
func timestampFromTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// timestampFromTimePtr renders an optional domain timestamp.
func timestampFromTimePtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestampFromTime(*t)
}

// ── model → proto ────────────────────────────────────────────────────────

func projectToProto(p workspace.Project) *v1.Project {
	return &v1.Project{
		Id:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		CreatedAt:   timestampFromTime(p.CreatedAt),
		UpdatedAt:   timestampFromTime(p.UpdatedAt),
	}
}

func architectureToProto(a workspace.Architecture) *v1.ArchitectureRecord {
	return &v1.ArchitectureRecord{
		Id:            a.ID,
		ProjectId:     a.ProjectID,
		Name:          a.Name,
		Description:   a.Description,
		VersionCount:  int32(a.VersionCount),
		LatestVersion: int32(a.LatestVersion),
		CreatedAt:     timestampFromTime(a.CreatedAt),
		UpdatedAt:     timestampFromTime(a.UpdatedAt),
	}
}

// versionToProto decodes the stored definition back into the canonical
// architecture message.
func versionToProto(v workspace.ArchitectureVersion) (*v1.ArchitectureVersion, error) {
	definition := &v1.Architecture{}
	if len(v.Definition) > 0 {
		if err := protojson.Unmarshal(v.Definition, definition); err != nil {
			return nil, fmt.Errorf("workspace: decode architecture definition: %w", err)
		}
	}
	return &v1.ArchitectureVersion{
		Id:             v.ID,
		ArchitectureId: v.ArchitectureID,
		Version:        int32(v.Version),
		Definition:     definition,
		CreatedAt:      timestampFromTime(v.CreatedAt),
	}, nil
}

// workloadToProto decodes the stored configuration back into the canonical
// workload spec.
func workloadToProto(w workspace.Workload) (*v1.WorkloadRecord, error) {
	spec := &v1.WorkloadSpec{}
	if len(w.Configuration) > 0 {
		if err := protojson.Unmarshal(w.Configuration, spec); err != nil {
			return nil, fmt.Errorf("workspace: decode workload configuration: %w", err)
		}
	}
	return &v1.WorkloadRecord{
		Id:                    w.ID,
		ArchitectureVersionId: w.ArchitectureVersionID,
		Name:                  w.Name,
		Spec:                  spec,
		CreatedAt:             timestampFromTime(w.CreatedAt),
		UpdatedAt:             timestampFromTime(w.UpdatedAt),
	}, nil
}

func runToProto(run workspace.SimulationRun) *v1.SimulationRunRecord {
	return &v1.SimulationRunRecord{
		Id:                    run.ID,
		ArchitectureVersionId: run.ArchitectureVersionID,
		WorkloadId:            run.WorkloadID,
		Status:                runStatusToProto(run.Status),
		Seed:                  run.Seed,
		DurationMs:            run.DurationMS,
		Error:                 run.Error,
		CreatedAt:             timestampFromTime(run.CreatedAt),
		StartedAt:             timestampFromTimePtr(run.StartedAt),
		FinishedAt:            timestampFromTimePtr(run.FinishedAt),
	}
}

// runStatusToProto maps the persisted lifecycle onto the API enum. The two
// share the same states; the API additionally has transient control states
// (paused, stopping) that are never persisted.
func runStatusToProto(s workspace.RunStatus) v1.RunStatus {
	switch s {
	case workspace.RunStatusPending:
		return v1.RunStatus_RUN_STATUS_PENDING
	case workspace.RunStatusRunning:
		return v1.RunStatus_RUN_STATUS_RUNNING
	case workspace.RunStatusCompleted:
		return v1.RunStatus_RUN_STATUS_COMPLETED
	case workspace.RunStatusFailed:
		return v1.RunStatus_RUN_STATUS_FAILED
	case workspace.RunStatusStopped:
		return v1.RunStatus_RUN_STATUS_STOPPED
	default:
		return v1.RunStatus_RUN_STATUS_UNSPECIFIED
	}
}

// ── error mapping ────────────────────────────────────────────────────────

// connectError converts a repository error into a transport error.
//
// Database errors are logged server-side and replaced with a generic
// message: internal SQL, table names, and constraint names must never reach
// a client (and a connection string must never appear in a response).
func connectError(op string, err error) error {
	switch {
	case err == nil:
		return nil
	case workspace.IsNotFound(err):
		return connect.NewError(connect.CodeNotFound, err)
	case workspace.IsInvalidID(err):
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("%s: unrecognized id", op))
	default:
		log.Printf("workspace: %s: %v", op, err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("workspace: %s failed", op))
	}
}
