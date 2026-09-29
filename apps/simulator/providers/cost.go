package providers

import (
	"fmt"
	"sort"

	"github.com/kekubhai/Loadline/apps/simulator/sim"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// CostCategory buckets the monthly estimate.
type CostCategory string

const (
	CostCompute  CostCategory = "compute"
	CostRequests CostCategory = "requests"
	CostStorage  CostCategory = "storage"
	CostDatabase CostCategory = "database"
	CostCache    CostCategory = "cache"
	CostQueue    CostCategory = "queue"
	CostNetwork  CostCategory = "network"
)

// allCategories is the fixed reporting order.
var allCategories = []CostCategory{
	CostCompute, CostRequests, CostStorage, CostDatabase,
	CostCache, CostQueue, CostNetwork,
}

// LineItem is one priced element with its derivation.
type LineItem struct {
	ComponentID string
	Category    CostCategory
	Description string
	Quantity    float64 // usage amount
	Unit        string  // "requests", "GB-month", "instance-hours", ...
	UnitPrice   float64
	MonthlyCost float64
}

// ComponentCost is one component's monthly estimate.
type ComponentCost struct {
	ComponentID string
	Provider    string
	Service     string
	LineItems   []LineItem
	Monthly     float64
}

// CostEstimate is the full monthly estimate. Every number carries the
// marker that it is an ESTIMATE from local pricing models — never live
// billing data.
type CostEstimate struct {
	Currency   string
	ESTIMATE   bool // always true; documentation in metal
	ByCategory map[CostCategory]float64
	Total      float64
	Components []ComponentCost
	// Assumptions: the full derivation trail (workload scaling, pricing
	// plan notes, free-tier deductions).
	Assumptions []string
}

// HoursPerMonth: 730h is the standard monthly instance-hour basis.
const HoursPerMonth = 730.0

// EstimateCost builds the monthly cost estimate from simulation outputs
// and the workload plan. Usage is scaled from the simulated window to a
// month at the observed average rate — the assumption trail states this
// explicitly.
func EstimateCost(res *sim.RunResult, specs []ResolvedSpec, plan workload.Plan) *CostEstimate {
	est := &CostEstimate{
		Currency:   "USD",
		ESTIMATE:   true,
		ByCategory: map[CostCategory]float64{},
	}

	windowSec := res.Metrics.DurationMS / 1000
	if windowSec <= 0 {
		windowSec = 1
	}
	scaleToMonth := HoursPerMonth * 3600 / windowSec

	est.Assumptions = append(est.Assumptions,
		fmt.Sprintf("usage scaled from %.0fs simulated window to %.0fh month at observed average rates (x%.1f)",
			windowSec, HoursPerMonth, scaleToMonth))
	est.Assumptions = append(est.Assumptions,
		fmt.Sprintf("workload basis: %d DAU × %.0f req/user/day = %.0f req/day",
			plan.DAU, plan.RequestsPerUserPerDay, plan.RequestsPerDay))
	est.Assumptions = append(est.Assumptions,
		"prices are LOCAL ESTIMATES for mid-tier plans, primary commercial regions; not live billing data")

	for _, rs := range specs {
		cc := ComponentCost{
			ComponentID: rs.Spec.ID,
			Provider:    rs.Model.Provider(),
			Service:     rs.Model.Service(),
		}
		pm := rs.Model.Pricing()
		sm := rs.Model.Scaling()
		cm := findComponent(res, rs.Spec.ID)

		// --- Compute: instance-hours ---
		if pm.PerInstanceHour > 0 {
			units := float64(maxInt(1, sm.Units))
			q := units * HoursPerMonth
			cost := q * pm.PerInstanceHour
			cc.add(LineItem{
				ComponentID: rs.Spec.ID, Category: CostCompute,
				Description: fmt.Sprintf("%d × instance-hour (%s)", int(units), sm.Kind),
				Quantity:    q, Unit: "instance-hours", UnitPrice: pm.PerInstanceHour, MonthlyCost: cost,
			})
		}

		// --- Compute: per-compute-unit (GB-second class) ---
		if pm.PerComputeUnit > 0 && cm.Completed > 0 {
			memGB := 0.5
			if rs.Config.MemoryMB > 0 {
				memGB = float64(rs.Config.MemoryMB) / 1024
			}
			avgSvcSec := avgServiceSeconds(rs, cm)
			q := float64(cm.Completed) * scaleToMonth
			gbS := q * memGB * avgSvcSec
			cost := gbS * pm.PerComputeUnit
			cc.add(LineItem{
				ComponentID: rs.Spec.ID, Category: CostCompute,
				Description: fmt.Sprintf("%.0fM monthly executions × %.1fGB × %.3fs (GB-s)",
					q/1e6, memGB, avgSvcSec),
				Quantity: gbS, Unit: "GB-seconds", UnitPrice: pm.PerComputeUnit, MonthlyCost: cost,
			})
		}

		// --- Requests ---
		if pm.PerRequest > 0 && cm.Completed > 0 {
			q := float64(cm.Completed) * scaleToMonth
			cost := q * pm.PerRequest
			cc.add(LineItem{
				ComponentID: rs.Spec.ID, Category: CostRequests,
				Description: fmt.Sprintf("%.1fM monthly requests", q/1e6),
				Quantity:    q, Unit: "requests", UnitPrice: pm.PerRequest, MonthlyCost: cost,
			})
		}

		// --- Storage (object storage) ---
		if pm.StorageGBMonth > 0 && isStorage(rs) {
			gb := rs.Config.StorageGB
			if gb <= 0 {
				gb = 100 // documented default assumption
			}
			q := gb
			cost := q * pm.StorageGBMonth
			cc.add(LineItem{
				ComponentID: rs.Spec.ID, Category: CostStorage,
				Description: fmt.Sprintf("%.0f GB stored (default assumption)", gb),
				Quantity:    q, Unit: "GB-month", UnitPrice: pm.StorageGBMonth, MonthlyCost: cost,
			})
		}

		// --- Database ---
		if pm.DatabaseGBMonth > 0 {
			gb := rs.Config.StorageGB
			if gb <= 0 {
				gb = 50 // documented default assumption
			}
			cost := gb * pm.DatabaseGBMonth
			cc.add(LineItem{
				ComponentID: rs.Spec.ID, Category: CostDatabase,
				Description: fmt.Sprintf("%.0f GB database storage (default assumption)", gb),
				Quantity:    gb, Unit: "GB-month", UnitPrice: pm.DatabaseGBMonth, MonthlyCost: cost,
			})
		}

		// --- Cache ---
		if pm.CacheGBMonth > 0 {
			gb := rs.Config.StorageGB
			if gb <= 0 {
				gb = 13 // ElastiCache-class default node memory
			}
			cost := gb * pm.CacheGBMonth
			cc.add(LineItem{
				ComponentID: rs.Spec.ID, Category: CostCache,
				Description: fmt.Sprintf("%.0f GB cache memory (default assumption)", gb),
				Quantity:    gb, Unit: "GB-month", UnitPrice: pm.CacheGBMonth, MonthlyCost: cost,
			})
		}

		// --- Queue ---
		if pm.QueueOp > 0 && cm.Completed > 0 {
			q := float64(cm.Completed) * scaleToMonth
			cost := q * pm.QueueOp
			cc.add(LineItem{
				ComponentID: rs.Spec.ID, Category: CostQueue,
				Description: fmt.Sprintf("%.1fM monthly queue operations", q/1e6),
				Quantity:    q, Unit: "operations", UnitPrice: pm.QueueOp, MonthlyCost: cost,
			})
		}

		// --- Network (egress) ---
		if pm.EgressGB > 0 && cm.Completed > 0 && res.Plan.PayloadBytes > 0 {
			// Assumption: 20% of responses egress to the internet (cache
			// hits at CDN/edge don't double-bill the origin).
			egressFrac := 0.2
			gb := float64(cm.Completed) * scaleToMonth * float64(res.Plan.PayloadBytes) * egressFrac / (1 << 30)
			cost := gb * pm.EgressGB
			cc.add(LineItem{
				ComponentID: rs.Spec.ID, Category: CostNetwork,
				Description: fmt.Sprintf("%.0f GB egress (20%% of %.0fB responses × %dB)",
					gb, float64(cm.Completed)*scaleToMonth, res.Plan.PayloadBytes),
				Quantity: gb, Unit: "GB", UnitPrice: pm.EgressGB, MonthlyCost: cost,
			})
		}

		cc.Monthly = 0
		for _, li := range cc.LineItems {
			cc.Monthly += li.MonthlyCost
		}
		for _, li := range cc.LineItems {
			est.ByCategory[li.Category] += li.MonthlyCost
		}
		est.Total += cc.Monthly
		if len(cc.LineItems) > 0 {
			est.Components = append(est.Components, cc)
		}
	}

	// Free-tier deductions (documented, applied to categories globally).
	applyFreeTier(est, specs)

	// Recompute totals after deductions so every roll-up agrees:
	// component.Monthly, ByCategory, and Total all reflect the deducted
	// line items (before this, components kept pre-deduction sums).
	est.Total = 0
	for i := range est.Components {
		cc := &est.Components[i]
		cc.Monthly = 0
		for _, li := range cc.LineItems {
			cc.Monthly += li.MonthlyCost
		}
		est.Total += cc.Monthly
	}

	// Deterministic component order.
	sort.Slice(est.Components, func(i, j int) bool {
		return est.Components[i].ComponentID < est.Components[j].ComponentID
	})
	return est
}

// applyFreeTier deducts monthly free allowances per component line items,
// deducting from the most expensive line of the matching usage kind first.
func applyFreeTier(est *CostEstimate, specs []ResolvedSpec) {
	for _, rs := range specs {
		ft := rs.Model.Pricing().FreeTier
		if ft == (FreeTier{}) {
			continue
		}
		for ci := range est.Components {
			cc := &est.Components[ci]
			if cc.ComponentID != rs.Spec.ID {
				continue
			}
			for li := range cc.LineItems {
				l := &cc.LineItems[li]
				switch l.Unit {
				case "requests":
					if ft.Requests > 0 && l.Quantity > 0 {
						deduct := l.MonthlyCost * (minFloat(ft.Requests, l.Quantity) / l.Quantity)
						l.Description += fmt.Sprintf(" (first %.0fM free)", ft.Requests/1e6)
						l.MonthlyCost -= deduct
						est.ByCategory[l.Category] -= deduct
					}
				case "GB-seconds":
					if ft.ComputeUnits > 0 && l.Quantity > 0 {
						deduct := l.MonthlyCost * (minFloat(ft.ComputeUnits, l.Quantity) / l.Quantity)
						l.Description += fmt.Sprintf(" (first %.0f GB-s free)", ft.ComputeUnits)
						l.MonthlyCost -= deduct
						est.ByCategory[l.Category] -= deduct
					}
				case "GB-month":
					if l.Category == CostStorage && ft.StorageGB > 0 && l.Quantity > 0 {
						deduct := l.MonthlyCost * (minFloat(ft.StorageGB, l.Quantity) / l.Quantity)
						l.Description += fmt.Sprintf(" (first %.0fGB free)", ft.StorageGB)
						l.MonthlyCost -= deduct
						est.ByCategory[l.Category] -= deduct
					}
				case "instance-hours":
					if ft.InstanceHours > 0 && l.Quantity > 0 {
						deduct := l.MonthlyCost * (minFloat(ft.InstanceHours, l.Quantity) / l.Quantity)
						l.Description += fmt.Sprintf(" (first %.0f h free)", ft.InstanceHours)
						l.MonthlyCost -= deduct
						est.ByCategory[l.Category] -= deduct
					}
				case "operations":
					if ft.QueueOps > 0 && l.Quantity > 0 {
						deduct := l.MonthlyCost * (minFloat(ft.QueueOps, l.Quantity) / l.Quantity)
						l.Description += fmt.Sprintf(" (first %.0fM free)", ft.QueueOps/1e6)
						l.MonthlyCost -= deduct
						est.ByCategory[l.Category] -= deduct
					}
				case "GB":
					if l.Category == CostNetwork && ft.EgressGB > 0 && l.Quantity > 0 {
						deduct := l.MonthlyCost * (minFloat(ft.EgressGB, l.Quantity) / l.Quantity)
						l.Description += fmt.Sprintf(" (first %.0fGB free)", ft.EgressGB)
						l.MonthlyCost -= deduct
						est.ByCategory[l.Category] -= deduct
					}
				}
			}
		}
	}
}

// findComponent fetches a component's metrics by ID.
func findComponent(res *sim.RunResult, id string) sim.ComponentMetrics {
	for _, c := range res.Metrics.Components {
		if c.ID == id {
			return c
		}
	}
	return sim.ComponentMetrics{}
}

// avgServiceSeconds derives the mean per-request compute duration from
// measured simulation data (busy time ÷ completions).
func avgServiceSeconds(rs ResolvedSpec, cm sim.ComponentMetrics) float64 {
	if cm.Completed > 0 && cm.AvgServiceMS > 0 {
		return cm.AvgServiceMS / 1000
	}
	if rs.Spec.DefaultServiceTimeMillis > 0 {
		return rs.Spec.DefaultServiceTimeMillis / 1000
	}
	return 0.05 // conservative fallback, documented in line item
}

// isStorage reports whether the service is object-storage kind.
func isStorage(rs ResolvedSpec) bool {
	return rs.Spec.Kind == sim.KindObjectStorage
}

func (cc *ComponentCost) add(li LineItem) { cc.LineItems = append(cc.LineItems, li) }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
