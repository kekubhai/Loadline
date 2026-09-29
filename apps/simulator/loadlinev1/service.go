package loadlinev1

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/kekubhai/Loadline/apps/simulator/engine"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
	"github.com/kekubhai/Loadline/apps/simulator/providers"
	"github.com/kekubhai/Loadline/apps/simulator/sim"
)

// Service implements loadline.v1.SimulationService over the sim engine.
// It owns lifecycle and transport adaptation only: every number it
// returns was computed by the simulation kernel or the provider models.
type Service struct {
	store *Store
	seq   atomic.Uint64 // per-process goroutine bookkeeping (diagnostics)

	// comparison store: results of RunComparison calls, fetchable via
	// GetComparison. In-memory, mirroring the run store.
	compMu      sync.RWMutex
	comparisons map[string]*v1.ComparisonResult
	compSeq     int
}

// compile-time check that the generated handler contract is met.
var _ lv1connect.SimulationServiceHandler = (*Service)(nil)

// NewService wires the Connect service to a run store.
func NewService(store *Store) *Service {
	return &Service{
		store:       store,
		comparisons: map[string]*v1.ComparisonResult{},
	}
}

// CreateSimulation validates and stores a simulation without running it.
// Validation here is structural (architecture + workload + provider
// references + failure specs) so bad documents fail fast; engine-side
// validation runs again at execution time.
func (s *Service) CreateSimulation(ctx context.Context, req *connect.Request[v1.CreateSimulationRequest]) (*connect.Response[v1.CreateSimulationResponse], error) {
	in := req.Msg
	if in.GetArchitecture() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("architecture is required"))
	}
	if in.GetWorkload() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("workload is required"))
	}

	// Fail fast on unknown providers/services, kinds, and malformed specs.
	arch, _, err := protoArchToDomain(in.GetArchitecture())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	wl := protoWorkloadToDomain(in.GetWorkload())
	if err := wl.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	known := map[string]bool{}
	for _, c := range arch.Components {
		known[c.ID] = true
	}
	opts := protoOptionsToDomain(in.GetOptions())
	if err := validateFailures(opts.Failures, known); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if in.GetArchitecture().GetSchemaVersion() == "" {
		in.Architecture.SchemaVersion = "1"
	}
	created, err := s.store.Create(&v1.Simulation{
		Architecture: in.GetArchitecture(),
		Workload:     in.GetWorkload(),
		Options:      in.GetOptions(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.CreateSimulationResponse{Simulation: created.Sim}), nil
}

// validateFailures checks failure specs against the architecture's
// component-ID set.
func validateFailures(fs []sim.Failure, known map[string]bool) error {
	for i, f := range fs {
		if !known[f.Target] {
			return fmt.Errorf("failure %d: unknown target component %q", i, f.Target)
		}
		if f.StartMS < 0 {
			return fmt.Errorf("failure %d: StartMS cannot be negative", i)
		}
		if f.DurationMS <= 0 {
			return fmt.Errorf("failure %d: DurationMS must be > 0", i)
		}
	}
	return nil
}

// RunSimulation starts asynchronous execution on the simulation engine
// and returns immediately. Progress is observable via GetSimulationStatus
// and StreamMetrics; the run is already marked RUNNING when this returns.
func (s *Service) RunSimulation(ctx context.Context, req *connect.Request[v1.RunSimulationRequest]) (*connect.Response[v1.RunSimulationResponse], error) {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	r.mu.RLock()
	started, status := r.Started, r.Status
	r.mu.RUnlock()
	if started {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("simulation %s already %s", r.ID, status))
	}

	r.mu.Lock()
	r.Started = true
	r.Status = v1.RunStatus_RUN_STATUS_RUNNING
	r.mu.Unlock()

	s.seq.Add(1)
	go s.execute(r, req.Msg.GetWallDurationMs())

	return connect.NewResponse(&v1.RunSimulationResponse{Simulation: r.Sim}), nil
}

