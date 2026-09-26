package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/kekubhai/Loadline/apps/simulator/engine"
)

const (
	requestCount  = 5
	seed          = 42
	meanThinkMs   = 5.0  // mean gap between requests, milliseconds
	meanServiceMs = 12.0 // mean service time, milliseconds
)

func main() {
	trace1, res1 := runOnce(seed)
	trace2, res2 := runOnce(seed)

	fmt.Println("== run 1 ==")
	fmt.Print(trace1)
	fmt.Println("== run 2 ==")
	fmt.Print(trace2)

	fmt.Printf("processed: run1=%d run2=%d  final sim time: %s / %s\n",
		res1.Processed, res2.Processed, res1.FinalTime, res2.FinalTime)

	if trace1 == trace2 && res1.Processed == res2.Processed {
		fmt.Println("determinism: OK — identical seed produced identical traces")
	} else {
		fmt.Println("determinism: FAILED — traces diverge")
		os.Exit(1)
	}
}

// runOnce simulates a closed chain of requests against one fictional
// service: each arrival draws an exponential service time from the event's
// private RNG stream and schedules its completion via ctx.Schedule (which
// stamps timestamp and causal parent); completions chain the next arrival.
func runOnce(seed uint64) (string, *engine.Results) {
	clock := engine.NewClock()
	queue := engine.NewPriorityQueue()
	sched := engine.NewScheduler(queue, clock)
	sched.SetSeed(seed)

	var buf bytes.Buffer
	runner := engine.NewRunner(sched, clock)
	runner.TraceOut = &buf

	// Mutually recursive handlers: declare first, assign after.
	var arrive, complete engine.EventHandler

	complete = func(ctx *engine.Context) []engine.Event {
		reqID := ctx.Event().RequestID
		next := reqID + 1
		if next > requestCount {
			return nil
		}
		// Think time before the next request arrives.
		think := engine.Duration(meanThinkMs * float64(engine.Millisecond) * ctx.Rand().ExpFloat64())
		ctx.Schedule(think, engine.Event{
			Type:        "request.arrive",
			ComponentID: "svc",
			RequestID:   next,
			Handler:     arrive,
		})
		return nil
	}

	arrive = func(ctx *engine.Context) []engine.Event {
		reqID := ctx.Event().RequestID
		// Service time drawn from this arrival's private RNG stream.
		service := engine.Duration(meanServiceMs * float64(engine.Millisecond) * ctx.Rand().ExpFloat64())
		ctx.Schedule(service, engine.Event{
			Type:        "request.complete",
			ComponentID: "svc",
			RequestID:   reqID,
			Handler:     complete,
		})
		return nil
	}

	// Kick off request 1 at t=0.
	sched.Schedule(engine.Event{
		Timestamp:   0,
		Type:        "request.arrive",
		ComponentID: "svc",
		RequestID:   1,
		Handler:     arrive,
	})

	res := runner.Run()
	return buf.String(), res
}
