// Command simulator is the executable example for LOADLINE's simulation
// stack: engine + workload + components + failure injection + diagnosis.
//
// Scenario: a Client → API → Cache → DB architecture under a 1M-user
// workload. Run 1 is the healthy baseline. Run 2 injects a cache crash
// (with pass-through failover to the origin) for 15 seconds in the middle
// of the run, with retry/timeout behavior configured. The cascade — extra
// DB load, longer queues, rising latency, timeouts — EMERGES from the
// component interactions; nothing about it is hardcoded. Both runs are
// then analyzed by the automatic bottleneck diagnosis.
//
// Everything runs in simulated time: no wall-clock sleeps, no network, no
// external services.
package main

import (
	"fmt"
	"os"

	"github.com/kekubhai/Loadline/apps/simulator/sim"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

const seed = 42

func main() {
	// Peak-load scenario (~1157 RPS): heavy enough that losing the cache
	// pushes the database past its real service capacity — the full
	// cascade emerges and the diagnosis has something to find.
	spec := workload.Spec{
		TotalUsers:            1_000_000,
		DAU:                   500_000,
		RequestsPerUserPerDay: 40,
		PeakMultiplier:        5,
		ReadWriteRatio:        4, // 80% read / 20% write
		PayloadBytes:          4096,
	}
	plan, err := workload.Derive(spec)
	if err != nil {
		fmt.Println("workload error:", err)
		os.Exit(1)
	}
	printPlan(plan)

	arch := sim.Architecture{
		Name: "basic-web",
		Components: []sim.ComponentSpec{
			{ID: "client", Kind: sim.KindClient},
			{ID: "api", Kind: sim.KindAPIServer, Concurrency: 10, QueueLimit: 50,
				CapacityRPS: 1500, DefaultServiceTimeMillis: 3},
			{ID: "cache", Kind: sim.KindCache, Concurrency: 2, QueueLimit: 100,
				HitRatio: 0.8, DefaultServiceTimeMillis: 0.1},
			{ID: "db", Kind: sim.KindDatabase, Concurrency: 4, QueueLimit: 20,
				CapacityRPS: 500, DefaultServiceTimeMillis: 5},
		},
		Links: []sim.Link{
			{From: "client", To: "api"},
			{From: "api", To: "cache"},
			{From: "cache", To: "db"},
		},
	}
	if err := arch.Validate(); err != nil {
		fmt.Println("architecture error:", err)
		os.Exit(1)
	}

	const durationMS = 60_000 // one simulated minute

	// Run 1: healthy baseline.
	base, err := sim.Simulate(arch, spec, sim.Options{
		Seed:       seed,
		DurationMS: durationMS,
		Retry:      sim.RetryPolicy{MaxRetries: 2, BackoffBaseMS: 5, TimeoutMS: 50},
		RetryOn:    []string{"api", "cache"},
	})
	if err != nil {
		fmt.Println("simulation error:", err)
		os.Exit(1)
	}

	// Run 2: cache crashes (pass-through) from t=20s to t=35s.
	crash, err := sim.Simulate(arch, spec, sim.Options{
		Seed:       seed, // same arrivals → controlled comparison
		DurationMS: durationMS,
		Retry:      sim.RetryPolicy{MaxRetries: 2, BackoffBaseMS: 5, TimeoutMS: 50},
		RetryOn:    []string{"api", "cache"},
		Failures: []sim.Failure{{
			Target: "cache", Type: sim.FailureCrash,
			StartMS: 20_000, DurationMS: 15_000,
			Config: sim.FailureConfig{PassThrough: true},
		}},
	})
	if err != nil {
		fmt.Println("simulation error:", err)
		os.Exit(1)
	}

	fmt.Println("################ RUN 1 — healthy baseline ################")
	printMetrics(base)
	printDiagnosis(sim.DiagnoseWithBaseline(base.Metrics, base.Metrics), "baseline")

	fmt.Println("################ RUN 2 — cache crash 20s→35s ################")
	printMetrics(crash)
	printDiagnosis(sim.DiagnoseWithBaseline(crash.Metrics, base.Metrics), "crash vs baseline")
}

func printPlan(p workload.Plan) {
	fmt.Println("== workload ==")
	fmt.Printf("users %d → DAU %d (%.1f%%) → %.0f req/day → avg %.1f RPS → peak %.1f RPS (x%.1f)\n",
		p.TotalUsers, p.DAU, p.DAUFraction*100, p.RequestsPerDay,
		p.AverageRPS, p.PeakRPS, p.PeakMultiplier)
	fmt.Printf("split %.0f%% read / %.0f%% write, payload %d B, mean inter-arrival %.2f ms\n\n",
		p.ReadFraction*100, p.WriteFraction*100, p.PayloadBytes, p.MeanInterArrivalMillis)
}

func printMetrics(r *sim.RunResult) {
	m := r.Metrics
	fmt.Println("== system ==")
	fmt.Printf("window %.0fs: generated %d, completed %d, rejected %d, failed %d (timeouts %d, drops %d), in-flight %d\n",
		m.DurationMS/1000, m.Generated, m.Completed, m.Rejected, m.Failed, m.Timeouts, m.Dropped, m.InFlight)
	fmt.Printf("error rate %.2f%%, timeout rate %.2f%%\n", m.ErrorRate*100, m.TimeoutRate*100)
	fmt.Printf("latency ms: avg %.2f  p50 %.2f  p95 %.2f  p99 %.2f  max %.2f\n\n",
		m.AvgLatencyMS, m.P50MS, m.P95MS, m.P99MS, m.MaxLatencyMS)

	fmt.Println("== components ==")
	fmt.Printf("%-8s %-14s %8s %8s %8s %8s %6s %6s %7s %8s %8s\n",
		"ID", "KIND", "ARRIVED", "COMPL", "REJECT", "FAILED", "QMAX", "TREND", "UTIL", "THR-RPS", "ARR-RPS")
	for _, c := range m.Components {
		util := "-"
		if c.Utilization > 0 {
			util = fmt.Sprintf("%.1f%%", c.Utilization*100)
		}
		fmt.Printf("%-8s %-14s %8d %8d %8d %8d %6d %6s %7s %8.1f %8.1f\n",
			c.ID, c.Kind, c.Arrived, c.Completed, c.Rejected, c.Failed,
			c.MaxQueueDepth, c.QueueTrend, util, c.ThroughputRPS, c.ArrivalRPS)
	}
	fmt.Printf("\nevents: processed %d, stop=%s (sim time %s)\n\n",
		r.Events.Processed, r.Events.StopReason, r.Events.FinalTime)
}

func printDiagnosis(d sim.Diagnosis, label string) {
	fmt.Printf("== diagnosis (%s) ==\n", label)
	fmt.Println(d.Summary)
	for _, b := range d.Bottlenecks {
		fmt.Printf("  Bottleneck: %s (%s) — severity %s\n", b.ComponentID, b.Kind, b.Severity)
		fmt.Println("  Why:")
		for _, r := range b.Reasons {
			fmt.Printf("    - %s\n", r)
		}
	}
	if len(d.Impacts) > 0 && !d.Healthy {
		fmt.Println("  Impact:")
		for _, imp := range d.Impacts {
			fmt.Printf("    - %s\n", imp)
		}
	}
	fmt.Println()
}