// execute performs the actual simulation on this goroutine via
// sim.StartPaced, publishing progress and results into the store as it
// goes. The hook it hands to the engine is observational: it cannot
// influence the simulation. Pacing: wallDuration 0 → as fast as possible
// (unchanged default); >0 → the horizon takes that much wall time and
// the run becomes pausable/stoppable at natural sampling boundaries.
func (s *Service) execute(r *Run, wallDurationMS float64) {
	defer func() {
		if rec := recover(); rec != nil {
			r.SetStatus(v1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("engine panic: %v", rec))
		}
	}()

	arch, resolved, err := protoArchToDomain(r.Sim.Architecture)
	if err != nil {
		r.SetStatus(v1.RunStatus_RUN_STATUS_FAILED, err.Error())
		return
	}
	wl := protoWorkloadToDomain(r.Sim.Workload)
	opts := protoOptionsToDomain(r.Sim.Options)
	opts.Progress = func(snap sim.Snapshot) {
		r.SetProgress(snapshotToProto(snap))
	}

	paced, err := sim.StartPaced(arch, wl, opts, wallDurationMS)
	if err != nil {
		r.SetStatus(v1.RunStatus_RUN_STATUS_FAILED, err.Error())
		return
	}
	r.SetControl(paced.Control())

	res := paced.WaitResult()
	if res == nil {
		r.SetStatus(v1.RunStatus_RUN_STATUS_FAILED, "engine returned no result")
		return
	}
	if res.Events.StopReason == engine.StopStopped {
		// Stopped before the horizon: publish the partial run under the
		// distinct STOPPED status (results stay queryable).
		r.SetStatusStopped(res, resolved)
		return
	}
	r.SetResult(res, resolved)
	r.SetStatus(v1.RunStatus_RUN_STATUS_COMPLETED, "")
}

// PauseSimulation pauses a RUNNING run at the current simulated instant.
// The status guard makes the transition atomic with respect to the
// engine's completion: once the run reaches COMPLETED/STOPPED/FAILED,
// Pause can no longer overwrite it (a pause landing after the last event
// is rejected instead of clobbering the terminal status).
func (s *Service) PauseSimulation(ctx context.Context, req *connect.Request[v1.PauseSimulationRequest]) (*connect.Response[v1.PauseSimulationResponse], error) {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	ctl := r.Control()
	if ctl == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("simulation %s is not running yet", r.ID))
	}
	if r.currentStatus() != v1.RunStatus_RUN_STATUS_RUNNING {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("simulation %s is %s, not RUNNING; cannot pause", r.ID, r.currentStatus()))
	}
	if !ctl.Pause() {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("simulation %s is no longer running; cannot pause", r.ID))
	}
	r.SetStatusPaused()
	return connect.NewResponse(&v1.PauseSimulationResponse{
		SimulationId: r.ID, Status: v1.RunStatus_RUN_STATUS_PAUSED,
	}), nil
}

// ResumeSimulation resumes a PAUSED run, optionally re-targeting pacing.
// The PAUSED guard prevents a stale resume from overwriting a terminal
// status with RUNNING.
func (s *Service) ResumeSimulation(ctx context.Context, req *connect.Request[v1.ResumeSimulationRequest]) (*connect.Response[v1.ResumeSimulationResponse], error) {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	ctl := r.Control()
	if ctl == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("simulation %s is not running yet", r.ID))
	}
	if r.currentStatus() != v1.RunStatus_RUN_STATUS_PAUSED {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("simulation %s is %s, not PAUSED; cannot resume", r.ID, r.currentStatus()))
	}
	if req.Msg.GetWallDurationMs() > 0 {
		applyWallDuration(ctl, req.Msg.GetWallDurationMs(), r.Sim.GetOptions().GetDurationMs())
	}
	ctl.Resume()
	r.SetStatusRunning()
	return connect.NewResponse(&v1.ResumeSimulationResponse{
		SimulationId: r.ID, Status: v1.RunStatus_RUN_STATUS_RUNNING,
	}), nil
}

