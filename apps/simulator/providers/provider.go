// Package providers holds LOADLINE's cloud service catalog: identity,
// capacity, latency, scaling, failure, and pricing models for AWS,
// Cloudflare, and GCP services.
//
// This package depends on the generic simulation layer, never the other
// way around: the simulator operates against generic ComponentSpec
// behavior and knows nothing about providers. Nothing here connects to a
// real cloud account; all numbers are documented modeling assumptions.
package providers

import (
	"github.com/kekubhai/Loadline/apps/simulator/sim"
)

// ServiceModel is the six-facet contract every catalog service exposes.
// The simulation engine never sees this interface — it is consumed by the
// adapter and the capacity/cost layers.
type ServiceModel interface {
	// Identity
	Provider() string
	Service() string
	Summary() string

	// Capacity: the default concurrency/service-time/queue assumptions,
	// plus the modeled sustained RPS ceiling for the default config.
	Capacity() CapacityModel

	// Latency: per-op service times in milliseconds.
	Latency() LatencyModel

	// Scaling: how the service grows and what it costs to run.
	Scaling() ScalingModel

	// Failure: default failure posture used by failure injection.
	Failure() FailureModel

	// Pricing: the local pricing model (ESTIMATE, not live billing).
	Pricing() PricingModel

	// BuildSpec renders the service as a generic ComponentSpec for the
	// simulator, applying the user's typed config overrides.
	BuildSpec(id string, cfg Config) (sim.ComponentSpec, error)
}

// CapacityModel states the capacity assumptions.
type CapacityModel struct {
	// Concurrency: parallel requests the service processes.
	Concurrency int
	// ServiceTimeMS: baseline per-request service time (also in Latency).
	ServiceTimeMS float64
	// QueueLimit: buffered requests before admission rejection.
	QueueLimit int
	// ModeledRPS: sustained requests/sec this configuration is modeled to
	// sustain = Concurrency / ServiceTime(s), when both are positive.
	ModeledRPS float64
	// Notes: explicit assumptions a human can audit.
	Notes []string
}

// LatencyModel prices each op class in milliseconds.
type LatencyModel struct {
	// PerOp maps op classes ("read", "write", "compute", "get", "put",
	// "enqueue", "compute", "transit") to service time in ms.
	PerOp map[sim.Op]float64
	// Default is the fallback for unmapped ops.
	Default float64
	// Notes: where the numbers came from.
	Notes []string
}

// ScalingModel describes how the service scales and its bounding config.
type ScalingModel struct {
	// Kind: "static" (fixed fleet), "autoscaled" (instance counter), or
	// "serverless" (per-request, effectively unbounded with different
	// cost structure).
	Kind string
	// Units: current number of instances/executions assumed.
	Units int
	// MinUnits / MaxUnits bound autoscaled fleets (0 = unbounded).
	MinUnits int
	MaxUnits int
	// ScaleUnitCost is the monthly cost of one more unit (instance-hour
	// based services); 0 when scaling is free (serverless).
	ScaleUnitCost float64
	// Notes.
	Notes []string
}

// FailureModel is the default failure posture for injection defaults.
type FailureModel struct {
	// MTTRMillis: modeled mean time to recover.
	MTTRMillis float64
	// ErrorRate is the baseline error fraction when healthy.
	ErrorRate float64
	// Notes.
	Notes []string
}

// PricingModel is the local pricing model. All amounts are USD, marked
// ESTIMATE elsewhere; none of this is live billing data.
type PricingModel struct {
	// Currency is always "USD" in V1.
	Currency string
	// PerRequest is charged per request (e.g. Lambda, Workers, API calls).
	PerRequest float64
	// PerComputeUnit is charged per GB-second / CPU-ms of compute; the
	// service model documents the unit.
	PerComputeUnit float64
	// PerInstanceHour is charged per running instance per hour.
	PerInstanceHour float64
	// StorageGBMonth: per GB of stored data per month.
	StorageGBMonth float64
	// DatabaseGBMonth: per GB of database storage per month.
	DatabaseGBMonth float64
	// CacheGBMonth: per GB of cache memory per month.
	CacheGBMonth float64
	// QueueOp is per queue operation/message.
	QueueOp float64
	// EgressGB: per GB of internet egress.
	EgressGB float64
	// FreeTier: monthly allowances deducted before billing.
	FreeTier FreeTier
	// Notes: plan assumptions (which tier, which region class).
	Notes []string
}

// FreeTier describes monthly free allowances.
type FreeTier struct {
	Requests      float64
	ComputeUnits  float64
	StorageGB     float64
	InstanceHours float64
	QueueOps      float64
	EgressGB      float64
}

// Config is the user's typed configuration for a service instance.
// Unknown fields are rejected on resolution; zero values mean "use the
// catalog default".
type Config struct {
	Concurrency int
	QueueLimit  int
	Units       int
	// MemoryMB is honored by per-compute-unit priced services (Lambda,
	// Workers, Cloud Run): bigger memory → faster modeled service time and
	// higher compute cost.
	MemoryMB int
	// StorageGB seeds storage-cost estimation.
	StorageGB float64
	// HitRatio applies to caches (ElastiCache, Memorystore, KV, CDN).
	HitRatio float64
}

// resolved merges cfg onto a base spec.
func (c Config) resolved(base ConcurrencyBase) ConcurrencyBase {
	out := base
	if c.Concurrency > 0 {
		out.Concurrency = c.Concurrency
	}
	if c.QueueLimit > 0 {
		out.QueueLimit = c.QueueLimit
	}
	return out
}

// ConcurrencyBase is the default (concurrency, queueLimit) pair a service
// model exposes for config resolution.
type ConcurrencyBase struct {
	Concurrency int
	QueueLimit  int
}
