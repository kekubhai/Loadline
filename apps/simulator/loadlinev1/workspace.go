package loadlinev1

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/kekubhai/Loadline/apps/simulator/internal/repositories"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
	"github.com/kekubhai/Loadline/apps/simulator/sim"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// WorkspaceService implements loadline.v1.WorkspaceService: the
// persistence-backed half of LOADLINE's API.
//
// It owns orchestration and transport adaptation only. In particular
// ExecuteSimulation loads a stored architecture version and workload,
// converts them to the domain types the simulator understands, runs the
// SAME in-memory engine SimulationService uses, and stores what came back.
// The engine remains unaware that persistence exists — it could be deleted
// and this service would still produce identical numbers.
type WorkspaceService struct {
	projects      repositories.ProjectRepository
	architectures repositories.ArchitectureRepository
	workloads     repositories.WorkloadRepository
	simulations   repositories.SimulationRepository
}

// compile-time check that the generated handler contract is met.
var _ lv1connect.WorkspaceServiceHandler = (*WorkspaceService)(nil)

// NewWorkspaceService wires the Connect service to its repositories.
func NewWorkspaceService(
	projects repositories.ProjectRepository,
	architectures repositories.ArchitectureRepository,
	workloads repositories.WorkloadRepository,
	simulations repositories.SimulationRepository,
) *WorkspaceService {
	return &WorkspaceService{
		projects:      projects,
		architectures: architectures,
		workloads:     workloads,
		simulations:   simulations,
	}
}

// ── Projects ─────────────────────────────────────────────────────────────

func (s *WorkspaceService) CreateProject(ctx context.Context, req *connect.Request[v1.CreateProjectRequest]) (*connect.Response[v1.CreateProjectResponse], error) {
	in := req.Msg
	if in.GetName() == "" {
		return nil, invalid("name is required")
	}
	created, err := s.projects.Create(ctx, workspace.Project{
		Name:        in.GetName(),
		Description: in.GetDescription(),
	})
	if err != nil {
		return nil, connectError("create project", err)
	}
	return connect.NewResponse(&v1.CreateProjectResponse{Project: projectToProto(created)}), nil
}

func (s *WorkspaceService) GetProject(ctx context.Context, req *connect.Request[v1.GetProjectRequest]) (*connect.Response[v1.GetProjectResponse], error) {
	p, err := s.projects.Get(ctx, req.Msg.GetId())
	if err != nil {
		return nil, connectError("get project", err)
	}
	return connect.NewResponse(&v1.GetProjectResponse{Project: projectToProto(p)}), nil
}

func (s *WorkspaceService) ListProjects(ctx context.Context, req *connect.Request[v1.ListProjectsRequest]) (*connect.Response[v1.ListProjectsResponse], error) {
	items, err := s.projects.List(ctx)
	if err != nil {
		return nil, connectError("list projects", err)
	}
	out := make([]*v1.Project, 0, len(items))
	for _, p := range items {
		out = append(out, projectToProto(p))
	}
	return connect.NewResponse(&v1.ListProjectsResponse{Projects: out}), nil
}

func (s *WorkspaceService) UpdateProject(ctx context.Context, req *connect.Request[v1.UpdateProjectRequest]) (*connect.Response[v1.UpdateProjectResponse], error) {
	in := req.Msg
	if in.GetName() == "" {
		return nil, invalid("name is required")
	}
	updated, err := s.projects.Update(ctx, workspace.Project{
		ID:          in.GetId(),
		Name:        in.GetName(),
		Description: in.GetDescription(),
	})
	if err != nil {
		return nil, connectError("update project", err)
	}
	return connect.NewResponse(&v1.UpdateProjectResponse{Project: projectToProto(updated)}), nil
}

func (s *WorkspaceService) DeleteProject(ctx context.Context, req *connect.Request[v1.DeleteProjectRequest]) (*connect.Response[v1.DeleteProjectResponse], error) {
	if err := s.projects.Delete(ctx, req.Msg.GetId()); err != nil {
		return nil, connectError("delete project", err)
	}
	return connect.NewResponse(&v1.DeleteProjectResponse{}), nil
}

// ── Architectures ────────────────────────────────────────────────────────

