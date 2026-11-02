package ratelimit

import (
	"sync"
	"testing"
	"time"

	"github.com/kekubhai/Loadline/apps/oast/internal/clock"
)

func TestLimiterBurstAndRefill(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0))
	l := New(1, 2, clk)

	if !l.Allow() || !l.Allow() {
		t.Fatal("initial burst must be allowed")
	}
	if l.Allow() {
		t.Fatal("third request must be denied before refill")
	}
	clk.Advance(time.Second)
	if !l.Allow() {
		t.Fatal("one token should refill after one second")
	}
	if l.Allow() {
		t.Fatal("refill must not exceed the burst")
	}
}

func TestLimiterConcurrent(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0))
	l := New(0.001, 50, clk)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow() {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 50 {
		t.Fatalf("allowed = %d, want 50", allowed)
	}
}
