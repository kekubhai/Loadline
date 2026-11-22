package sim

import (
	"testing"
	"time"

	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// TestStartPacedHonorsWallDuration is the regression for paced runs that
// overran their requested wall time by an order of magnitude: the old
// per-event tick assumed ~2 events per simulated ms, but a busy
// architecture produces many more, so a "10 s at 1×" run took minutes and
// the client's stream gave up ("network error"). Wall time must now track
// the requested duration regardless of event density.
func TestStartPacedHonorsWallDuration(t *testing.T) {
	dense := workload.Spec{
		TotalUsers: 10_000_000, DAU: 1_000_000, RequestsPerUserPerDay: 40,
		PeakMultiplier: 5, ReadWriteRatio: 4, PayloadBytes: 4096,
	} // ≈2.3k peak RPS → several events per simulated ms
	const wallMS = 400.0
	start := time.Now()
	p, err := StartPaced(basicArch(), dense, Options{Seed: 7, DurationMS: 2_000}, wallMS)
	if err != nil {
		t.Fatalf("StartPaced: %v", err)
	}
	res := p.WaitResult()
	wall := time.Since(start)
	if res == nil || p.Err() != nil {
		t.Fatalf("paced run failed: %v", p.Err())
	}
	if perSimMS := float64(res.Events.Processed) / 2_000; perSimMS < 4 {
		t.Fatalf("workload too sparse to exercise the regression: %.1f events/sim-ms", perSimMS)
	}
	if wall < 300*time.Millisecond || wall > 2*time.Second {
		t.Fatalf("requested %.0fms of wall time, run took %s", wallMS, wall)
	}
}