func (s *WorkspaceService) CreateArchitecture(ctx context.Context, req *connect.Request[v1.CreateArchitectureRequest]) (*connect.Response[v1.CreateArchitectureResponse], error) {
	in := req.Msg
	if in.GetProjectId() == "" {
		return nil, invalid("project_id is required")
	}
	if in.GetName() == "" {
		return nil, invalid("name is required")
	}
	if in.GetDefinition() == nil {
		return nil, invalid("definition is required")
	}
	definition, err := marshalMessage(in.GetDefinition())
	if err != nil {
		return nil, connectError("create architecture", err)
	}
	// One transaction: an architecture without its first version would be
	// unreachable (nothing to simulate).
	arch, version, err := s.architectures.CreateWithVersion(ctx, workspace.Architecture{
		ProjectID:   in.GetProjectId(),
		Name:        in.GetName(),
		Description: in.GetDescription(),
	}, definition)
	if err != nil {
		return nil, connectError("create architecture", err)
	}
	versionProto, err := versionToProto(version)
	if err != nil {
		return nil, connectError("create architecture", err)
	}
	return connect.NewResponse(&v1.CreateArchitectureResponse{
		Architecture: architectureToProto(arch),
		Version:      versionProto,
	}), nil
}

func (s *WorkspaceService) GetArchitecture(ctx context.Context, req *connect.Request[v1.GetArchitectureRequest]) (*connect.Response[v1.GetArchitectureResponse], error) {
	a, err := s.architectures.Get(ctx, req.Msg.GetId())
	if err != nil {
		return nil, connectError("get architecture", err)
	}
	return connect.NewResponse(&v1.GetArchitectureResponse{Architecture: architectureToProto(a)}), nil
}

func (s *WorkspaceService) ListArchitectures(ctx context.Context, req *connect.Request[v1.ListArchitecturesRequest]) (*connect.Response[v1.ListArchitecturesResponse], error) {
	items, err := s.architectures.ListByProject(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, connectError("list architectures", err)
	}
	out := make([]*v1.ArchitectureRecord, 0, len(items))
	for _, a := range items {
		out = append(out, architectureToProto(a))
	}
	return connect.NewResponse(&v1.ListArchitecturesResponse{Architectures: out}), nil
}

func (s *WorkspaceService) UpdateArchitecture(ctx context.Context, req *connect.Request[v1.UpdateArchitectureRequest]) (*connect.Response[v1.UpdateArchitectureResponse], error) {
	in := req.Msg
	if in.GetName() == "" {
		return nil, invalid("name is required")
	}
	existing, err := s.architectures.Get(ctx, in.GetId())
	if err != nil {
		return nil, connectError("update architecture", err)
	}
	updated, err := s.architectures.Update(ctx, workspace.Architecture{
		ID:          existing.ID,
		ProjectID:   existing.ProjectID,
		Name:        in.GetName(),
		Description: in.GetDescription(),
	})
	if err != nil {
		return nil, connectError("update architecture", err)
	}
	updated.VersionCount = existing.VersionCount
	updated.LatestVersion = existing.LatestVersion
	return connect.NewResponse(&v1.UpdateArchitectureResponse{Architecture: architectureToProto(updated)}), nil
}

func (s *WorkspaceService) DeleteArchitecture(ctx context.Context, req *connect.Request[v1.DeleteArchitectureRequest]) (*connect.Response[v1.DeleteArchitectureResponse], error) {
	if err := s.architectures.Delete(ctx, req.Msg.GetId()); err != nil {
		return nil, connectError("delete architecture", err)
	}
	return connect.NewResponse(&v1.DeleteArchitectureResponse{}), nil
}

// ── Architecture versions ────────────────────────────────────────────────

func (s *WorkspaceService) CreateArchitectureVersion(ctx context.Context, req *connect.Request[v1.CreateArchitectureVersionRequest]) (*connect.Response[v1.CreateArchitectureVersionResponse], error) {
	in := req.Msg
	if in.GetArchitectureId() == "" {
		return nil, invalid("architecture_id is required")
	}
	if in.GetDefinition() == nil {
		return nil, invalid("definition is required")
	}
	definition, err := marshalMessage(in.GetDefinition())
	if err != nil {
		return nil, connectError("create architecture version", err)
	}
	version, err := s.architectures.CreateVersion(ctx, in.GetArchitectureId(), definition)
	if err != nil {
		return nil, connectError("create architecture version", err)
	}
	versionProto, err := versionToProto(version)
	if err != nil {
		return nil, connectError("create architecture version", err)
	}
	return connect.NewResponse(&v1.CreateArchitectureVersionResponse{Version: versionProto}), nil
}

