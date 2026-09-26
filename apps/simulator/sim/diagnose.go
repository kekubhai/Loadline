package sim

import (
	"fmt"
	"sort"
)

// Bottleneck is one component flagged by diagnosis, with the evidence and
// the observed system impacts. Every string is derived from metrics —
// nothing is hardcoded per component kind.
type Bottleneck struct {
	ComponentID string
	Kind        ComponentKind
	Severity    string // "critical", "high", "moderate"
	Reasons     []string
	Impacts     []string
}

// Diagnosis is the automatic bottleneck report for one run.
type Diagnosis struct {
	Bottlenecks []Bottleneck // ordered: critical first, then by arrival rate
	Impacts     []string     // system-level impacts observed in this run
	Healthy     bool         // true when no component was flagged
	Summary     string
}

// Diagnose analyzes one run's metrics and flags bottlenecks. Signals, in
// decreasing order of severity:
//
//   - rejections:            admission capacity exhausted (critical)
//   - saturated:             arrival rate above modeled capacity (critical)
//   - queue growing:         arrival rate exceeds service rate (critical)
//   - utilization >= 97%:    at capacity (critical); >= 90% high; >= 75% moderate
//   - queueing dominates:    wait time far above service time (high)
//   - failures at component: timeouts/errors/drops observed (high)
func Diagnose(m Metrics) Diagnosis {
	return diagnose(m, nil)
}

// DiagnoseWithBaseline compares a run against a baseline run (typically
// the same architecture without failures) and adds delta impacts such as
// "p95 latency increased 340% vs baseline".
func DiagnoseWithBaseline(m, baseline Metrics) Diagnosis {
	return diagnose(m, &baseline)
}

func diagnose(m Metrics, baseline *Metrics) Diagnosis {
	var out []Bottleneck

	for _, c := range m.Components {
		if c.Kind == KindClient {
			continue
		}
		var reasons []string
		sev := 0

		if c.Rejected > 0 {
			pct := 100 * float64(c.Rejected) / float64(max64(1, float64(c.Arrived)))
			reasons = append(reasons,
				fmt.Sprintf("rejected %d of %d arrivals (%.1f%%) — admission capacity exhausted",
					c.Rejected, c.Arrived, pct))
			sev = 3
		}
		if c.Saturated {
			reasons = append(reasons,
				fmt.Sprintf("incoming load %.1f RPS exceeds modeled capacity %.1f RPS",
					c.ArrivalRPS, c.CapacityRPS))
			sev = 3
		}
		switch {
		case c.Utilization >= 0.97:
			reasons = append(reasons,
				fmt.Sprintf("utilization %.0f%% — running at capacity", c.Utilization*100))
			sev = maxInt(sev, 3)
		case c.Utilization >= 0.90:
			reasons = append(reasons,
				fmt.Sprintf("utilization %.0f%% — near capacity", c.Utilization*100))
			sev = maxInt(sev, 2)
		case c.Utilization >= 0.75:
			reasons = append(reasons,
				fmt.Sprintf("utilization %.0f%% — elevated", c.Utilization*100))
			sev = maxInt(sev, 1)
		}
		if c.QueueTrend == "growing" && c.MaxQueueDepth >= 5 {
			reasons = append(reasons,
				fmt.Sprintf("queue growing continuously (max depth %d, avg wait %.1fms) — arrival rate exceeds service rate",
					c.MaxQueueDepth, c.AvgQueueWaitMS))
			sev = maxInt(sev, 3)
		}
		if c.AvgServiceMS > 0 && c.AvgQueueWaitMS > 2*c.AvgServiceMS {
			reasons = append(reasons,
				fmt.Sprintf("queueing dominates: requests wait %.1fms vs %.1fms service time",
					c.AvgQueueWaitMS, c.AvgServiceMS))
			sev = maxInt(sev, 2)
		}
		if c.Failed > 0 {
			reasons = append(reasons,
				fmt.Sprintf("%d requests failed here (timeouts, injected errors, drops)", c.Failed))
			sev = maxInt(sev, 2)
		}

		if sev == 0 {
			continue
		}
		out = append(out, Bottleneck{
			ComponentID: c.ID,
			Kind:        c.Kind,
			Severity:    severityLabel(sev),
			Reasons:     reasons,
		})
	}

	// Deterministic order: severity desc, then arrival rate desc, then ID.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return severityRank(out[i].Severity) > severityRank(out[j].Severity)
		}
		ci, cj := findComponent(m, out[i].ComponentID), findComponent(m, out[j].ComponentID)
		if ci.ArrivalRPS != cj.ArrivalRPS {
			return ci.ArrivalRPS > cj.ArrivalRPS
		}
		return out[i].ComponentID < out[j].ComponentID
	})

	// System-level impacts: attached to every flagged component AND to the
	// diagnosis itself (deltas can matter even without a flagged component).
	impacts := systemImpacts(m, baseline)
	for i := range out {
		out[i].Impacts = impacts
	}

	d := Diagnosis{Bottlenecks: out, Impacts: impacts, Healthy: len(out) == 0}
	if d.Healthy {
		d.Summary = "no bottlenecks detected"
	} else {
		d.Summary = fmt.Sprintf("%d bottleneck(s); primary: %s (%s)",
			len(out), out[0].ComponentID, out[0].Severity)
	}
	return d
}

