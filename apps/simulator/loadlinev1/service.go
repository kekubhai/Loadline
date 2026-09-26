package loadlinev1

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"

	"connectrpc.com/connect"
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
}

// compile-time check that the generated handler contract is met.
var _ lv1connect.SimulationServiceHandler = (*Service)(nil)

// NewService wires the Connect service to a run store.
func NewService(store *Store) *Service {
	return &Service{store: store}
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
	go s.execute(r)

	return connect.NewResponse(&v1.RunSimulationResponse{Simulation: r.Sim}), nil
}

// execute performs the actual simulation on this goroutine, publishing
// progress and results into the store as it goes. The hook it hands to
// the engine is observational: it cannot influence the simulation.
func (s *Service) execute(r *Run) {
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

	res, err := sim.Simulate(arch, wl, opts)
	if err != nil {
		r.SetStatus(v1.RunStatus_RUN_STATUS_FAILED, err.Error())
		return
	}
	r.SetResult(res, resolved)
	r.SetStatus(v1.RunStatus_RUN_STATUS_COMPLETED, "")
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
