package loadlinev1

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
)

// createAndStart registers a test simulation and starts it paced (the
// full 5s horizon takes wallMS of wall time). Returns the run ID.
func createAndStart(t *testing.T, c lv1connect.SimulationServiceClient, wallMS float64) string {
	t.Helper()
	created, err := c.CreateSimulation(context.Background(), connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: testArchitecture(),
		Workload:     testWorkload(),
		Options:      testOptions(),
	}))
	if err != nil {
		t.Fatalf("CreateSimulation: %v", err)
	}
	id := created.Msg.GetSimulation().GetId()
	if _, err := c.RunSimulation(context.Background(), connect.NewRequest(&v1.RunSimulationRequest{
		SimulationId:   id,
		WallDurationMs: wallMS,
	})); err != nil {
		t.Fatalf("RunSimulation: %v", err)
	}
	return id
}

// waitForStatus polls until the run reaches one of the wanted statuses.
func waitForStatus(t *testing.T, c lv1connect.SimulationServiceClient, id string, wanted ...v1.RunStatus) v1.RunStatus {
	t.Helper()
	want := map[v1.RunStatus]bool{}
	for _, w := range wanted {
		want[w] = true
	}
	deadline := time.Now().Add(15 * time.Second)
	var last *v1.GetSimulationStatusResponse
	for time.Now().Before(deadline) {
		st, err := c.GetSimulationStatus(context.Background(), connect.NewRequest(&v1.GetSimulationStatusRequest{SimulationId: id}))
		if err != nil {
			t.Fatalf("GetSimulationStatus: %v", err)
		}
		last = st.Msg
		if want[st.Msg.GetStatus()] {
			return st.Msg.GetStatus()
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach %v in time; last status=%s err=%q simTime=%f",
		id, wanted, last.GetStatus(), last.GetError(), last.GetProgress().GetSimTimeMs())
	return v1.RunStatus_RUN_STATUS_UNSPECIFIED
}

// TestPauseResumeLifecycle verifies the full operational flow: a paced
// run freezes at PAUSED (progress stops advancing), resumes, and
// completes with a full-horizon result.
func TestPauseResumeLifecycle(t *testing.T) {
	c := newTestClient(t)
	// 60s wall pacing: the engine budgets 2 events/sim-ms (10k events for
	// a 5s horizon) but the real run has far fewer, so actual wall time is
	// ~events × 6ms ≈ seconds — 700ms lands safely mid-run.
	id := createAndStart(t, c, 60_000)

	waitForStatus(t, c, id, v1.RunStatus_RUN_STATUS_RUNNING)
	time.Sleep(700 * time.Millisecond) // let some sim time elapse

	if _, err := c.PauseSimulation(context.Background(), connect.NewRequest(&v1.PauseSimulationRequest{SimulationId: id})); err != nil {
		t.Fatalf("PauseSimulation: %v", err)
	}
	waitForStatus(t, c, id, v1.RunStatus_RUN_STATUS_PAUSED)

	st := func() *v1.ProgressSnapshot {
		s, err := c.GetSimulationStatus(context.Background(), connect.NewRequest(&v1.GetSimulationStatusRequest{SimulationId: id}))
		if err != nil {
			t.Fatalf("GetSimulationStatus: %v", err)
		}
		return s.Msg.GetProgress()
	}
	pausedSnap := st()
	time.Sleep(500 * time.Millisecond) // paused: nothing may advance
	afterSnap := st()
	if pausedSnap == nil || afterSnap == nil {
		t.Fatal("paused run has no progress snapshot")
	}
	if afterSnap.GetSimTimeMs() != pausedSnap.GetSimTimeMs() {
		t.Fatalf("sim time advanced while paused: %f → %f",
			pausedSnap.GetSimTimeMs(), afterSnap.GetSimTimeMs())
	}

	if _, err := c.ResumeSimulation(context.Background(), connect.NewRequest(&v1.ResumeSimulationRequest{SimulationId: id})); err != nil {
		t.Fatalf("ResumeSimulation: %v", err)
	}
	waitForStatus(t, c, id, v1.RunStatus_RUN_STATUS_RUNNING)
	waitForStatus(t, c, id, v1.RunStatus_RUN_STATUS_COMPLETED)

	res, err := c.GetResults(context.Background(), connect.NewRequest(&v1.GetResultsRequest{SimulationId: id}))
	if err != nil {
		t.Fatalf("GetResults: %v", err)
	}
	// A completed run must have measured the full configured horizon.
	if res.Msg.GetMetrics().GetDurationMs() != 5_000 {
		t.Fatalf("completed duration = %f, want 5000", res.Msg.GetMetrics().GetDurationMs())
	}
	if res.Msg.GetMetrics().GetGenerated() == 0 {
		t.Fatal("completed run generated no requests")
	}
}

// TestStopPublishesPartialResults verifies stopping mid-run yields the
// STOPPED status with queryable partial results whose measured window
// matches the simulated span actually processed.
func TestStopPublishesPartialResults(t *testing.T) {
	c := newTestClient(t)
	// Same reasoning as the pause test: generous pacing keeps the stop
	// point (700ms) inside the run instead of after completion.
	id := createAndStart(t, c, 60_000)

	waitForStatus(t, c, id, v1.RunStatus_RUN_STATUS_RUNNING)
	time.Sleep(700 * time.Millisecond)

	if _, err := c.StopSimulation(context.Background(), connect.NewRequest(&v1.StopSimulationRequest{SimulationId: id})); err != nil {
		t.Fatalf("StopSimulation: %v", err)
	}
	waitForStatus(t, c, id, v1.RunStatus_RUN_STATUS_STOPPED)

	res, err := c.GetResults(context.Background(), connect.NewRequest(&v1.GetResultsRequest{SimulationId: id}))
	if err != nil {
		t.Fatalf("GetResults on a stopped run: %v", err)
	}
	m := res.Msg.GetMetrics()
	if m.GetDurationMs() <= 0 || m.GetDurationMs() >= 5_000 {
		t.Fatalf("stopped duration = %f, want (0, 5000) — the measured span only", m.GetDurationMs())
	}
	if res.Msg.GetSummary().GetStopReason() != "stopped" {
		t.Fatalf("stop reason = %q, want stopped", res.Msg.GetSummary().GetStopReason())
	}
}

// TestDeterminismIndependentOfPacing verifies the AGENTS.md rule at the
// API level: the same seed run fast vs paced produces byte-identical
// metrics — wall-clock pacing never leaks into simulated behavior.
func TestDeterminismIndependentOfPacing(t *testing.T) {
	c := newTestClient(t)

	metrics := func(paced bool) *v1.SystemMetrics {
		var id string
		if paced {
			id = createAndStart(t, c, 6_000)
		} else {
			id = createAndStart(t, c, 0)
		}
		waitForStatus(t, c, id, v1.RunStatus_RUN_STATUS_COMPLETED, v1.RunStatus_RUN_STATUS_FAILED)
		res, err := c.GetResults(context.Background(), connect.NewRequest(&v1.GetResultsRequest{SimulationId: id}))
		if err != nil {
			t.Fatalf("GetResults: %v", err)
		}
		return res.Msg.GetMetrics()
	}

	fast, slow := metrics(false), metrics(true)
	if fast.GetGenerated() != slow.GetGenerated() ||
		fast.GetCompleted() != slow.GetCompleted() ||
		fast.GetP50Ms() != slow.GetP50Ms() ||
		fast.GetP99Ms() != slow.GetP99Ms() ||
		fast.GetAvgLatencyMs() != slow.GetAvgLatencyMs() {
		t.Fatalf("pacing changed results:\nfast: %+v\nslow: %+v", fast, slow)
	}
}

// TestControlPreconditions verifies control RPCs reject runs that are
// not live.
func TestControlPreconditions(t *testing.T) {
	c := newTestClient(t)
	created, err := c.CreateSimulation(context.Background(), connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: testArchitecture(),
		Workload:     testWorkload(),
		Options:      testOptions(),
	}))
	if err != nil {
		t.Fatalf("CreateSimulation: %v", err)
	}
	id := created.Msg.GetSimulation().GetId()
	if _, err := c.PauseSimulation(context.Background(), connect.NewRequest(&v1.PauseSimulationRequest{SimulationId: id})); err == nil {
		t.Fatal("PauseSimulation on a PENDING run succeeded")
	}
	if _, err := c.StopSimulation(context.Background(), connect.NewRequest(&v1.StopSimulationRequest{SimulationId: id})); err == nil {
		t.Fatal("StopSimulation on a PENDING run succeeded")
	}
}
