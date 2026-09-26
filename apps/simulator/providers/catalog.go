package providers

import "github.com/kekubhai/Loadline/apps/simulator/sim"

// Catalog is the built-in provider catalog, keyed "provider/service".
// Every number here is a documented modeling assumption for a mid-tier
// (non-enterprise) plan in a primary commercial region, not live pricing.
func Catalog() map[string]*GenericService {
	svcs := []*GenericService{

		// ---------------- AWS ----------------
		NewGenericService("aws", "ec2",
			"Virtual machines: fixed fleet, throughput-bound by instance size",
			CapacityModel{
				Concurrency: 200, ServiceTimeMS: 10, QueueLimit: 100, ModeledRPS: 20000,
				Notes: []string{"assumes c6i.xlarge-class, 200 concurrent in-flight, 10ms app service time"},
			},
			LatencyModel{Default: 10, Notes: []string{"application compute per request, 10ms assumed"}},
			ScalingModel{Kind: "static", Units: 2, MinUnits: 1, MaxUnits: 0, ScaleUnitCost: 0.125 * 730,
				Notes: []string{"2 instances assumed; each additional unit adds its instance-hour cost"}},
			FailureModel{MTTRMillis: 60_000, ErrorRate: 0.001,
				Notes: []string{"instance crash modeled; MTTR = replace instance"}},
			PricingModel{Currency: "USD", PerInstanceHour: 0.125, EgressGB: 0.09,
				FreeTier: FreeTier{InstanceHours: 0, EgressGB: 100},
				Notes:    []string{"c6i.xlarge Linux on-demand ESTIMATE, us-east class"}}),

		NewGenericService("aws", "lambda",
			"Serverless functions: per-request billing, concurrency by memory",
			CapacityModel{
				Concurrency: 100, ServiceTimeMS: 25, QueueLimit: 0, ModeledRPS: 4000,
				Notes: []string{"1000 concurrent executions at 512MB ÷ 25ms mean; no queue (serverless buffers)"},
			},
			LatencyModel{Default: 25, Notes: []string{"cold starts folded into mean; 512MB assumption"}},
			ScalingModel{Kind: "serverless", Units: 0, ScaleUnitCost: 0,
				Notes: []string{"scales with traffic; concurrency limit 1000 assumed"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.005,
				Notes: []string{"throttling modeled as errors when concurrency exhausted"}},
			PricingModel{Currency: "USD", PerRequest: 0.0000002, PerComputeUnit: 0.0000166667,
				FreeTier: FreeTier{Requests: 1_000_000, ComputeUnits: 400_000},
				Notes:    []string{"x86 on-demand ESTIMATE; GB-second billing at 512MB"}}),

		NewGenericService("aws", "rds",
			"Managed SQL: connection-bound, read/write priced ops",
			CapacityModel{
				Concurrency: 8, ServiceTimeMS: 5, QueueLimit: 50, ModeledRPS: 1600,
				Notes: []string{"db.m5.large-class, ~8 effective connections, 5ms per query"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpRead: 5, sim.OpWrite: 8}, Default: 5,
				Notes: []string{"simple indexed query class"}},
			ScalingModel{Kind: "static", Units: 1, MinUnits: 1, ScaleUnitCost: 0.24 * 730,
				Notes: []string{"single-AZ; multi-AZ doubles instance cost"}},
			FailureModel{MTTRMillis: 120_000, ErrorRate: 0.0005,
				Notes: []string{"failover modeled at 2 minutes"}},
			PricingModel{Currency: "USD", PerInstanceHour: 0.24, DatabaseGBMonth: 0.115, EgressGB: 0.09,
				Notes: []string{"db.m5.large MySQL on-demand ESTIMATE + storage"}}),

		NewGenericService("aws", "elasticache",
			"Managed Redis: memory-bound cache",
			CapacityModel{
				Concurrency: 1000, ServiceTimeMS: 0.5, QueueLimit: 200, ModeledRPS: 2_000_000,
				Notes: []string{"cache.m5.large-class; sub-millisecond ops"},
			},
			LatencyModel{Default: 0.5, Notes: []string{"in-memory op"}},
			ScalingModel{Kind: "static", Units: 1, MinUnits: 1, ScaleUnitCost: 0.155 * 730,
				Notes: []string{"single node assumed; replication groups scale reads"}},
			FailureModel{MTTRMillis: 30_000, ErrorRate: 0.0001, Notes: []string{"node replacement"}},
			PricingModel{Currency: "USD", PerInstanceHour: 0.155, CacheGBMonth: 0,
				Notes: []string{"cache.m5.large on-demand ESTIMATE; memory included in instance"}}),

		NewGenericService("aws", "sqs",
			"Managed queue: buffered message delivery",
			CapacityModel{
				Concurrency: 0, ServiceTimeMS: 1, QueueLimit: 0, ModeledRPS: 10_000,
				Notes: []string{"effectively unbounded buffer; 10k ops/s soft ceiling assumed"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpEnqueue: 1, sim.OpDequeue: 1}, Default: 1, Notes: []string{"standard queue API latency"}},
			ScalingModel{Kind: "serverless", Notes: []string{"fully managed"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.0001, Notes: []string{"managed availability"}},
			PricingModel{Currency: "USD", QueueOp: 0.0000004,
				FreeTier: FreeTier{QueueOps: 1_000_000},
				Notes:    []string{"standard queue ESTIMATE per request"}}),

		NewGenericService("aws", "s3",
			"Object storage: get/put priced, egress heavy",
			CapacityModel{
				Concurrency: 500, ServiceTimeMS: 30, QueueLimit: 0, ModeledRPS: 16_000,
				Notes: []string{"per-object latency 30ms; scales horizontally"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpGet: 30, sim.OpPut: 50}, Default: 30, Notes: []string{"first-byte latency class"}},
			ScalingModel{Kind: "serverless", Notes: []string{"fully managed"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.0001, Notes: []string{"11x9 durability; availability modeled"}},
			PricingModel{Currency: "USD", StorageGBMonth: 0.023, EgressGB: 0.09,
				PerRequest: 0.0000004,
				FreeTier:   FreeTier{StorageGB: 5, Requests: 2000, EgressGB: 100},
				Notes:      []string{"S3 standard ESTIMATE; GET-class pricing"}}),

		NewGenericService("aws", "cloudfront",
			"CDN: edge caching, egress included in price",
			CapacityModel{
				Concurrency: 0, ServiceTimeMS: 10, QueueLimit: 0, ModeledRPS: 500_000,
				Notes: []string{"edge-served; effectively unbounded concurrency assumed"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpTransit: 10}, Default: 10, Notes: []string{"edge to viewer RTT class"}},
			ScalingModel{Kind: "serverless", Notes: []string{"fully managed"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.0005, Notes: []string{"edge PoP failures hidden"}},
			PricingModel{Currency: "USD", EgressGB: 0.085, PerRequest: 0.0000012,
				FreeTier: FreeTier{EgressGB: 1000, Requests: 10_000_000},
				Notes:    []string{"North America class ESTIMATE; 20% assumed cache-miss egress"}}),

		// ---------------- Cloudflare ----------------
		NewGenericService("cloudflare", "workers",
			"Edge serverless: per-request CPU-ms billing",
			CapacityModel{
				Concurrency: 0, ServiceTimeMS: 5, QueueLimit: 0, ModeledRPS: 1_000_000,
				Notes: []string{"isolate-per-request; effectively unbounded at V1 assumptions"},
			},
			LatencyModel{Default: 5, Notes: []string{"short CPU-burst scripts assumed"}},
			ScalingModel{Kind: "serverless", Notes: []string{"auto-everywhere"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.001, Notes: []string{"per-colo failures hidden"}},
			PricingModel{Currency: "USD", PerRequest: 0.0000003, PerComputeUnit: 0.000002,
				FreeTier: FreeTier{Requests: 10_000_000, ComputeUnits: 30_000_000},
				Notes:    []string{"paid plan ESTIMATE: 30M CPU-ms included, then $0.02/GB-s class"}}),

		NewGenericService("cloudflare", "kv",
			"Edge key-value store: eventually consistent reads",
			CapacityModel{
				Concurrency: 0, ServiceTimeMS: 10, QueueLimit: 0, ModeledRPS: 500_000,
				Notes: []string{"edge-cached reads; writes propagate eventually"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpRead: 10, sim.OpWrite: 50}, Default: 10,
				Notes: []string{"cached read 10ms, uncached/write 50ms assumed"}},
			ScalingModel{Kind: "serverless", Notes: []string{"fully managed"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.0005, Notes: []string{"edge replication"}},
			PricingModel{Currency: "USD", PerRequest: 0.0000005, StorageGBMonth: 0.50,
				FreeTier: FreeTier{Requests: 100_000, StorageGB: 1},
				Notes:    []string{"Workers KV paid-plan ESTIMATE: read/write/delete per million"}}),

		NewGenericService("cloudflare", "r2",
			"S3-compatible object storage with zero egress fees",
			CapacityModel{
				Concurrency: 500, ServiceTimeMS: 25, QueueLimit: 0, ModeledRPS: 20_000,
				Notes: []string{"per-object latency 25ms; horizontal"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpGet: 25, sim.OpPut: 40}, Default: 25, Notes: []string{"class A/B ops"}},
			ScalingModel{Kind: "serverless", Notes: []string{"fully managed"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.0002, Notes: []string{"managed replication"}},
			PricingModel{Currency: "USD", StorageGBMonth: 0.015, PerRequest: 0.00000045,
				FreeTier: FreeTier{StorageGB: 10, Requests: 1_000_000},
				Notes:    []string{"R2 ESTIMATE: Class B (read) $0.36/million"}}),

		NewGenericService("cloudflare", "queues",
			"Managed message queue with consumer workers",
			CapacityModel{
				Concurrency: 0, ServiceTimeMS: 2, QueueLimit: 0, ModeledRPS: 5_000,
				Notes: []string{"delivery through consumer workers assumed"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpEnqueue: 2}, Default: 2, Notes: []string{"enqueue latency; delivery async"}},
			ScalingModel{Kind: "serverless", Notes: []string{"fully managed"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.001, Notes: []string{"at-least-once delivery"}},
			PricingModel{Currency: "USD", QueueOp: 0.0000004,
				FreeTier: FreeTier{QueueOps: 1_000_000},
				Notes:    []string{"Queues paid-plan ESTIMATE per million operations"}}),

		NewGenericService("cloudflare", "durable_objects",
			"Strongly consistent coordinated objects",
			CapacityModel{
				Concurrency: 100, ServiceTimeMS: 2, QueueLimit: 100, ModeledRPS: 50_000,
				Notes: []string{"per-object serialization limits concurrency per object"},
			},
			LatencyModel{Default: 2, Notes: []string{"single-object coordination assumed"}},
			ScalingModel{Kind: "autoscaled", MinUnits: 0, MaxUnits: 0, Notes: []string{"objects distributed automatically"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.001, Notes: []string{"per-object isolation"}},
			PricingModel{Currency: "USD", PerRequest: 0.000000125, PerComputeUnit: 0.000002,
				FreeTier: FreeTier{Requests: 0, ComputeUnits: 13_000_000},
				Notes:    []string{"paid plan ESTIMATE: duration billed per GB-s active"}}),

		// ---------------- GCP ----------------
		NewGenericService("gcp", "cloud_run",
			"Serverless containers: per-request billing, concurrency per instance",
			CapacityModel{
				Concurrency: 80, ServiceTimeMS: 20, QueueLimit: 0, ModeledRPS: 4000,
				Notes: []string{"80 concurrent per instance; instances autoscale"},
			},
			LatencyModel{Default: 20, Notes: []string{"container cold starts folded into mean"}},
			ScalingModel{Kind: "serverless", Notes: []string{"instance autoscaling; max instances assumed unbounded"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.005, Notes: []string{"instance crashes retried"}},
			PricingModel{Currency: "USD", PerRequest: 0.0000004, PerComputeUnit: 0.0000024,
				FreeTier: FreeTier{Requests: 2_000_000, ComputeUnits: 360_000},
				Notes:    []string{"vCPU-second + GB-second ESTIMATE blended at 512MB"}}),

		NewGenericService("gcp", "cloud_sql",
			"Managed SQL: connection-bound database",
			CapacityModel{
				Concurrency: 8, ServiceTimeMS: 5, QueueLimit: 50, ModeledRPS: 1600,
				Notes: []string{"db-custom-2-4 class, ~8 effective connections, 5ms per query"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpRead: 5, sim.OpWrite: 8}, Default: 5, Notes: []string{"simple indexed query class"}},
			ScalingModel{Kind: "static", Units: 1, MinUnits: 1, ScaleUnitCost: 0.25 * 730, Notes: []string{"HA doubles cost"}},
			FailureModel{MTTRMillis: 120_000, ErrorRate: 0.0005, Notes: []string{"failover ~2 minutes"}},
			PricingModel{Currency: "USD", PerInstanceHour: 0.25, DatabaseGBMonth: 0.17, EgressGB: 0.12,
				Notes: []string{"db-custom-2-4 MySQL ESTIMATE + SSD storage"}}),

		NewGenericService("gcp", "memorystore",
			"Managed Redis: memory-bound cache",
			CapacityModel{
				Concurrency: 1000, ServiceTimeMS: 0.5, QueueLimit: 200, ModeledRPS: 2_000_000,
				Notes: []string{"standard 1GB class; sub-millisecond ops"},
			},
			LatencyModel{Default: 0.5, Notes: []string{"in-memory op"}},
			ScalingModel{Kind: "static", Units: 1, MinUnits: 1, ScaleUnitCost: 0.21 * 730, Notes: []string{"1GB standard tier ESTIMATE"}},
			FailureModel{MTTRMillis: 30_000, ErrorRate: 0.0001, Notes: []string{"node replacement"}},
			PricingModel{Currency: "USD", PerInstanceHour: 0.21, Notes: []string{"standard tier ESTIMATE"}}),

		NewGenericService("gcp", "pub_sub",
			"Managed messaging: at-least-once delivery",
			CapacityModel{
				Concurrency: 0, ServiceTimeMS: 2, QueueLimit: 0, ModeledRPS: 50_000,
				Notes: []string{"effectively unbounded buffer"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpEnqueue: 2}, Default: 2, Notes: []string{"publish latency class"}},
			ScalingModel{Kind: "serverless", Notes: []string{"fully managed"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.0002, Notes: []string{"managed availability"}},
			PricingModel{Currency: "USD", QueueOp: 0.00000004, StorageGBMonth: 0.27,
				FreeTier: FreeTier{QueueOps: 10_000_000},
				Notes:    []string{"$40/TB message delivery ESTIMATE → per-million"}}),

		NewGenericService("gcp", "cloud_storage",
			"Object storage: get/put priced, egress heavy",
			CapacityModel{
				Concurrency: 500, ServiceTimeMS: 30, QueueLimit: 0, ModeledRPS: 16_000,
				Notes: []string{"per-object latency 30ms; horizontal"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpGet: 30, sim.OpPut: 50}, Default: 30, Notes: []string{"first-byte latency class"}},
			ScalingModel{Kind: "serverless", Notes: []string{"fully managed"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.0001, Notes: []string{"managed availability"}},
			PricingModel{Currency: "USD", StorageGBMonth: 0.020, PerRequest: 0.0000004, EgressGB: 0.12,
				FreeTier: FreeTier{StorageGB: 5, QueueOps: 0, Requests: 5000},
				Notes:    []string{"standard class ESTIMATE"}}),

		NewGenericService("gcp", "cloud_cdn",
			"CDN: edge caching backed by Cloud Load Balancing",
			CapacityModel{
				Concurrency: 0, ServiceTimeMS: 10, QueueLimit: 0, ModeledRPS: 500_000,
				Notes: []string{"edge-served; effectively unbounded concurrency assumed"},
			},
			LatencyModel{PerOp: map[sim.Op]float64{sim.OpTransit: 10}, Default: 10, Notes: []string{"edge RTT class"}},
			ScalingModel{Kind: "serverless", Notes: []string{"fully managed"}},
			FailureModel{MTTRMillis: 0, ErrorRate: 0.0005, Notes: []string{"edge PoP failures hidden"}},
			PricingModel{Currency: "USD", EgressGB: 0.12, PerRequest: 0.0000012,
				FreeTier: FreeTier{EgressGB: 0, Requests: 0},
				Notes:    []string{"cache-egress ESTIMATE; load-balancer forwarding costs not modeled"}}),
	}

	out := make(map[string]*GenericService, len(svcs))
	for _, s := range svcs {
		out[s.Provider()+"/"+s.Service()] = s
	}
	return out
}
