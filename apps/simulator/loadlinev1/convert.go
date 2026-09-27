// Package loadlinev1 implements LOADLINE's ConnectRPC API over the Go
// simulation engine.
//
// Dependency direction (enforced by imports):
//
//	connectrpc handlers → this package → sim / workload / providers
//
// The simulation engine (engine, sim, workload) never imports this
// package or any transport concern: sim stays runnable and testable
// without an API, and this layer only adapts messages and orchestrates
// run lifecycle. No simulation behavior lives here.
package loadlinev1

import (
	"fmt"

	"github.com/kekubhai/Loadline/apps/simulator/engine"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	"github.com/kekubhai/Loadline/apps/simulator/providers"
	"github.com/kekubhai/Loadline/apps/simulator/sim"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// protoKindToDomain maps the proto enum onto the generic component kind
// strings the simulator understands.
func protoKindToDomain(k v1.ComponentKind) sim.ComponentKind {
	switch k {
	case v1.ComponentKind_COMPONENT_KIND_CLIENT:
		return sim.KindClient
	case v1.ComponentKind_COMPONENT_KIND_LOAD_BALANCER:
		return sim.KindLoadBalancer
	case v1.ComponentKind_COMPONENT_KIND_API_SERVER:
		return sim.KindAPIServer
	case v1.ComponentKind_COMPONENT_KIND_CACHE:
		return sim.KindCache
	case v1.ComponentKind_COMPONENT_KIND_QUEUE:
		return sim.KindQueue
	case v1.ComponentKind_COMPONENT_KIND_WORKER:
		return sim.KindWorker
	case v1.ComponentKind_COMPONENT_KIND_DATABASE:
		return sim.KindDatabase
	case v1.ComponentKind_COMPONENT_KIND_OBJECT_STORAGE:
		return sim.KindObjectStorage
	case v1.ComponentKind_COMPONENT_KIND_NETWORK:
		return sim.KindNetwork
	default:
		return ""
	}
}

// protoArchToDomain converts a proto Architecture into the generic
// sim.Architecture. Provider references (provider/service/config) are
// resolved here so the simulator only ever sees generic specs.
func protoArchToDomain(pb *v1.Architecture) (sim.Architecture, []providers.ResolvedSpec, error) {
	if pb == nil {
		return sim.Architecture{}, nil, fmt.Errorf("architecture is required")
	}
	out := sim.Architecture{
		Name:       pb.GetName(),
		Components: make([]sim.ComponentSpec, 0, len(pb.GetComponents())),
		Links:      make([]sim.Link, 0, len(pb.GetLinks())),
	}
	var resolved []providers.ResolvedSpec

	for _, c := range pb.GetComponents() {
		kind := protoKindToDomain(c.GetKind())
		if kind == "" {
			return sim.Architecture{}, nil,
				fmt.Errorf("component %q: unknown or unspecified kind", c.GetId())
		}
		spec := sim.ComponentSpec{
			ID:                       c.GetId(),
			Kind:                     kind,
			DefaultServiceTimeMillis: c.GetDefaultServiceTimeMillis(),
			Concurrency:              int(c.GetConcurrency()),
			QueueLimit:               int(c.GetQueueLimit()),
			CapacityRPS:              c.GetCapacityRps(),
			HitRatio:                 c.GetHitRatio(),
			FanOut:                   int(c.GetFanOut()),
			ServiceTimeMillis:        map[sim.Op]float64{},
		}
		for op, ms := range c.GetServiceTimeMillis() {
			spec.ServiceTimeMillis[sim.Op(op)] = ms
		}

		// Provider-backed component: fill unspecified defaults from the
		// catalog and remember the resolved model for capacity/cost.
		if c.GetProvider() != "" {
			cfg := c.GetConfig()
			hit := cfg.GetHitRatio()
			if hit == 0 {
				hit = c.GetHitRatio()
			}
			rs, err := providers.ResolveService(c.GetId(), c.GetProvider(), c.GetService(), providers.Config{
				Concurrency: int(cfg.GetConcurrency()),
				QueueLimit:  int(cfg.GetQueueLimit()),
				Units:       int(cfg.GetUnits()),
				MemoryMB:    int(cfg.GetMemoryMb()),
				StorageGB:   cfg.GetStorageGb(),
				HitRatio:    hit,
			})
			if err != nil {
				return sim.Architecture{}, nil, fmt.Errorf("component %q: %w", c.GetId(), err)
			}
			// User-specified generic fields win over catalog defaults.
			if spec.Concurrency > 0 {
				rs.Spec.Concurrency = spec.Concurrency
			}
			if spec.QueueLimit > 0 {
				rs.Spec.QueueLimit = spec.QueueLimit
			}
			if spec.CapacityRPS > 0 {
				rs.Spec.CapacityRPS = spec.CapacityRPS
			}
			if spec.DefaultServiceTimeMillis > 0 {
				rs.Spec.DefaultServiceTimeMillis = spec.DefaultServiceTimeMillis
			}
			if len(spec.ServiceTimeMillis) > 0 {
				rs.Spec.ServiceTimeMillis = spec.ServiceTimeMillis
			}
			out.Components = append(out.Components, rs.Spec)
			resolved = append(resolved, rs)
			continue
		}
		out.Components = append(out.Components, spec)
	}
	for _, l := range pb.GetLinks() {
		out.Links = append(out.Links, sim.Link{
			From:      l.GetFrom(),
			To:        l.GetTo(),
			Condition: l.GetCondition(),
		})
	}
	return out, resolved, nil
}

// protoWorkloadToDomain converts the workload spec.
func protoWorkloadToDomain(pb *v1.WorkloadSpec) workload.Spec {
	return workload.Spec{
		TotalUsers:            pb.GetTotalUsers(),
		DAU:                   pb.GetDau(),
		RequestsPerUserPerDay: pb.GetRequestsPerUserPerDay(),
		PeakMultiplier:        pb.GetPeakMultiplier(),
		ReadWriteRatio:        pb.GetReadWriteRatio(),
		PayloadBytes:          pb.GetPayloadBytes(),
	}
}

// protoOptionsToDomain converts run options, defaulting seed/horizon the
// same way sim.Simulate does (the defaults are echoed back to callers).
func protoOptionsToDomain(pb *v1.SimulationOptions) sim.Options {
	opts := sim.Options{
		Seed:       pb.GetSeed(),
		DurationMS: pb.GetDurationMs(),
		Retry: sim.RetryPolicy{
			MaxRetries:    int(pb.GetMaxRetries()),
			BackoffBaseMS: pb.GetBackoffBaseMs(),
			TimeoutMS:     pb.GetTimeoutMs(),
		},
		RetryOn: append([]string(nil), pb.GetRetryOn()...),
	}
	for _, f := range pb.GetFailures() {
		opts.Failures = append(opts.Failures, sim.Failure{
			Target:     f.GetTarget(),
			Type:       sim.FailureType(protoFailureType(f.GetType())),
			StartMS:    f.GetStartMs(),
			DurationMS: f.GetDurationMs(),
			Config: sim.FailureConfig{
				AddedLatencyMillis: f.GetConfig().GetAddedLatencyMillis(),
				ErrorRate:          f.GetConfig().GetErrorRate(),
				PacketLossRate:     f.GetConfig().GetPacketLossRate(),
				PassThrough:        f.GetConfig().GetPassThrough(),
			},
		})
	}
	return opts
}

func protoFailureType(t v1.FailureType) string {
	switch t {
	case v1.FailureType_FAILURE_TYPE_CRASH:
		return string(sim.FailureCrash)
	case v1.FailureType_FAILURE_TYPE_INCREASED_LATENCY:
		return string(sim.FailureLatency)
	case v1.FailureType_FAILURE_TYPE_INCREASED_ERROR_RATE:
		return string(sim.FailureErrorRate)
	case v1.FailureType_FAILURE_TYPE_NETWORK_FAILURE:
		return string(sim.FailureNetwork)
	default:
		return ""
	}
}

// domainPlanToProto converts a derived workload plan.
func domainPlanToProto(p workload.Plan) *v1.LoadPlan {
	return &v1.LoadPlan{
		TotalUsers:             p.TotalUsers,
		Dau:                    p.DAU,
		DauFraction:            p.DAUFraction,
		RequestsPerUserPerDay:  p.RequestsPerUserPerDay,
		RequestsPerDay:         p.RequestsPerDay,
		AverageRps:             p.AverageRPS,
		PeakMultiplier:         p.PeakMultiplier,
		PeakRps:                p.PeakRPS,
		ReadFraction:           p.ReadFraction,
		WriteFraction:          p.WriteFraction,
		PayloadBytes:           p.PayloadBytes,
		MeanInterArrivalMillis: p.MeanInterArrivalMillis,
	}
}

// metricsToProto converts system + per-component metrics.
func metricsToProto(m sim.Metrics) *v1.SystemMetrics {
	out := &v1.SystemMetrics{
		DurationMs:   m.DurationMS,
		Generated:    m.Generated,
		Completed:    m.Completed,
		Rejected:     m.Rejected,
		Failed:       m.Failed,
		Timeouts:     m.Timeouts,
		Dropped:      m.Dropped,
		InFlight:     m.InFlight,
		ErrorRate:    m.ErrorRate,
		TimeoutRate:  m.TimeoutRate,
		AvgLatencyMs: m.AvgLatencyMS,
		P50Ms:        m.P50MS,
		P95Ms:        m.P95MS,
		P99Ms:        m.P99MS,
		MaxLatencyMs: m.MaxLatencyMS,
		Components:   make([]*v1.ComponentMetrics, 0, len(m.Components)),
	}
	for _, c := range m.Components {
		out.Components = append(out.Components, &v1.ComponentMetrics{
			Id:             c.ID,
			Kind:           string(c.Kind),
			Arrived:        c.Arrived,
			Completed:      c.Completed,
			Rejected:       c.Rejected,
			Failed:         c.Failed,
			QueueDepth:     int32(c.QueueDepth),
			MaxQueueDepth:  int32(c.MaxQueueDepth),
			InFlight:       int32(c.InFlight),
			Utilization:    c.Utilization,
			AvgQueueWaitMs: c.AvgQueueWaitMS,
			AvgServiceMs:   c.AvgServiceMS,
			ThroughputRps:  c.ThroughputRPS,
			ArrivalRps:     c.ArrivalRPS,
			CapacityRps:    c.CapacityRPS,
			QueueTrend:     c.QueueTrend,
			Saturated:      c.Saturated,
		})
	}
	return out
}

// failureRecordsToProto converts the per-request failure log.
func failureRecordsToProto(recs []sim.FailureRecord) []*v1.FailureRecord {
	out := make([]*v1.FailureRecord, 0, len(recs))
	for _, f := range recs {
		out = append(out, &v1.FailureRecord{
			RequestId:   f.RequestID,
			ComponentId: f.ComponentID,
			CallerId:    f.CallerID,
			Kind:        f.Kind,
			Attempt:     int32(f.Attempt),
			AtMs:        f.AtMS,
			LatencyMs:   f.LatencyMS,
			Path:        append([]string(nil), f.Path...),
		})
	}
	return out
}

// diagnosisToProto converts the bottleneck report.
func diagnosisToProto(d sim.Diagnosis) *v1.Diagnosis {
	out := &v1.Diagnosis{
		Bottlenecks: make([]*v1.Bottleneck, 0, len(d.Bottlenecks)),
		Impacts:     append([]string(nil), d.Impacts...),
		Healthy:     d.Healthy,
		Summary:     d.Summary,
	}
	for _, b := range d.Bottlenecks {
		out.Bottlenecks = append(out.Bottlenecks, &v1.Bottleneck{
			ComponentId: b.ComponentID,
			Kind:        string(b.Kind),
			Severity:    b.Severity,
			Reasons:     append([]string(nil), b.Reasons...),
			Impacts:     append([]string(nil), b.Impacts...),
		})
	}
	return out
}

// capacityToProto converts provider capacity reports.
func capacityToProto(reports []providers.CapacityReport) []*v1.CapacityReport {
	out := make([]*v1.CapacityReport, 0, len(reports))
	for _, r := range reports {
		out = append(out, &v1.CapacityReport{
			ComponentId:       r.ComponentID,
			Provider:          r.Provider,
			Service:           r.Service,
			CurrentRps:        r.CurrentRPS,
			MaxSustainableRps: r.MaxSustainableRPS,
			Utilization:       r.Utilization,
			Headroom:          r.Headroom,
			Saturated:         r.Saturated,
			Bottleneck:        r.Bottleneck,
			Assumptions:       append([]string(nil), r.Assumptions...),
		})
	}
	return out
}

// costToProto converts the monthly cost estimate.
func costToProto(est *providers.CostEstimate) *v1.CostEstimate {
	if est == nil {
		return nil
	}
	out := &v1.CostEstimate{
		Currency:    est.Currency,
		Total:       est.Total,
		ByCategory:  map[string]float64{},
		Assumptions: append([]string(nil), est.Assumptions...),
	}
	for cat, amt := range est.ByCategory {
		out.ByCategory[string(cat)] = amt
	}
	for _, cc := range est.Components {
		pbcc := &v1.ComponentCost{
			ComponentId: cc.ComponentID,
			Provider:    cc.Provider,
			Service:     cc.Service,
			Monthly:     cc.Monthly,
			LineItems:   make([]*v1.CostLineItem, 0, len(cc.LineItems)),
		}
		for _, li := range cc.LineItems {
			pbcc.LineItems = append(pbcc.LineItems, &v1.CostLineItem{
				ComponentId: li.ComponentID,
				Category:    string(li.Category),
				Description: li.Description,
				Quantity:    li.Quantity,
				Unit:        li.Unit,
				UnitPrice:   li.UnitPrice,
				MonthlyCost: li.MonthlyCost,
			})
		}
		out.Components = append(out.Components, pbcc)
	}
	return out
}

// runSummaryToProto converts engine-level counters.
func runSummaryToProto(r *engine.Results) *v1.RunSummary {
	return &v1.RunSummary{
		EventsProcessed: r.Processed,
		EventsScheduled: r.Scheduled,
		EventsPending:   int32(r.Pending),
		StopReason:      string(r.StopReason),
	}
}

// snapshotToProto converts a mid-run progress snapshot.
func snapshotToProto(s sim.Snapshot) *v1.ProgressSnapshot {
	out := &v1.ProgressSnapshot{
		SimTimeMs:       s.SimTimeMS,
		Generated:       s.Generated,
		Completed:       s.Completed,
		Rejected:        s.Rejected,
		Failed:          s.Failed,
		InFlight:        s.InFlight,
		WallElapsedMs:   s.WallElapsedMS,
		EventsProcessed: s.EventsProcessed,
		EventsPending:   s.EventsPending,
		Components: make([]*v1.ComponentOccupancy, 0,
			len(s.Components)),
	}
	for _, c := range s.Components {
		out.Components = append(out.Components, &v1.ComponentOccupancy{
			ComponentId: c.ID,
			QueueDepth:  int32(c.QueueDepth),
			InFlight:    int32(c.InFlight),
			Arrived:     c.Arrived,
			Completed:   c.Completed,
			Utilization: c.Utilization,
		})
	}
	return out
}