// StopSimulation stops a RUNNING or PAUSED run early; partial results
// remain queryable under the STOPPED status. Terminal runs are rejected:
// a stop that lands after completion must not re-label the run STOPPING.
func (s *Service) StopSimulation(ctx context.Context, req *connect.Request[v1.StopSimulationRequest]) (*connect.Response[v1.StopSimulationResponse], error) {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	ctl := r.Control()
	if ctl == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("simulation %s is not running yet", r.ID))
	}
	switch r.currentStatus() {
	case v1.RunStatus_RUN_STATUS_RUNNING, v1.RunStatus_RUN_STATUS_PAUSED:
		// live run: stopping is legal
	default:
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("simulation %s is %s; cannot stop", r.ID, r.currentStatus()))
	}
	ctl.Stop()
	return connect.NewResponse(&v1.StopSimulationResponse{
		SimulationId: r.ID, Status: v1.RunStatus_RUN_STATUS_STOPPING,
	}), nil
}

// SetWallDuration re-targets the wall-clock pacing of a live run.
// wallDurationMS ≤ 0 restores as-fast-as-possible; N ms means the full
// horizon takes N ms of wall time; the speed of a run already paced is
// multiplied through by the caller (speed buttons compute this).
func (s *Service) SetWallDuration(ctx context.Context, req *connect.Request[v1.SetWallDurationRequest]) (*connect.Response[v1.SetWallDurationResponse], error) {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	ctl := r.Control()
	if ctl == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("simulation %s is not running yet", r.ID))
	}
	applyWallDuration(ctl, req.Msg.GetWallDurationMs(), r.Sim.GetOptions().GetDurationMs())
	return connect.NewResponse(&v1.SetWallDurationResponse{
		SimulationId: r.ID, Status: r.currentStatus(),
	}), nil
}

// applyWallDuration converts a wall duration for the full horizon into
// the per-event tick (the engine's pacing primitive). wallDurationMS ≤ 0
// restores as-fast-as-possible.
func applyWallDuration(ctl sim.SimRunControl, wallDurationMS, horizonMS float64) {
	runner, ok := ctl.(*engine.Runner)
	if !ok {
		return
	}
	if wallDurationMS <= 0 {
		runner.SetTick(0)
		return
	}
	if horizonMS <= 0 {
		horizonMS = 60_000
	}
	const eventsPerSimMS = 2 // mirrors sim.StartPaced's estimate
	budget := int64(horizonMS * eventsPerSimMS)
	if budget < 1 {
		budget = 1
	}
	tickNS := int64(wallDurationMS * float64(time.Millisecond) / float64(budget))
	if tickNS < 1 {
		tickNS = 1
	}
	runner.SetTick(time.Duration(tickNS))
}

// currentStatus reads the run's status safely.
func (r *Run) currentStatus() v1.RunStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Status
}

// GetSimulationStatus polls the run state and latest progress snapshot.
func (s *Service) GetSimulationStatus(ctx context.Context, req *connect.Request[v1.GetSimulationStatusRequest]) (*connect.Response[v1.GetSimulationStatusResponse], error) {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	r.mu.RLock()
	resp := &v1.GetSimulationStatusResponse{
		SimulationId: r.ID,
		Status:       r.Status,
		Error:        r.Err,
		Progress:     r.Progress,
	}
	r.mu.RUnlock()
	return connect.NewResponse(resp), nil
}

