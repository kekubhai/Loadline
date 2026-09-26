// Command simulator is the executable example for LOADLINE's simulation
// stack: the discrete-event engine, the workload model, and the generic
// component layer.
//
// It derives a load plan from human-scale assumptions, simulates the basic
// architecture (Client → API Server → Cache → Database) twice with the
// same seed, prints the per-component metrics, and verifies both runs
// produced identical results — proving deterministic execution end to end.
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
	// 1. Workload: inspectable derivation chain.
	spec := workload.Spec{
		TotalUsers:            1_000_000,
		DAU:                   100_000,
		RequestsPerUserPerDay: 20,
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

	// 2. Architecture: Client → API Server → Cache → Database.
	arch := sim.Architecture{
		Name: "basic-web",
		Components: []sim.ComponentSpec{
			{ID: "client", Kind: sim.KindClient},
			{ID: "api", Kind: sim.KindAPIServer, Concurrency: 10, QueueLimit: 50,
				CapacityRPS: 1500, DefaultServiceTimeMillis: 3},
			{ID: "cache", Kind: sim.KindCache, Concurrency: 2, QueueLimit: 100,
				HitRatio: 0.8, DefaultServiceTimeMillis: 0.1},
			{ID: "db", Kind: sim.KindDatabase, Concurrency: 4, QueueLimit: 20,
				CapacityRPS: 400, DefaultServiceTimeMillis: 5},
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

	// 3. Simulate twice with the same seed; results must be identical.
	const durationMS = 60_000 // one simulated minute
	run1, err := sim.Simulate(arch, spec, sim.Options{Seed: seed, DurationMS: durationMS})
	if err != nil {
		fmt.Println("simulation error:", err)
		os.Exit(1)
	}
	run2, err := sim.Simulate(arch, spec, sim.Options{Seed: seed, DurationMS: durationMS})
	if err != nil {
		fmt.Println("simulation error:", err)
		os.Exit(1)
	}

	printMetrics(run1)

	if metricsEqual(run1.Metrics, run2.Metrics) {
		fmt.Println("determinism: OK — identical seed produced identical metrics")
	} else {
		fmt.Println("determinism: FAILED — runs diverged")
		os.Exit(1)
	}
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
	fmt.Printf("window %.0fs: generated %d, completed %d, rejected %d, in-flight %d\n",
		m.DurationMS/1000, m.Generated, m.Completed, m.Rejected, m.InFlight)
	fmt.Printf("latency ms: avg %.2f  p50 %.2f  p95 %.2f  p99 %.2f  max %.2f\n\n",
		m.AvgLatencyMS, m.P50MS, m.P95MS, m.P99MS, m.MaxLatencyMS)

	fmt.Println("== components ==")
	fmt.Printf("%-8s %-14s %9s %9s %9s %7s %7s %8s %9s\n",
		"ID", "KIND", "ARRIVED", "COMPLETED", "REJECTED", "QDEPTH", "QMAX", "UTIL", "ARR-RPS")
	for _, c := range m.Components {
		util := "-"
		if c.Utilization > 0 {
			util = fmt.Sprintf("%.1f%%", c.Utilization*100)
		}
		sat := ""
		if c.Saturated {
			sat = " !SAT"
		}
		fmt.Printf("%-8s %-14s %9d %9d %9d %7d %7d %8s %9.1f%s\n",
			c.ID, c.Kind, c.Arrived, c.Completed, c.Rejected,
			c.QueueDepth, c.MaxQueueDepth, util, c.ArrivalRPS, sat)
	}
	fmt.Printf("\nevents: processed %d, scheduled %d, stop=%s (sim time %s)\n",
		r.Events.Processed, r.Events.Scheduled, r.Events.StopReason, r.Events.FinalTime)
}

func metricsEqual(a, b sim.Metrics) bool {
	if a.Generated != b.Generated || a.Completed != b.Completed ||
		a.Rejected != b.Rejected || a.InFlight != b.InFlight {
		return false
	}
	if a.AvgLatencyMS != b.AvgLatencyMS || a.P99MS != b.P99MS {
		return false
	}
	if len(a.Components) != len(b.Components) {
		return false
	}
	for i := range a.Components {
		if a.Components[i] != b.Components[i] {
			return false
		}
	}
	return true
}
