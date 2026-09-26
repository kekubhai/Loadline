package workload

import (
	"fmt"
	"strings"
	"testing"
)

// Contract: the derivation chain holds at every link, exactly as
// documented — no hidden jumps.
func TestDeriveChain(t *testing.T) {
	p, err := Derive(Spec{
		TotalUsers:            1_000_000,
		DAU:                   100_000,
		RequestsPerUserPerDay: 20,
		PeakMultiplier:        5,
		ReadWriteRatio:        4,
		PayloadBytes:          4096,
	})
	if err != nil {
		t.Fatal(err)
	}

	if p.RequestsPerDay != 2_000_000 {
		t.Errorf("RequestsPerDay = %f, want 2000000", p.RequestsPerDay)
	}
	if p.AverageRPS != 2_000_000/86400.0 {
		t.Errorf("AverageRPS = %f, want %f", p.AverageRPS, 2_000_000/86400.0)
	}
	if p.PeakRPS != p.AverageRPS*5 {
		t.Errorf("PeakRPS = %f, want AverageRPS*5", p.PeakRPS)
	}
	if p.DAUFraction != 0.1 {
		t.Errorf("DAUFraction = %f, want 0.1", p.DAUFraction)
	}
	if abs(p.ReadFraction-0.8) > 1e-12 || abs(p.WriteFraction-0.2) > 1e-12 {
		t.Errorf("read/write split = %f/%f, want 0.8/0.2", p.ReadFraction, p.WriteFraction)
	}
	if p.MeanInterArrivalMillis != 1000.0/p.PeakRPS {
		t.Errorf("MeanInterArrivalMillis = %f, want %f", p.MeanInterArrivalMillis, 1000.0/p.PeakRPS)
	}
}

// Contract: 86,400 DAU issuing 1 request/user/day at peak multiplier 1
// yields exactly 1 RPS — the smallest meaningful anchor point.
func TestDeriveAnchor(t *testing.T) {
	p, err := Derive(Spec{TotalUsers: 100_000, DAU: 86_400, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.AverageRPS != 1 || p.PeakRPS != 1 {
		t.Fatalf("anchor = avg %f peak %f, want 1/1", p.AverageRPS, p.PeakRPS)
	}
	if p.MeanInterArrivalMillis != 1000 {
		t.Fatalf("mean IAT = %f ms, want 1000", p.MeanInterArrivalMillis)
	}
}

// Contract: invalid specs are rejected with explanatory errors.
func TestDeriveValidation(t *testing.T) {
	cases := []struct {
		name   string
		spec   Spec
		substr string
	}{
		{"zero users", Spec{TotalUsers: 0, DAU: 1, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 1}, "TotalUsers"},
		{"zero DAU", Spec{TotalUsers: 10, DAU: 0, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 1}, "DAU"},
		{"DAU > users", Spec{TotalUsers: 10, DAU: 11, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 1}, "exceed"},
		{"zero req/user", Spec{TotalUsers: 10, DAU: 5, RequestsPerUserPerDay: 0, PeakMultiplier: 1, ReadWriteRatio: 1}, "RequestsPerUserPerDay"},
		{"peak < 1", Spec{TotalUsers: 10, DAU: 5, RequestsPerUserPerDay: 1, PeakMultiplier: 0.5, ReadWriteRatio: 1}, "PeakMultiplier"},
		{"ratio <= 0", Spec{TotalUsers: 10, DAU: 5, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 0}, "ReadWriteRatio"},
		{"negative payload", Spec{TotalUsers: 10, DAU: 5, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 1, PayloadBytes: -1}, "PayloadBytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Derive(tc.spec)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.substr)
			}
		})
	}
}

// Contract: 100k peak requests draw read/write labels that match the
// configured ratio within statistical tolerance.
func TestReadWriteSplitMatchesRatio(t *testing.T) {
	r := NewSplitter(77)
	const n = 100000
	reads := 0
	for i := 0; i < n; i++ {
		if r.IsRead() {
			reads++
		}
	}
	frac := float64(reads) / n
	if frac < 0.79 || frac > 0.81 {
		t.Fatalf("read fraction = %f, want ~0.8", frac)
	}
}

// Contract: the derived mean inter-arrival time matches the realized mean
// of drawn exponential gaps.
func TestArrivalDrawsMatchPlan(t *testing.T) {
	p, err := Derive(Spec{TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 20, PeakMultiplier: 5, ReadWriteRatio: 4})
	if err != nil {
		t.Fatal(err)
	}
	g := NewArrivalClock(99, p.MeanInterArrivalMillis)
	const n = 200000
	var sum float64
	for i := 0; i < n; i++ {
		sum += g.NextGapMillis()
	}
	mean := sum / n
	want := p.MeanInterArrivalMillis
	// 5-sigma tolerance on the mean of N exponential draws.
	tol := 5 * want / sqrt(n)
	if abs(mean-want) > tol {
		t.Fatalf("realized mean IAT = %f ms, want %f (tol %f)", mean, want, tol)
	}
}

// Contract: every request gets a unique, increasing ID.
func TestRequestIDsUnique(t *testing.T) {
	ids := NewIDAllocator()
	var prev uint64
	for i := 0; i < 1000; i++ {
		id := ids.Next()
		if id <= prev {
			t.Fatalf("ID %d not greater than previous %d", id, prev)
		}
		prev = id
	}
}

func sqrt(x float64) float64 {
	// Newton's method; avoids importing math in tests for one call.
	z := x
	for i := 0; i < 40; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

var _ = fmt.Sprintf
