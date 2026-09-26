// Package sim implements LOADLINE's generic component and request-flow
// simulation layer on top of the engine package.
//
// Components are models, not providers: nothing here knows about AWS, GCP,
// or Cloudflare. Provider-specific behavior is a future layer that must
// plug into the same ComponentSpec/Behavior abstractions.
package sim

import (
	"fmt"
	"time"

	"github.com/kekubhai/Loadline/apps/simulator/engine"
)

// ComponentKind enumerates the generic V1 component types.
type ComponentKind string

const (
	KindClient        ComponentKind = "client"
	KindLoadBalancer  ComponentKind = "load_balancer"
	KindAPIServer     ComponentKind = "api_server"
	KindCache         ComponentKind = "cache"
	KindQueue         ComponentKind = "queue"
	KindWorker        ComponentKind = "worker"
	KindDatabase      ComponentKind = "database"
	KindObjectStorage ComponentKind = "object_storage"
	KindNetwork       ComponentKind = "network"
)

// Op is the class of work a request carries. Components price each op
// differently: a cache read is ~100µs, a DB write might be 5ms.
type Op string

const (
	OpRead     Op = "read"
	OpWrite    Op = "write"
	OpCompute  Op = "compute"
	OpPut      Op = "put"
	OpGet      Op = "get"
	OpEnqueue  Op = "enqueue"
	OpDequeue  Op = "dequeue"
	OpTransit  Op = "transit"
	OpDeliver  Op = "deliver"
	OpComplete Op = "complete"
)

// ComponentSpec is the static definition of one node in the architecture.
// Values are assumptions the user can inspect and change; the simulation
// turns them into behavior.
type ComponentSpec struct {
	ID   string
	Kind ComponentKind

	// ServiceTimeMillis prices each op class the component executes. Ops
	// absent from the map use DefaultServiceTimeMillis.
	ServiceTimeMillis        map[Op]float64
	DefaultServiceTimeMillis float64

	// Concurrency is the number of requests the component processes in
	// parallel. 0 means unconstrained (pure latency pass-through such as
	// client or network).
	Concurrency int

	// QueueLimit is the max requests that may wait while all concurrency
	// slots are busy. Requests arriving to a full queue are rejected.
	// 0 means unbounded queueing.
	QueueLimit int

	// CapacityRPS, when > 0, is the component's maximum sustainable
	// throughput. When the observed arrival rate exceeds it, the component
	// reports saturated in metrics. It does not by itself reject requests;
	// the queue limit does.
	CapacityRPS float64

	// HitRatio applies to caches: probability a read is served without
	// going downstream. 0–1; ignored by other kinds.
	HitRatio float64

	// FanOut applies to load balancers and networks: each incoming request
	// is replicated to N downstream targets (1 = plain routing).
	FanOut int

	// CapacityBytes applies to object storage: total size of the stored
	// object set, used for bandwidth math in reports.
	CapacityBytes int64
}

// ServiceTime returns the modeled service time for an op, falling back to
// the component default.
func (c ComponentSpec) ServiceTime(op Op) float64 {
	if c.ServiceTimeMillis != nil {
		if ms, ok := c.ServiceTimeMillis[op]; ok {
			return ms
		}
	}
	return c.DefaultServiceTimeMillis
}

func (c ComponentSpec) validate() error {
	if c.ID == "" {
		return fmt.Errorf("component: ID must not be empty")
	}
	if c.Concurrency < 0 {
		return fmt.Errorf("component %s: Concurrency cannot be negative", c.ID)
	}
	if c.QueueLimit < 0 {
		return fmt.Errorf("component %s: QueueLimit cannot be negative", c.ID)
	}
	if c.CapacityRPS < 0 {
		return fmt.Errorf("component %s: CapacityRPS cannot be negative", c.ID)
	}
	if c.HitRatio < 0 || c.HitRatio > 1 {
		return fmt.Errorf("component %s: HitRatio must be in [0,1], got %f", c.ID, c.HitRatio)
	}
	return nil
}

// Link wires two components: requests flow From → To.
type Link struct {
	From string
	To   string
	// Condition, when non-empty, restricts which requests traverse the
	// link: "read", "write", or "" for everything.
	Condition string
}

// Architecture is the serializable definition of what to simulate.
type Architecture struct {
	Name       string
	Components []ComponentSpec
	Links      []Link
}