// StreamMetrics emits one progress frame per sampling window while the
// run executes, then a terminal status frame and (on success) the final
// results. The stream always ends after the terminal frame. Frames are
// triggered by real state changes in the store — no polling timers, and
// nothing here can influence the simulation.
func (s *Service) StreamMetrics(ctx context.Context, req *connect.Request[v1.StreamMetricsRequest], ss *connect.ServerStream[v1.StreamMetricsResponse]) error {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}

	var lastSent *v1.ProgressSnapshot
	lastBroadcast := v1.RunStatus_RUN_STATUS_UNSPECIFIED
	for {
		// Read version and state atomically so a change landing between
		// this read and the subscribe below cannot be missed: subscribe()
		// re-checks the version under the run's lock.
		version, status, errMsg, snap := r.State()

		// Replay the latest known snapshot before terminal frames: a run
		// can finish faster than the subscriber attaches (simulations run
		// far faster than wall time), and the subscriber must still see
		// the most recent live state.
		if snap != nil && snap != lastSent {
			if err := ss.Send(&v1.StreamMetricsResponse{Event: &v1.StreamMetricsResponse_Progress{
				Progress: snap,
			}}); err != nil {
				return err
			}
			lastSent = snap
		}

		switch status {
		case v1.RunStatus_RUN_STATUS_COMPLETED:
			if err := ss.Send(&v1.StreamMetricsResponse{Event: &v1.StreamMetricsResponse_Status{
				Status: &v1.GetSimulationStatusResponse{
					SimulationId: r.ID, Status: status,
				},
			}}); err != nil {
				return err
			}
			res, _, err := r.ResultsFor()
			if err != nil {
				return connect.NewError(connect.CodeInternal, err)
			}
			return ss.Send(&v1.StreamMetricsResponse{Event: &v1.StreamMetricsResponse_Results{
				Results: finalResults(res),
			}})
		case v1.RunStatus_RUN_STATUS_FAILED:
			return ss.Send(&v1.StreamMetricsResponse{Event: &v1.StreamMetricsResponse_Status{
				Status: &v1.GetSimulationStatusResponse{
					SimulationId: r.ID, Status: status, Error: errMsg,
				},
			}})
		case v1.RunStatus_RUN_STATUS_STOPPED:
			// Terminal: stopped early. Send the control transition frame,
			// the terminal status, and the partial results.
			simTime := 0.0
			if snap != nil {
				simTime = snap.GetSimTimeMs()
			}
			if err := ss.Send(&v1.StreamMetricsResponse{Event: &v1.StreamMetricsResponse_Control{
				Control: &v1.ControlFrame{
					SimulationId: r.ID, Status: status, SimTimeMs: simTime,
				},
			}}); err != nil {
				return err
			}
			if err := ss.Send(&v1.StreamMetricsResponse{Event: &v1.StreamMetricsResponse_Status{
				Status: &v1.GetSimulationStatusResponse{
					SimulationId: r.ID, Status: status,
				},
			}}); err != nil {
				return err
			}
			res, _, err := r.ResultsFor()
			if err != nil {
				return connect.NewError(connect.CodeInternal, err)
			}
			return ss.Send(&v1.StreamMetricsResponse{Event: &v1.StreamMetricsResponse_Results{
				Results: finalResults(res),
			}})
		case v1.RunStatus_RUN_STATUS_PAUSED:
			// Broadcast the pause transition once per observed change; the
			// stream stays open and resumes when the run does.
			if status != lastBroadcast {
				lastBroadcast = status
				simTime := 0.0
				if snap != nil {
					simTime = snap.GetSimTimeMs()
				}
				if err := ss.Send(&v1.StreamMetricsResponse{Event: &v1.StreamMetricsResponse_Control{
					Control: &v1.ControlFrame{
						SimulationId: r.ID, Status: status, SimTimeMs: simTime,
					},
				}}); err != nil {
					return err
				}
			}
		}

		// Wait for the next state change or stream cancellation.
		wake, cancel, missed := r.subscribe(version)
		if missed {
			cancel() // state changed while we subscribed — re-read it
			continue
		}
		select {
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		case <-wake:
		}
		cancel()
	}
}

// finalResults builds the FinalResults message from a finished run.
func finalResults(res *sim.RunResult) *v1.FinalResults {
	return &v1.FinalResults{
		Plan:     domainPlanToProto(res.Plan),
		Metrics:  metricsToProto(res.Metrics),
		Failures: failureRecordsToProto(res.Metrics.Failures),
		Summary:  runSummaryToProto(&res.Events),
	}
}

// GetResults returns the stored final results of a completed run.
func (s *Service) GetResults(ctx context.Context, req *connect.Request[v1.GetResultsRequest]) (*connect.Response[v1.GetResultsResponse], error) {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	res, _, err := r.ResultsFor()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&v1.GetResultsResponse{
		SimulationId: r.ID,
		Plan:         domainPlanToProto(res.Plan),
		Metrics:      metricsToProto(res.Metrics),
		Failures:     failureRecordsToProto(res.Metrics.Failures),
		Summary:      runSummaryToProto(&res.Events),
	}), nil
}