func (s *WorkspaceService) GetArchitectureVersion(ctx context.Context, req *connect.Request[v1.GetArchitectureVersionRequest]) (*connect.Response[v1.GetArchitectureVersionResponse], error) {
	v, err := s.architectures.GetVersion(ctx, req.Msg.GetId())
	if err != nil {
		return nil, connectError("get architecture version", err)
	}
	versionProto, err := versionToProto(v)
	if err != nil {
		return nil, connectError("get architecture version", err)
	}
	return connect.NewResponse(&v1.GetArchitectureVersionResponse{Version: versionProto}), nil
}

func (s *WorkspaceService) ListArchitectureVersions(ctx context.Context, req *connect.Request[v1.ListArchitectureVersionsRequest]) (*connect.Response[v1.ListArchitectureVersionsResponse], error) {
	versions, err := s.architectures.ListVersions(ctx, req.Msg.GetArchitectureId())
	if err != nil {
		return nil, connectError("list architecture versions", err)
	}
	out := make([]*v1.ArchitectureVersion, 0, len(versions))
	for _, v := range versions {
		versionProto, err := versionToProto(v)
		if err != nil {
			return nil, connectError("list architecture versions", err)
		}
		out = append(out, versionProto)
	}
	return connect.NewResponse(&v1.ListArchitectureVersionsResponse{Versions: out}), nil
}

// ── Workloads ────────────────────────────────────────────────────────────

func (s *WorkspaceService) CreateWorkload(ctx context.Context, req *connect.Request[v1.CreateWorkloadRequest]) (*connect.Response[v1.CreateWorkloadResponse], error) {
	in := req.Msg
	if in.GetArchitectureVersionId() == "" {
		return nil, invalid("architecture_version_id is required")
	}
	if in.GetName() == "" {
		return nil, invalid("name is required")
	}
	if in.GetSpec() == nil {
		return nil, invalid("spec is required")
	}
	// Reject a workload the engine could not execute before storing it: a
	// saved workload that cannot run is worse than a rejected one.
	if err := validateWorkloadSpec(in.GetSpec()); err != nil {
		return nil, err
	}
	configuration, err := marshalMessage(in.GetSpec())
	if err != nil {
		return nil, connectError("create workload", err)
	}
	created, err := s.workloads.Create(ctx, workspace.Workload{
		ArchitectureVersionID: in.GetArchitectureVersionId(),
		Name:                  in.GetName(),
		Configuration:         configuration,
	})
	if err != nil {
		return nil, connectError("create workload", err)
	}
	workloadProto, err := workloadToProto(created)
	if err != nil {
		return nil, connectError("create workload", err)
	}
	return connect.NewResponse(&v1.CreateWorkloadResponse{Workload: workloadProto}), nil
}

func (s *WorkspaceService) GetWorkload(ctx context.Context, req *connect.Request[v1.GetWorkloadRequest]) (*connect.Response[v1.GetWorkloadResponse], error) {
	w, err := s.workloads.Get(ctx, req.Msg.GetId())
	if err != nil {
		return nil, connectError("get workload", err)
	}
	workloadProto, err := workloadToProto(w)
	if err != nil {
		return nil, connectError("get workload", err)
	}
	return connect.NewResponse(&v1.GetWorkloadResponse{Workload: workloadProto}), nil
}

func (s *WorkspaceService) ListWorkloads(ctx context.Context, req *connect.Request[v1.ListWorkloadsRequest]) (*connect.Response[v1.ListWorkloadsResponse], error) {
	items, err := s.workloads.ListByVersion(ctx, req.Msg.GetArchitectureVersionId())
	if err != nil {
		return nil, connectError("list workloads", err)
	}
	out := make([]*v1.WorkloadRecord, 0, len(items))
	for _, w := range items {
		workloadProto, err := workloadToProto(w)
		if err != nil {
			return nil, connectError("list workloads", err)
		}
		out = append(out, workloadProto)
	}
	return connect.NewResponse(&v1.ListWorkloadsResponse{Workloads: out}), nil
}