// Validate checks structural rules:
//
// - exactly one client,
// - no unknown IDs in links,
// - no cycles,
// - every non-client node is reachable from the client,
// - every queue has exactly one linked worker downstream,
// - workers are linked only from queues.
func (a Architecture) Validate() error {
	if len(a.Components) == 0 {
		return fmt.Errorf("architecture: has no components")
	}
	byID := make(map[string]ComponentSpec, len(a.Components))
	clients := 0
	for _, c := range a.Components {
		if err := c.validate(); err != nil {
			return err
		}
		if _, dup := byID[c.ID]; dup {
			return fmt.Errorf("architecture: duplicate component ID %q", c.ID)
		}
		byID[c.ID] = c
		if c.Kind == KindClient {
			clients++
		}
	}
	if clients != 1 {
		return fmt.Errorf("architecture: exactly one client is required, found %d", clients)
	}

	outgoing := map[string][]Link{}
	for _, l := range a.Links {
		if _, ok := byID[l.From]; !ok {
			return fmt.Errorf("architecture: link references unknown source %q", l.From)
		}
		if _, ok := byID[l.To]; !ok {
			return fmt.Errorf("architecture: link references unknown target %q", l.To)
		}
		if l.From == l.To {
			return fmt.Errorf("architecture: self-link on %q", l.From)
		}
		outgoing[l.From] = append(outgoing[l.From], l)
	}

	if hasCycle(byID, outgoing) {
		return fmt.Errorf("architecture: dependency cycle detected")
	}

	// Queue/worker pairing rules.
	for _, c := range a.Components {
		switch c.Kind {
		case KindQueue:
			workers := 0
			for _, l := range outgoing[c.ID] {
				if byID[l.To].Kind == KindWorker {
					workers++
				}
			}
			if workers != 1 {
				return fmt.Errorf("architecture: queue %q must link to exactly one worker, found %d", c.ID, workers)
			}
		case KindWorker:
			if len(outgoing[c.ID]) > 0 {
				return fmt.Errorf("architecture: worker %q cannot link onward", c.ID)
			}
			queues := 0
			for _, l := range a.Links {
				if l.To == c.ID && byID[l.From].Kind == KindQueue {
					queues++
				}
			}
			if queues != 1 {
				return fmt.Errorf("architecture: worker %q must be linked from exactly one queue, found %d", c.ID, queues)
			}
		}
	}

	// Reachability from the client.
	if !allReachable(byID, outgoing) {
		return fmt.Errorf("architecture: some components are unreachable from the client")
	}
	return nil
}

// hasCycle runs an iterative DFS with tri-state coloring.
func hasCycle(nodes map[string]ComponentSpec, outgoing map[string][]Link) bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = gray
		for _, l := range outgoing[id] {
			switch color[l.To] {
			case gray:
				return true
			case white:
				if visit(l.To) {
					return true
				}
			}
		}
		color[id] = black
		return false
	}
	for id := range nodes {
		if color[id] == white {
			if visit(id) {
				return true
			}
		}
	}
	return false
}

// allReachable reports whether every node except the client is reachable
// from the client.
func allReachable(nodes map[string]ComponentSpec, outgoing map[string][]Link) bool {
	var client string
	for id, c := range nodes {
		if c.Kind == KindClient {
			client = id
		}
	}
	seen := map[string]bool{}
	stack := []string{client}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		for _, l := range outgoing[id] {
			if !seen[l.To] {
				stack = append(stack, l.To)
			}
		}
	}
	for id, c := range nodes {
		if c.Kind != KindClient && !seen[id] {
			return false
		}
	}
	return true
}

// delay converts milliseconds to engine Duration.
func delay(ms float64) engine.Duration {
	return engine.Duration(ms * float64(engine.Millisecond))
}

// nowMillis renders the current context time in milliseconds (monotonic
// simulated clock; epoch is arbitrary).
func nowMillis(ctx *engine.Context) float64 {
	return float64(ctx.Now()) / float64(engine.Millisecond)
}

// millisToWall converts simulated milliseconds to a time.Duration — used
// only for reporting latencies in standard units.
func millisToWall(ms float64) time.Duration {
	return time.Duration(ms * float64(time.Millisecond))
}