// GetDiagnosis returns the bottleneck diagnosis, optionally against a
// baseline run (typically the same architecture without failures).
func (s *Service) GetDiagnosis(ctx context.Context, req *connect.Request[v1.GetDiagnosisRequest]) (*connect.Response[v1.GetDiagnosisResponse], error) {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	res, _, err := r.ResultsFor()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	var diag sim.Diagnosis
	if baseID := req.Msg.GetBaselineSimulationId(); baseID != "" {
		base, err := s.store.Get(baseID)
		if err != nil {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		baseRes, _, err := base.ResultsFor()
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("baseline %s: %w", baseID, err))
		}
		diag = sim.DiagnoseWithBaseline(res.Metrics, baseRes.Metrics)
	} else {
		diag = sim.Diagnose(res.Metrics)
	}
	return connect.NewResponse(&v1.GetDiagnosisResponse{
		SimulationId: r.ID,
		Diagnosis:    diagnosisToProto(diag),
	}), nil
}

// GetCapacity returns per-component capacity estimates for
// provider-backed components of a completed run.
func (s *Service) GetCapacity(ctx context.Context, req *connect.Request[v1.GetCapacityRequest]) (*connect.Response[v1.GetCapacityResponse], error) {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	res, resolved, err := r.ResultsFor()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if len(resolved) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("architecture has no provider-backed components; capacity estimates need provider references"))
	}

	want := map[string]bool{}
	for _, id := range req.Msg.GetComponentIds() {
		want[id] = true
	}
	reports := providers.EstimateCapacity(res, resolved)
	filtered := make([]providers.CapacityReport, 0, len(reports))
	for _, cr := range reports {
		if len(want) == 0 || want[cr.ComponentID] {
			filtered = append(filtered, cr)
		}
	}
	providers.SortCapacity(filtered)
	return connect.NewResponse(&v1.GetCapacityResponse{
		SimulationId: r.ID,
		Reports:      capacityToProto(filtered),
	}), nil
}

// GetCostEstimate returns the monthly cost estimate derived from the
// run's measured usage. Always an ESTIMATE — never live billing data.
func (s *Service) GetCostEstimate(ctx context.Context, req *connect.Request[v1.GetCostEstimateRequest]) (*connect.Response[v1.GetCostEstimateResponse], error) {
	r, err := s.store.Get(req.Msg.GetSimulationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	res, resolved, err := r.ResultsFor()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if len(resolved) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("architecture has no provider-backed components; cost estimates need provider references"))
	}
	est := providers.EstimateCost(res, resolved, res.Plan)
	return connect.NewResponse(&v1.GetCostEstimateResponse{
		SimulationId: r.ID,
		Estimate:     costToProto(est),
	}), nil
}

// ListCatalog returns the built-in provider catalog in deterministic
// (provider, service) order.
func (s *Service) ListCatalog(ctx context.Context, req *connect.Request[v1.ListCatalogRequest]) (*connect.Response[v1.ListCatalogResponse], error) {
	catalog := providers.Catalog()
	out := make([]*v1.CatalogService, 0, len(catalog))
	for _, m := range catalog {
		cap := m.Capacity()
		out = append(out, &v1.CatalogService{
			Provider:             m.Provider(),
			Service:              m.Service(),
			Summary:              m.Summary(),
			ComponentKind:        componentKindName(m),
			Concurrency:          int32(cap.Concurrency),
			QueueLimit:           int32(cap.QueueLimit),
			ModeledRps:           cap.ModeledRPS,
			DefaultServiceTimeMs: cap.ServiceTimeMS,
			ScalingKind:          m.Scaling().Kind,
			PerRequestPrice:      m.Pricing().PerRequest,
			PerInstanceHourPrice: m.Pricing().PerInstanceHour,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GetProvider() != out[j].GetProvider() {
			return out[i].GetProvider() < out[j].GetProvider()
		}
		return out[i].GetService() < out[j].GetService()
	})
	return connect.NewResponse(&v1.ListCatalogResponse{Services: out}), nil
}

// componentKindName maps a service model onto its generic kind name.
func componentKindName(m providers.ServiceModel) string {
	spec, err := m.BuildSpec("probe", providers.Config{})
	if err != nil {
		return ""
	}
	return string(spec.Kind)
}
