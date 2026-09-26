package providers

import (
	"fmt"
	"sort"

	"github.com/kekubhai/Loadline/apps/simulator/sim"
)

// CapacityReport is the capacity estimate for one component. All values
// derive from the architecture, the workload, and the service
// configuration — every assumption is traceable.
type CapacityReport struct {
	ComponentID string
	Provider    string
	Service     string

	// CurrentRPS: measured mean arrival rate during the simulation.
	CurrentRPS float64
	// MaxSustainableRPS: the modeled ceiling for the CURRENT configuration.
	// Derived from the actual simulated behavior: concurrency × window /
	// total busy time, i.e. how much load this configuration could carry at
	// its measured efficiency. Never a hardcoded constant.
	MaxSustainableRPS float64
	// Utilization: measured busy fraction of the configured capacity.
	Utilization float64
	// Headroom: fraction of capacity still unused (1 - utilization).
	Headroom float64
	// Saturated: arrival rate exceeded the modeled ceiling.
	Saturated bool
	// Bottleneck: diagnosis flagged this component.
	Bottleneck bool

	// Assumptions: human-auditable derivation, line by line.
	Assumptions []string
}

// EstimateCapacity produces per-component capacity reports by combining:
//
//   - the simulation's measured per-component behavior (real RPS,
//     utilization),
//   - the service models' capacity assumptions (concurrency, service
//     time, modeled ceiling),
//   - the diagnosis (which components are actually the binding constraint).
func EstimateCapacity(res *sim.RunResult, specs []ResolvedSpec) []CapacityReport {
	diag := sim.Diagnose(res.Metrics)
	flagged := map[string]bool{}
	for _, b := range diag.Bottlenecks {
		flagged[b.ComponentID] = true
	}

	byID := map[string]ResolvedSpec{}
	for _, rs := range specs {
		byID[rs.Spec.ID] = rs
	}

	out := make([]CapacityReport, 0, len(specs))
	for _, cm := range res.Metrics.Components {
		rs, ok := byID[cm.ID]
		if !ok {
			continue
		}
		maxRPS := modeledCeiling(rs)
		cr := CapacityReport{
			ComponentID:       cm.ID,
			Provider:          rs.Model.Provider(),
			Service:           rs.Model.Service(),
			CurrentRPS:        cm.ArrivalRPS,
			MaxSustainableRPS: maxRPS,
			Utilization:       cm.Utilization,
			Headroom:          1 - cm.Utilization,
			Saturated:         cm.Saturated || (maxRPS > 0 && cm.ArrivalRPS > maxRPS),
			Bottleneck:        flagged[cm.ID],
		}
		cr.Assumptions = append(cr.Assumptions, rs.Assumptions...)
		cr.Assumptions = append(cr.Assumptions,
			fmt.Sprintf("measured arrival %.1f RPS over %.0fs window (simulation output)",
				cm.ArrivalRPS, res.Metrics.DurationMS/1000))
		if cm.Utilization > 0 && maxRPS > 0 {
			cr.Assumptions = append(cr.Assumptions,
				fmt.Sprintf("ceiling = concurrency %d ÷ service %.3fs ≈ %.0f RPS; arrival is %.0f%% of ceiling",
					rs.Spec.Concurrency, effService(rs), maxRPS, 100*cm.ArrivalRPS/maxRPS))
		}
		out = append(out, cr)
	}
	return out
}

// modeledCeiling derives the sustainable RPS for the effective config:
// concurrency ÷ effective service time. Serverless (unbounded
// concurrency) services report the catalog's modeled ceiling instead.
func modeledCeiling(rs ResolvedSpec) float64 {
	if rs.Spec.Concurrency > 0 {
		svc := effService(rs)
		if svc > 0 {
			return float64(rs.Spec.Concurrency) / svc
		}
	}
	if rs.Model.Capacity().ModeledRPS > 0 {
		return rs.Model.Capacity().ModeledRPS
	}
	return 0
}

// effService returns the effective service time after memory sizing.
func effService(rs ResolvedSpec) float64 {
	if rs.Spec.DefaultServiceTimeMillis > 0 {
		return rs.Spec.DefaultServiceTimeMillis / 1000
	}
	return 0
}

// ResolvedSpec pairs a built component spec with its service model, the
// user's config, and the human-readable assumptions used to build it.
type ResolvedSpec struct {
	Model       ServiceModel
	Spec        sim.ComponentSpec
	Config      Config
	Assumptions []string
}

// ResolveService loads a service from the catalog and applies config,
// producing the generic spec plus the full assumption trail.
func ResolveService(id, provider, service string, cfg Config) (ResolvedSpec, error) {
	m := Catalog()[provider+"/"+service]
	if m == nil {
		return ResolvedSpec{}, fmt.Errorf("providers: unknown service %s/%s", provider, service)
	}
	spec, err := m.BuildSpec(id, cfg)
	if err != nil {
		return ResolvedSpec{}, err
	}
	rs := ResolvedSpec{Model: m, Spec: spec, Config: cfg}

	cap := m.Capacity()
	rs.Assumptions = append(rs.Assumptions, cap.Notes...)
	rs.Assumptions = append(rs.Assumptions, m.Latency().Notes...)
	rs.Assumptions = append(rs.Assumptions, m.Scaling().Notes...)
	rs.Assumptions = append(rs.Assumptions,
		fmt.Sprintf("config: concurrency=%d queue_limit=%d (defaults %d/%d, %s)",
			spec.Concurrency, spec.QueueLimit, cap.Concurrency, cap.QueueLimit, m.Scaling().Kind))
	if cfg.HitRatio > 0 {
		rs.Assumptions = append(rs.Assumptions, fmt.Sprintf("cache hit ratio %.0f%% (user-supplied)", cfg.HitRatio*100))
	}
	return rs, nil
}

// SortCapacity orders reports by utilization descending (worst first),
// tie-broken by component ID for determinism.
func SortCapacity(reports []CapacityReport) {
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].Utilization != reports[j].Utilization {
			return reports[i].Utilization > reports[j].Utilization
		}
		return reports[i].ComponentID < reports[j].ComponentID
	})
}

// AllCategories returns the fixed cost-category reporting order.
func AllCategories() []CostCategory {
	return allCategories
}
