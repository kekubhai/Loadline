package sim

import (
	"math"
	"strings"
	"testing"

	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// floodWorkload is a valid, ordinary workload used as the base for the
// poisoned-options table below.
func floodWorkload() workload.Spec {
	return workload.Spec{
		TotalUsers: 10_000, DAU: 5_000, RequestsPerUserPerDay: 50,
		PeakMultiplier: 4, ReadWriteRatio: 4,
	}
}

// Every option value the engine cannot execute must be rejected BEFORE
// the first event is scheduled — not as a mid-run panic, and not as a
// result full of NaN. Each case mutates one field of an otherwise
// valid option set.
func TestValidateOptionsRejectsPoisonedValues(t *testing.T) {
	base := Options{Seed: 7, DurationMS: 5_000, Retry: RetryPolicy{MaxRetries: 2, BackoffBaseMS: 5, TimeoutMS: 200}}

	cases := []struct {
		name    string
		mutate  func(o *Options)
		wantSub string
	}{
		{"nan horizon", func(o *Options) { o.DurationMS = math.NaN() }, "DurationMS"},
		{"inf horizon", func(o *Options) { o.DurationMS = math.Inf(1) }, "DurationMS"},
		{"negative backoff", func(o *Options) { o.Retry.BackoffBaseMS = -1 }, "BackoffBaseMS"},
		{"nan backoff", func(o *Options) { o.Retry.BackoffBaseMS = math.NaN() }, "BackoffBaseMS"},
		{"nan timeout", func(o *Options) { o.Retry.TimeoutMS = math.NaN() }, "TimeoutMS"},
		{"negative retries", func(o *Options) { o.Retry.MaxRetries = -1 }, "MaxRetries"},
		// Beyond this the exponential backoff shift overflows sign and
		// would schedule a negative delay (engine panic).
		{"absurd retries", func(o *Options) { o.Retry.MaxRetries = 64 }, "MaxRetries"},
		{"negative sample window", func(o *Options) { o.TrackWindows = -5 }, "TrackWindows"},
		{"retry on unknown component", func(o *Options) { o.RetryOn = []string{"ghost"} }, "RetryOn"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := base
			tc.mutate(&opts)
			_, err := Simulate(basicArch(), floodWorkload(), opts)
			if err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// NaN workload fields pass every <= / < comparison, so a NaN spec would
// otherwise derive a plan with NaN rates and report NaN metrics as a
// successful run.
func TestWorkloadRejectsNonFiniteFields(t *testing.T) {
	spec := floodWorkload()
	spec.RequestsPerUserPerDay = math.NaN()
	if _, err := Simulate(basicArch(), spec, Options{Seed: 1, DurationMS: 1_000}); err == nil {
		t.Fatal("expected NaN requests/user/day to be rejected")
	}

	spec = floodWorkload()
	spec.PeakMultiplier = math.Inf(1)
	if _, err := Simulate(basicArch(), spec, Options{Seed: 1, DurationMS: 1_000}); err == nil {
		t.Fatal("expected infinite peak multiplier to be rejected")
	}
}

// A workload whose arrivals alone exceed the per-run budget is refused
// up front with the arithmetic shown, instead of occupying the engine
// for minutes without progress.
func TestRunSizeGuardRejectsUnsimulatableLoad(t *testing.T) {
	spec := workload.Spec{
		TotalUsers: 500_000_000, DAU: 400_000_000, RequestsPerUserPerDay: 1_000,
		PeakMultiplier: 20, ReadWriteRatio: 4, // ≫ 50M arrivals over 60s
	}
	_, err := Simulate(basicArch(), spec, Options{Seed: 1, DurationMS: 60_000})
	if err == nil {
		t.Fatal("expected the run-size guard to reject this workload")
	}
	if !strings.Contains(err.Error(), "requests") {
		t.Fatalf("error should explain the request budget, got %q", err.Error())
	}
}

// StartPaced validates exactly like Simulate: no goroutine starts and
// no pacing handle is returned for options the engine cannot execute.
func TestStartPacedRejectsPoisonedOptions(t *testing.T) {
	opts := Options{Seed: 7, DurationMS: 5_000, Retry: RetryPolicy{MaxRetries: 100}}
	if _, err := StartPaced(basicArch(), floodWorkload(), opts, 0); err == nil {
		t.Fatal("StartPaced must reject MaxRetries beyond the backoff bound")
	}
	opts = Options{Seed: 7, DurationMS: math.NaN()}
	if _, err := StartPaced(basicArch(), floodWorkload(), opts, 0); err == nil {
		t.Fatal("StartPaced must reject a NaN horizon")
	}
}

// A healthy paced run reports no error and stores a result — the error
// channel exists only for recovered engine panics.
func TestPacedRunHasNoErrorOnNormalRun(t *testing.T) {
	p, err := StartPaced(basicArch(), floodWorkload(), Options{Seed: 11, DurationMS: 500}, 0)
	if err != nil {
		t.Fatalf("StartPaced: %v", err)
	}
	res := p.WaitResult()
	if p.Err() != nil {
		t.Fatalf("unexpected run error: %v", p.Err())
	}
	if res == nil {
		t.Fatal("expected a result from a healthy paced run")
	}
	if res.Metrics.Generated == 0 {
		t.Fatal("expected the paced run to generate traffic")
	}
}
