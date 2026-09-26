package providers

import (
	"fmt"

	"github.com/kekubhai/Loadline/apps/simulator/sim"
)

// GenericService is the single concrete ServiceModel implementation the
// whole catalog instantiates with different defaults. One implementation
// keeps behavior uniform; only the numbers differ per service.
type GenericService struct {
	provider string
	service  string
	summary  string

	capacity CapacityModel
	latency  LatencyModel
	scaling  ScalingModel
	failure  FailureModel
	pricing  PricingModel
}

// NewGenericService assembles a service model from its facet data.
func NewGenericService(
	provider, service, summary string,
	cap CapacityModel, lat LatencyModel, sc ScalingModel,
	fail FailureModel, price PricingModel,
) *GenericService {
	if price.Currency == "" {
		price.Currency = "USD"
	}
	return &GenericService{
		provider: provider, service: service, summary: summary,
		capacity: cap, latency: lat, scaling: sc,
		failure: fail, pricing: price,
	}
}

func (g *GenericService) Provider() string        { return g.provider }
func (g *GenericService) Service() string         { return g.service }
func (g *GenericService) Summary() string         { return g.summary }
func (g *GenericService) Capacity() CapacityModel { return g.capacity }
func (g *GenericService) Latency() LatencyModel   { return g.latency }
func (g *GenericService) Scaling() ScalingModel   { return g.scaling }
func (g *GenericService) Failure() FailureModel   { return g.failure }
func (g *GenericService) Pricing() PricingModel   { return g.pricing }

// BuildSpec renders the service as a generic sim.ComponentSpec, applying
// typed config overrides. The simulator sees only generic behavior — no
// provider leakage.
func (g *GenericService) BuildSpec(id string, cfg Config) (sim.ComponentSpec, error) {
	if id == "" {
		return sim.ComponentSpec{}, fmt.Errorf("providers: component ID must not be empty")
	}

	kind, err := kindForService(g.provider, g.service)
	if err != nil {
		return sim.ComponentSpec{}, err
	}

	// Effective concurrency/queue: user config over catalog defaults.
	eff := cfg.resolved(ConcurrencyBase{
		Concurrency: g.capacity.Concurrency,
		QueueLimit:  g.capacity.QueueLimit,
	})

	spec := sim.ComponentSpec{
		ID:                       id,
		Kind:                     kind,
		ServiceTimeMillis:        map[sim.Op]float64{},
		DefaultServiceTimeMillis: g.latency.Default,
		Concurrency:              eff.Concurrency,
		QueueLimit:               eff.QueueLimit,
		CapacityRPS:              g.capacity.ModeledRPS,
		HitRatio:                 cfg.HitRatio,
	}
	for op, ms := range g.latency.PerOp {
		spec.ServiceTimeMillis[op] = ms
	}

	// Memory sizing: bigger allocation → faster modeled execution on
	// per-compute-unit services (documented assumption, not a benchmark).
	if cfg.MemoryMB > 0 && g.pricing.PerComputeUnit > 0 {
		ref := 512.0
		factor := ref / float64(cfg.MemoryMB)
		if factor < 1 {
			factor = 1
		}
		spec.DefaultServiceTimeMillis = g.latency.Default * factor
		for op, ms := range spec.ServiceTimeMillis {
			spec.ServiceTimeMillis[op] = ms * factor
		}
	}
	return spec, nil
}

// kindForService maps (provider, service) to the generic component kind.
// The mapping lives here — the sim package has no provider knowledge.
func kindForService(provider, service string) (sim.ComponentKind, error) {
	type key struct{ p, s string }
	switch (key{provider, service}) {
	case key{"aws", "lambda"}, key{"gcp", "cloud_run"}, key{"cloudflare", "workers"},
		key{"aws", "ec2"}, key{"cloudflare", "durable_objects"}:
		return sim.KindAPIServer, nil
	case key{"gcp", "cloud_cdn"}, key{"aws", "cloudfront"}:
		return sim.KindNetwork, nil
	case key{"aws", "elasticache"}, key{"gcp", "memorystore"}:
		return sim.KindCache, nil
	case key{"aws", "sqs"}, key{"gcp", "pub_sub"}, key{"cloudflare", "queues"}:
		return sim.KindQueue, nil
	case key{"aws", "rds"}, key{"gcp", "cloud_sql"}:
		return sim.KindDatabase, nil
	case key{"aws", "s3"}, key{"gcp", "cloud_storage"}, key{"cloudflare", "r2"}:
		return sim.KindObjectStorage, nil
	case key{"cloudflare", "kv"}:
		return sim.KindCache, nil
	}
	return "", fmt.Errorf("providers: no kind mapping for %s/%s", provider, service)
}