func (s *WorkspaceService) UpdateWorkload(ctx context.Context, req *connect.Request[v1.UpdateWorkloadRequest]) (*connect.Response[v1.UpdateWorkloadResponse], error) {
	in := req.Msg
	if in.GetName() == "" {
		return nil, invalid("name is required")
	}
	if in.GetSpec() == nil {
		return nil, invalid("spec is required")
	}
	if err := validateWorkloadSpec(in.GetSpec()); err != nil {
		return nil, err
	}
	configuration, err := marshalMessage(in.GetSpec())
	if err != nil {
		return nil, connectError("update workload", err)
	}
	// Ownership is immutable: an update changes the name and the load
	// definition, never which architecture version the workload belongs to.
	// The existing row is read first so the persisted value is carried over
	// rather than assumed from the request.
	existing, err := s.workloads.Get(ctx, in.GetId())
	if err != nil {
		return nil, connectError("update workload", err)
	}
	updated, err := s.workloads.Update(ctx, workspace.Workload{
		ID:                    existing.ID,
		ArchitectureVersionID: existing.ArchitectureVersionID,
		Name:                  in.GetName(),
		Configuration:         configuration,
	})
	if err != nil {
		return nil, connectError("update workload", err)
	}
	workloadProto, err := workloadToProto(updated)
	if err != nil {
		return nil, connectError("update workload", err)
	}
	return connect.NewResponse(&v1.UpdateWorkloadResponse{Workload: workloadProto}), nil
}

func (s *WorkspaceService) DeleteWorkload(ctx context.Context, req *connect.Request[v1.DeleteWorkloadRequest]) (*connect.Response[v1.DeleteWorkloadResponse], error) {
	if err := s.workloads.Delete(ctx, req.Msg.GetId()); err != nil {
		return nil, connectError("delete workload", err)
	}
	return connect.NewResponse(&v1.DeleteWorkloadResponse{}), nil
}

// ── Simulation runs ──────────────────────────────────────────────────────

// ExecuteSimulation runs a stored architecture version under a stored
// workload and persists the outcome.
//
// The architecture and the workload always come from storage, never from the
// request: a persisted run can therefore never refer to a document other
// than the one that produced it. The engine call is synchronous because sim
// time is decoupled from wall time — a run costs milliseconds, and a
// straight-line call gives the caller its result in the same response.
func (s *WorkspaceService) ExecuteSimulation(ctx context.Context, req *connect.Request[v1.ExecuteSimulationRequest]) (*connect.Response[v1.ExecuteSimulationResponse], error) {
	in := req.Msg
	if in.GetArchitectureVersionId() == "" {
		return nil, invalid("architecture_version_id is required")
	}
	if in.GetWorkloadId() == "" {
		return nil, invalid("workload_id is required")
	}

	version, err := s.architectures.GetVersion(ctx, in.GetArchitectureVersionId())
	if err != nil {
		return nil, connectError("execute simulation", err)
	}
	wlRecord, err := s.workloads.Get(ctx, in.GetWorkloadId())
	if err != nil {
		return nil, connectError("execute simulation", err)
	}
	// The workload must belong to the version being run, or the stored run
	// would pair a result with an input pair that never existed together.
	if wlRecord.ArchitectureVersionID != version.ID {
		return nil, invalid("workload does not belong to this architecture version")
	}

	archProto := &v1.Architecture{}
	if err := protojson.Unmarshal(version.Definition, archProto); err != nil {
		return nil, connectError("execute simulation",
			fmt.Errorf("stored architecture definition %s is unreadable: %w", version.ID, err))
	}
	specProto := &v1.WorkloadSpec{}
	if err := protojson.Unmarshal(wlRecord.Configuration, specProto); err != nil {
		return nil, connectError("execute simulation",
			fmt.Errorf("stored workload configuration %s is unreadable: %w", wlRecord.ID, err))
	}

	arch, resolved, err := protoArchToDomain(archProto)
	if err != nil {
		return nil, invalid(fmt.Sprintf("architecture: %v", err))
	}
	wl := protoWorkloadToDomain(specProto)
	if err := wl.Validate(); err != nil {
		return nil, invalid(fmt.Sprintf("workload: %v", err))
	}

	opts := protoOptionsToDomain(in.GetOptions())
	if opts.Seed == 0 {
		// Mirror the engine default and persist the EFFECTIVE seed, so the
		// stored run can be reproduced exactly.
		opts.Seed = 1
	}
	if opts.DurationMS <= 0 {
		opts.DurationMS = 60_000
	}
	known := map[string]bool{}
	for _, c := range arch.Components {
		known[c.ID] = true
	}
	if err := validateFailures(opts.Failures, known); err != nil {
		return nil, invalid(err.Error())
	}

	started := time.Now().UTC()
	res, runErr := simulateSafely(arch, wl, opts)
	finished := time.Now().UTC()

	run := workspace.SimulationRun{
		ArchitectureVersionID: version.ID,
		WorkloadID:            wlRecord.ID,
		Seed:                  opts.Seed,
		DurationMS:            opts.DurationMS,
		StartedAt:             &started,
		FinishedAt:            &finished,
	}
	if runErr != nil {
		// The engine rejected the run. Record the failure honestly: the run
		// is persisted with no result payload rather than with fake metrics.
		run.Status = workspace.RunStatusFailed
		run.Error = runErr.Error()
		persisted, err := s.simulations.CreateRun(ctx, run)
		if err != nil {
			return nil, connectError("persist simulation run", err)
		}
		return connect.NewResponse(&v1.ExecuteSimulationResponse{
			Result: &v1.SimulationRunResult{Run: runToProto(persisted)},
		}), nil
	}

	run.Status = workspace.RunStatusCompleted
	// Store the horizon actually measured: a run that stopped early reports
	// rates over the span it processed, not over the requested horizon.
	run.DurationMS = res.Metrics.DurationMS

	out := buildRunOutput(res, resolved)
	payload, err := out.payload()
	if err != nil {
		return nil, connectError("persist simulation result", err)
	}
	// The stored result row is written atomically with the run; the response
	// echoes the same payload that was persisted.
	persistedRun, _, err := s.simulations.PersistRunWithResult(ctx, run, payload)
	if err != nil {
		return nil, connectError("persist simulation run", err)
	}
	return connect.NewResponse(&v1.ExecuteSimulationResponse{
		Result: out.proto(persistedRun),
	}), nil
}