// systemImpacts derives observed system effects from the run's own
// metrics, plus deltas when a baseline is provided.
func systemImpacts(m Metrics, baseline *Metrics) []string {
	var out []string
	if m.Rejected > 0 {
		out = append(out, fmt.Sprintf("load shedding active: %d requests rejected", m.Rejected))
	}
	if m.Failed > 0 {
		out = append(out, fmt.Sprintf("error rate %.2f%% (%d failed requests)",
			m.ErrorRate*100, m.Failed))
	}
	if m.Timeouts > 0 {
		out = append(out, fmt.Sprintf("timeout rate %.2f%% (%d timeouts)",
			m.TimeoutRate*100, m.Timeouts))
	}
	if m.P50MS > 0 && m.P99MS > 3*m.P50MS {
		out = append(out, fmt.Sprintf("heavy tail: p99 %.1fms is %.1fx p50 (%.1fms)",
			m.P99MS, m.P99MS/m.P50MS, m.P50MS))
	}
	if m.InFlight > 0 {
		out = append(out, fmt.Sprintf("%d requests still in flight at horizon", m.InFlight))
	}
	if baseline != nil {
		if baseline.P95MS > 0 {
			delta := 100 * (m.P95MS - baseline.P95MS) / baseline.P95MS
			if delta > 5 {
				out = append(out, fmt.Sprintf("p95 latency increased %.0f%% vs baseline (%.1fms → %.1fms)",
					delta, baseline.P95MS, m.P95MS))
			}
		}
		if baseline.P50MS > 0 {
			delta := 100 * (m.P50MS - baseline.P50MS) / baseline.P50MS
			if delta > 5 {
				out = append(out, fmt.Sprintf("p50 latency increased %.0f%% vs baseline (%.1fms → %.1fms)",
					delta, baseline.P50MS, m.P50MS))
			}
		}
		if baseline.Completed > 0 {
			delta := 100 * (float64(m.Completed) - float64(baseline.Completed)) / float64(baseline.Completed)
			if delta < -5 {
				out = append(out, fmt.Sprintf("throughput decreased %.0f%% vs baseline (%d → %d completed)",
					-delta, baseline.Completed, m.Completed))
			}
		}
		if m.ErrorRate > baseline.ErrorRate+0.005 {
			out = append(out, fmt.Sprintf("error rate increased vs baseline (%.2f%% → %.2f%%)",
				baseline.ErrorRate*100, m.ErrorRate*100))
		}
	}
	if len(out) == 0 {
		out = append(out, "no system-level degradation observed")
	}
	return out
}

func findComponent(m Metrics, id string) ComponentMetrics {
	for _, c := range m.Components {
		if c.ID == id {
			return c
		}
	}
	return ComponentMetrics{}
}

func severityLabel(sev int) string {
	switch sev {
	case 3:
		return "critical"
	case 2:
		return "high"
	default:
		return "moderate"
	}
}

func severityRank(label string) int {
	switch label {
	case "critical":
		return 3
	case "high":
		return 2
	default:
		return 1
	}
}

func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
