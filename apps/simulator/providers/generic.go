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

	kind, err := ResolveKind(g.provider, g.service, cfg.Kind)
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

// alternateRoles lists the extra generic kinds a catalog service may play
// besides its primary kindForService mapping. Compute services that run
// arbitrary code can be deployed as a queue's consumer (Cloudflare Queues
// consumer Workers, Lambda event-source mappings on SQS, Cloud Run push
// subscriptions on Pub/Sub, an EC2 worker fleet). Coordination/storage
// services cannot: a Durable Object is not a queue consumer.
var alternateRoles = map[string][]sim.ComponentKind{
	"cloudflare/workers": {sim.KindWorker},
	"aws/lambda":         {sim.KindWorker},
	"aws/ec2":            {sim.KindWorker},
	"gcp/cloud_run":      {sim.KindWorker},
}

// ResolveKind returns the generic kind a provider-backed component runs as.
// requested is the kind declared on the architecture document ("" when the
// caller has none). The service's primary kind is used when nothing is
// requested or when the request matches it; an alternate role is honored
// when the service supports it; anything else is an error, so a document
// that asks for an impossible role fails loudly instead of being silently
// rewritten into a different shape.
func ResolveKind(provider, service string, requested sim.ComponentKind) (sim.ComponentKind, error) {
	primary, err := kindForService(provider, service)
	if err != nil {
		return "", err
	}
	if requested == "" || requested == primary {
		return primary, nil
	}
	for _, k := range alternateRoles[provider+"/"+service] {
		if k == requested {
			return k, nil
		}
	}
	return "", fmt.Errorf("providers: %s/%s cannot run as %s (supported: %s)",
		provider, service, requested, supportedRoles(provider, service, primary))
}

// SupportedKinds lists every generic kind the service can run as, primary
// first. Used by the catalog API so editors can offer the valid roles.
func SupportedKinds(provider, service string) ([]sim.ComponentKind, error) {
	primary, err := kindForService(provider, service)
	if err != nil {
		return nil, err
	}
	return append([]sim.ComponentKind{primary}, alternateRoles[provider+"/"+service]...), nil
}

func supportedRoles(provider, service string, primary sim.ComponentKind) string {
	out := string(primary)
	for _, k := range alternateRoles[provider+"/"+service] {
		out += ", " + string(k)
	}
	return out
}