func (s *WorkspaceService) GetSimulationRun(ctx context.Context, req *connect.Request[v1.GetSimulationRunRequest]) (*connect.Response[v1.GetSimulationRunResponse], error) {
	run, err := s.simulations.GetRun(ctx, req.Msg.GetId())
	if err != nil {
		return nil, connectError("get simulation run", err)
	}
	result, err := s.simulations.GetResultByRun(ctx, run.ID)
	if errors.Is(err, workspace.ErrNotFound) {
		// A run that produced no result payload (a failed run) is still a
		// valid answer: it carries its status and error, and no metrics.
		return connect.NewResponse(&v1.GetSimulationRunResponse{
			Result: &v1.SimulationRunResult{Run: runToProto(run)},
		}), nil
	}
	if err != nil {
		return nil, connectError("get simulation run", err)
	}
	decoded, err := resultFromWorkspace(run, result)
	if err != nil {
		return nil, connectError("get simulation run", err)
	}
	return connect.NewResponse(&v1.GetSimulationRunResponse{Result: decoded}), nil
}

func (s *WorkspaceService) ListSimulationRuns(ctx context.Context, req *connect.Request[v1.ListSimulationRunsRequest]) (*connect.Response[v1.ListSimulationRunsResponse], error) {
	in := req.Msg
	byVersion := in.GetArchitectureVersionId() != ""
	byProject := in.GetProjectId() != ""
	switch {
	case byVersion && byProject:
		return nil, invalid("set either architecture_version_id or project_id, not both")
	case !byVersion && !byProject:
		return nil, invalid("architecture_version_id or project_id is required")
	}

	var runs []workspace.SimulationRun
	var err error
	if byVersion {
		runs, err = s.simulations.ListRunsByVersion(ctx, in.GetArchitectureVersionId())
	} else {
		runs, err = s.simulations.ListRunsByProject(ctx, in.GetProjectId())
	}
	if err != nil {
		return nil, connectError("list simulation runs", err)
	}
	out := make([]*v1.SimulationRunRecord, 0, len(runs))
	for _, run := range runs {
		out = append(out, runToProto(run))
	}
	return connect.NewResponse(&v1.ListSimulationRunsResponse{Runs: out}), nil
}

// ── helpers ──────────────────────────────────────────────────────────────

// simulateSafely runs the engine with a panic barrier. sim.Simulate executes
// on the calling goroutine, so an engine panic would otherwise take down the
// whole server; instead it fails this one run.
func simulateSafely(arch sim.Architecture, spec workload.Spec, opts sim.Options) (res *sim.RunResult, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			res, err = nil, fmt.Errorf("engine panic: %v", rec)
		}
	}()
	return sim.Simulate(arch, spec, opts)
}

// invalid builds an InvalidArgument error for request-shaped mistakes.
func invalid(msg string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
}

// validateWorkloadSpec rejects a workload the engine cannot derive a plan
// from, before it is stored.
func validateWorkloadSpec(spec *v1.WorkloadSpec) error {
	if err := protoWorkloadToDomain(spec).Validate(); err != nil {
		return invalid(err.Error())
	}
	return nil
}
