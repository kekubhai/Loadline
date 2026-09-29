package loadlinev1

import (
	"context"
	"fmt"
	"sync"

	"connectrpc.com/connect"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	"github.com/kekubhai/Loadline/apps/simulator/providers"
	"github.com/kekubhai/Loadline/apps/simulator/sim"
)

// RunComparison simulates two or more architectures against ONE shared
// workload with ONE shared seed/options. Every architecture runs
// independently and in isolation; identical inputs mean identical
// arrivals, so metric differences come from the architecture and nothing
// else.
//
// There is deliberately no overall score, no ranking, and no
// recommendation anywhere in the output: the response is a factual
// per-architecture result set (metrics, bottlenecks, capacity, cost),
// exactly what a solo run of each architecture would have produced.
//
// A failure scenario in the shared options is applied to every
// architecture; targets are validated against EACH architecture (the
// comparison is rejected up front if a target is missing anywhere, so a
// silently-skewed comparison cannot happen).
func (s *Service) RunComparison(ctx context.Context, req *connect.Request[v1.RunComparisonRequest]) (*connect.Response[v1.RunComparisonResponse], error) {
	in := req.Msg

	if in.GetWorkload() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("workload is required"))
	}
	wl := protoWorkloadToDomain(in.GetWorkload())
	if err := wl.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if len(in.GetArchitectures()) < 2 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("a comparison needs at least 2 architectures, got %d", len(in.GetArchitectures())))
	}
	if len(in.GetArchitectures()) > 8 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("at most 8 architectures per comparison, got %d", len(in.GetArchitectures())))
	}
	// Shared options; seed 0 defaults to 1 (mirroring solo runs) and is
	// echoed back in the result.
	opts := protoOptionsToDomain(in.GetOptions())
	if opts.Seed == 0 {
		opts.Seed = 1
	}
	if opts.DurationMS <= 0 {
		opts.DurationMS = 60_000
	}

	// Resolve every architecture up front. All-or-nothing on shared
	// failure targets: the same failure scenario must apply to every
	// architecture or the comparison would be skewed.
	type resolved struct {
		name     string
		arch     sim.Architecture
		services []providers.ResolvedSpec
	}
	entries := make([]resolved, 0, len(in.GetArchitectures()))
	seen := map[string]bool{}
	for i, ca := range in.GetArchitectures() {
		if ca.GetArchitecture() == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("architecture %d (%q): architecture is required", i, ca.GetName()))
		}
		name := ca.GetName()
		if name == "" {
			name = fmt.Sprintf("architecture-%d", i+1)
		}
		if seen[name] {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("duplicate comparison name %q", name))
		}
		seen[name] = true

		arch, services, err := protoArchToDomain(ca.GetArchitecture())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("architecture %q: %w", name, err))
		}
		known := map[string]bool{}
		for _, c := range arch.Components {
			known[c.ID] = true
		}
		if err := validateFailures(opts.Failures, known); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("architecture %q: %w", name, err))
		}
		entries = append(entries, resolved{name: name, arch: arch, services: services})
	}

	// Simulate independently. Sequential execution keeps the process
	// single-threaded for determinism clarity; runs are fast (sim time
	// decoupled from wall time). Each architecture's validation error is
	// isolated into its own entry: one bad architecture never sinks the
	// whole comparison.
	out := make([]*v1.ComparisonEntry, len(entries))
	var wg sync.WaitGroup
	for i := range entries {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each architecture runs on its own goroutine, so a panic
			// inside the engine would take down the whole process — the
			// handler's own recover() never sees another goroutine's
			// panic. Contain it to this entry instead: the other
			// architectures still return real results.
			defer func() {
				if rec := recover(); rec != nil {
					e := entries[i]
					out[i] = &v1.ComparisonEntry{
						Name:  e.name,
						Error: fmt.Sprintf("engine panic: %v", rec),
					}
				}
			}()
			e := entries[i]
			entry := &v1.ComparisonEntry{Name: e.name}
			res, err := sim.Simulate(e.arch, wl, opts)
			if err != nil {
				entry.Error = err.Error()
				out[i] = entry
				return
			}
			entry.Plan = domainPlanToProto(res.Plan)
			entry.Metrics = metricsToProto(res.Metrics)
			entry.Failures = failureRecordsToProto(res.Metrics.Failures)
			entry.Summary = runSummaryToProto(&res.Events)
			diag := sim.Diagnose(res.Metrics)
			entry.Diagnosis = diagnosisToProto(diag)
			if len(e.services) > 0 {
				reports := providers.EstimateCapacity(res, e.services)
				providers.SortCapacity(reports)
				entry.Capacity = capacityToProto(reports)
				entry.Cost = costToProto(providers.EstimateCost(res, e.services, res.Plan))
			}
			out[i] = entry
		}(i)
	}
	wg.Wait()

	result := &v1.ComparisonResult{
		Workload: in.GetWorkload(),
		Seed:     opts.Seed,
		Entries:  out,
	}
	id := s.storeComparison(result)
	return connect.NewResponse(&v1.RunComparisonResponse{
		Result:       result,
		ComparisonId: id,
	}), nil
}

// GetComparison re-fetches a stored comparison result.
func (s *Service) GetComparison(ctx context.Context, req *connect.Request[v1.GetComparisonRequest]) (*connect.Response[v1.GetComparisonResponse], error) {
	s.compMu.RLock()
	res, ok := s.comparisons[req.Msg.GetComparisonId()]
	s.compMu.RUnlock()
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("comparison %q not found", req.Msg.GetComparisonId()))
	}
	return connect.NewResponse(&v1.GetComparisonResponse{Result: res}), nil
}

// maxComparisons bounds the in-memory comparison store: comparisons are
// re-runnable, so keeping the most recent window is enough and the
// process never accumulates results forever.
const maxComparisons = 32

// storeComparison persists a comparison result under an assigned ID so
// the frontend can re-open it without re-running.
func (s *Service) storeComparison(res *v1.ComparisonResult) string {
	s.compMu.Lock()
	defer s.compMu.Unlock()
	s.compSeq++
	id := fmt.Sprintf("cmp-%d", s.compSeq)
	s.comparisons[id] = res
	// IDs are assigned in increasing order, so the lexicographically
	// smallest is the oldest once the store is over budget.
	if len(s.comparisons) > maxComparisons {
		oldest := ""
		for k := range s.comparisons {
			if oldest == "" || k < oldest {
				oldest = k
			}
		}
		delete(s.comparisons, oldest)
	}
	return id
}
