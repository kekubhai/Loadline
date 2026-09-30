/**
 * LOADLINE — Explore Systems gallery.
 *
 * A catalog of recognizable production architectures (social graphs,
 * inference fleets, payment rails, streaming pipelines…) expressed with
 * the same primitives as every other architecture: one client, provider
 * services from the simulator's catalog, and conditional links. They are
 * starting points, not claims: each entry states the system-design idea
 * it models and the load pattern that typically breaks it, and every
 * number the user sees still comes from a simulation run.
 *
 * Structural rules mirror sim.Architecture.Validate: exactly one client,
 * queues pair with exactly one terminal worker, no cycles, everything
 * reachable — explore.test.ts enforces this so a card can never load an
 * architecture the backend would reject.
 */
import type { MessageInitShape } from "@bufbuild/protobuf";
import {
  ArchitectureSchema,
  ComponentKind,
  ComponentSpecSchema,
  LinkSchema,
  create,
} from "@loadline/api";
import type { Architecture, ComponentSpec } from "@loadline/api";
import { SCHEMA_VERSION } from "./editor";
import type { EditorState } from "./editor";
import { templateState } from "./templates";
import type { ArchitectureTemplate } from "./templates";

export interface ExploreSystem {
  id: string;
  name: string;
  /** What this shape models and the load pattern that usually breaks it. */
  description: string;
  architecture: Architecture;
}

export interface ExploreCategory {
  id: string;
  label: string;
  systems: ExploreSystem[];
}

type ComponentInit = MessageInitShape<typeof ComponentSpecSchema> & { id: string };

function comp(init: ComponentInit): ComponentSpec {
  return create(ComponentSpecSchema, init);
}

function build(
  name: string,
  components: ComponentSpec[],
  links: [string, string][],
): Architecture {
  return create(ArchitectureSchema, {
    schemaVersion: SCHEMA_VERSION,
    name,
    components,
    links: links.map(([from, to]) => create(LinkSchema, { from, to })),
  });
}

/* ------------------------------------------------------------------ helpers -- */

/** Deterministic client node for every system. */
const client = () => comp({ id: "client", kind: ComponentKind.CLIENT });

/* --------------------------------------------------------------- categories -- */

export const EXPLORE_CATEGORIES: ExploreCategory[] = [
  {
    id: "social",
    label: "Social",
    systems: [
      {
        id: "facebook-tao",
        name: "Facebook · social graph",
        description:
          "API → memcached-style cache → MySQL, with blobs in object storage. Reads are absorbed by the cache: a cold cache roughly doubles database reads, which is the classic feed meltdown.",
        architecture: build("facebook-tao", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "graph-cache", kind: ComponentKind.CACHE, provider: "aws", service: "elasticache", hitRatio: 0.9 }),
          comp({ id: "graph-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 500 } }),
          comp({ id: "media", kind: ComponentKind.OBJECT_STORAGE, provider: "aws", service: "s3" }),
        ], [
          ["client", "api"],
          ["api", "graph-cache"],
          ["graph-cache", "graph-db"],
          ["api", "media"],
        ]),
      },
      {
        id: "instagram-feed",
        name: "Instagram · feed fan-out",
        description:
          "CDN in front of a feed service with a write-through queue: posts fan out asynchronously while reads hit the timeline cache. Break it by slowing the workers — feed staleness becomes queue growth.",
        architecture: build("instagram-feed", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "gcp", service: "cloud_cdn" }),
          comp({ id: "feed-api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "timeline", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.85 }),
          comp({ id: "feed-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 400 } }),
          comp({ id: "fanout-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "fanout-worker", kind: ComponentKind.WORKER, concurrency: 16, defaultServiceTimeMillis: 30 }),
        ], [
          ["client", "edge"],
          ["edge", "feed-api"],
          ["feed-api", "timeline"],
          ["timeline", "feed-db"],
          ["feed-api", "fanout-queue"],
          ["fanout-queue", "fanout-worker"],
        ]),
      },
      {
        id: "discord-messages",
        name: "Discord · message pipeline",
        description:
          "Gateway API buffering message persistence through a queue into a wide-column-style store. Inject a worker slowdown: delivery latency climbs while the API stays healthy.",
        architecture: build("discord-messages", [
          client(),
          comp({ id: "gateway", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { memoryMb: 4096, units: 4 } }),
          comp({ id: "presence", kind: ComponentKind.CACHE, provider: "aws", service: "elasticache", hitRatio: 0.85 }),
          comp({ id: "messages", kind: ComponentKind.QUEUE, provider: "aws", service: "sqs" }),
          comp({ id: "persist-worker", kind: ComponentKind.WORKER, concurrency: 24, defaultServiceTimeMillis: 25 }),
          comp({ id: "message-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 600 } }),
        ], [
          ["client", "gateway"],
          ["gateway", "presence"],
          ["presence", "message-db"],
          ["gateway", "messages"],
          ["messages", "persist-worker"],
        ]),
      },
      {
        id: "reddit-hot-ranker",
        name: "Reddit · hot ranking",
        description:
          "CDN plus a scored-feed cache at 90% hit rate over a vote-heavy database. The break is a cache invalidation storm during a viral post: miss traffic saturates the database.",
        architecture: build("reddit-hot-ranker", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "gcp", service: "cloud_cdn" }),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 512 } }),
          comp({ id: "ranked", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.9 }),
          comp({ id: "votes-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 400 } }),
          comp({ id: "vote-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "rank-worker", kind: ComponentKind.WORKER, concurrency: 16, defaultServiceTimeMillis: 35 }),
        ], [
          ["client", "edge"],
          ["edge", "api"],
          ["api", "ranked"],
          ["ranked", "votes-db"],
          ["api", "vote-queue"],
          ["vote-queue", "rank-worker"],
        ]),
      },
      {
        id: "linkedin-graph",
        name: "LinkedIn · connection graph",
        description:
          "Graph reads served from a degree-of-separation cache over a connection database, with profile objects in storage. Lower the cache hit ratio and watch traversal load migrate to the database.",
        architecture: build("linkedin-graph", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "degrees", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.85 }),
          comp({ id: "conn-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 500 } }),
          comp({ id: "profiles", kind: ComponentKind.OBJECT_STORAGE, provider: "gcp", service: "cloud_storage" }),
        ], [
          ["client", "api"],
          ["api", "degrees"],
          ["degrees", "conn-db"],
          ["api", "profiles"],
        ]),
      },
    ],
  },
  {
    id: "ai",
    label: "AI",
    systems: [
      {
        id: "openai-inference",
        name: "OpenAI · inference fleet",
        description:
          "Requests buffered through a queue into a small GPU-worker pool with long (150ms) service times. The classic AI break: concurrency-capped workers, unbounded queue growth, timeouts at the client.",
        architecture: build("openai-inference", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "gpu-queue", kind: ComponentKind.QUEUE, provider: "aws", service: "sqs" }),
          comp({ id: "gpu-worker", kind: ComponentKind.WORKER, concurrency: 12, defaultServiceTimeMillis: 150 }),
        ], [
          ["client", "api"],
          ["api", "gpu-queue"],
          ["gpu-queue", "gpu-worker"],
        ]),
      },
      {
        id: "anthropic-streaming",
        name: "Anthropic · streaming completions",
        description:
          "Edge-routed streaming API feeding an async completion pipeline. Long-lived generations occupy workers for 120ms+; raise the load and watch the queue become the latency floor.",
        architecture: build("anthropic-streaming", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "aws", service: "cloudfront" }),
          comp({ id: "stream-api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "gen-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "gen-worker", kind: ComponentKind.WORKER, concurrency: 24, defaultServiceTimeMillis: 120 }),
        ], [
          ["client", "edge"],
          ["edge", "stream-api"],
          ["stream-api", "gen-queue"],
          ["gen-queue", "gen-worker"],
        ]),
      },
      {
        id: "gemini-multimodal",
        name: "Gemini · multimodal ingest",
        description:
          "Prompts and images land in object storage while the API synchronously serves cached context. Object GETs at 60ms class dominate: widen the payload and the storage leg drags p99.",
        architecture: build("gemini-multimodal", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "gcp", service: "cloud_cdn" }),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 2048 } }),
          comp({ id: "context", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.7 }),
          comp({ id: "ctx-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 300 } }),
          comp({ id: "media", kind: ComponentKind.OBJECT_STORAGE, provider: "gcp", service: "cloud_storage" }),
        ], [
          ["client", "edge"],
          ["edge", "api"],
          ["api", "context"],
          ["context", "ctx-db"],
          ["api", "media"],
        ]),
      },
      {
        id: "perplexity-rag",
        name: "Perplexity · RAG pipeline",
        description:
          "Retrieval-augmented answers: an index cache at 75% hit rate over a document store, with corpora in object storage. The break is index churn — lower the hit ratio and the database collapses.",
        architecture: build("perplexity-rag", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { memoryMb: 4096, units: 3 } }),
          comp({ id: "index", kind: ComponentKind.CACHE, provider: "aws", service: "elasticache", hitRatio: 0.75 }),
          comp({ id: "doc-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 400 } }),
          comp({ id: "corpus", kind: ComponentKind.OBJECT_STORAGE, provider: "aws", service: "s3" }),
        ], [
          ["client", "api"],
          ["api", "index"],
          ["index", "doc-db"],
          ["api", "corpus"],
        ]),
      },
      {
        id: "ai-coding-agents",
        name: "AI coding · agent platform",
        description:
          "Agent sessions enqueue long tool-running jobs; the API stays interactive by serving state from the database directly. Slow the agents and the job queue — not the API — is what degrades.",
        architecture: build("ai-coding-agents", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "agent-queue", kind: ComponentKind.QUEUE, provider: "aws", service: "sqs" }),
          comp({ id: "agent-worker", kind: ComponentKind.WORKER, concurrency: 32, defaultServiceTimeMillis: 80 }),
          comp({ id: "state-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 200 } }),
        ], [
          ["client", "api"],
          ["api", "agent-queue"],
          ["agent-queue", "agent-worker"],
          ["api", "state-db"],
        ]),
      },
    ],
  },
  {
    id: "infrastructure",
    label: "Infrastructure",
    systems: [
      {
        id: "cloudflare-anycast",
        name: "Cloudflare · anycast edge",
        description:
          "Edge network in front of Workers with KV for hot state and R2 for objects. Nearly unbounded edge concurrency — the ceiling shows up in KV latency and R2 reads, not the compute tier.",
        architecture: build("cloudflare-anycast", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "aws", service: "cloudfront" }),
          comp({ id: "worker", kind: ComponentKind.API_SERVER, provider: "cloudflare", service: "workers" }),
          comp({ id: "hot-state", kind: ComponentKind.CACHE, provider: "cloudflare", service: "kv", hitRatio: 0.9 }),
          comp({ id: "objects", kind: ComponentKind.OBJECT_STORAGE, provider: "cloudflare", service: "r2" }),
          comp({ id: "origin-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 200 } }),
        ], [
          ["client", "edge"],
          ["edge", "worker"],
          ["worker", "hot-state"],
          ["hot-state", "origin-db"],
          ["worker", "objects"],
        ]),
      },
      {
        id: "aws-cdn-origin",
        name: "AWS · CDN with origin shield",
        description:
          "CloudFront absorbing traffic into a fixed EC2 fleet and RDS. Measure how much load the edge removes before the origin notices — then drop the assumed hit rate and watch the fleet saturate.",
        architecture: build("aws-cdn-origin", [
          client(),
          comp({ id: "cdn", kind: ComponentKind.NETWORK, provider: "aws", service: "cloudfront" }),
          comp({ id: "origin", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { memoryMb: 4096, units: 4 } }),
          comp({ id: "db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 500 } }),
          comp({ id: "assets", kind: ComponentKind.OBJECT_STORAGE, provider: "aws", service: "s3" }),
        ], [
          ["client", "cdn"],
          ["cdn", "origin"],
          ["origin", "db"],
          ["origin", "assets"],
        ]),
      },
      {
        id: "edge-rendering",
        name: "Edge-rendered app",
        description:
          "CDN → edge middleware → KV session cache → regional database. Every request pays an edge hop plus a KV read: the p99 story is the sum of small latencies, not one big service time.",
        architecture: build("edge-rendering", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "gcp", service: "cloud_cdn" }),
          comp({ id: "middleware", kind: ComponentKind.API_SERVER, provider: "cloudflare", service: "workers" }),
          comp({ id: "sessions", kind: ComponentKind.CACHE, provider: "cloudflare", service: "kv", hitRatio: 0.9 }),
          comp({ id: "app-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 200 } }),
        ], [
          ["client", "edge"],
          ["edge", "middleware"],
          ["middleware", "sessions"],
          ["sessions", "app-db"],
        ]),
      },
      {
        id: "regional-microservices",
        name: "Regional microservices",
        description:
          "A gateway EC2 fleet fronting a serverless service tier with a shared cache and database. The gateway is the multiplexer: saturate it and every downstream service starves together.",
        architecture: build("regional-microservices", [
          client(),
          comp({ id: "gateway", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { memoryMb: 4096, units: 3 } }),
          comp({ id: "svc", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 512 } }),
          comp({ id: "shared-cache", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.8 }),
          comp({ id: "shared-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 400 } }),
        ], [
          ["client", "gateway"],
          ["gateway", "svc"],
          ["svc", "shared-cache"],
          ["shared-cache", "shared-db"],
        ]),
      },
      {
        id: "k8s-autoscaled",
        name: "Kubernetes · autoscaled cluster",
        description:
          "A horizontally scaled node pool (6 replicas) behind an edge network with a connection-bound database. Scale the replicas in the inspector and watch headroom move; the DB is the real ceiling.",
        architecture: build("k8s-autoscaled", [
          client(),
          comp({ id: "ingress", kind: ComponentKind.NETWORK, provider: "aws", service: "cloudfront" }),
          comp({ id: "pods", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { units: 6, memoryMb: 4096 }, concurrency: 150, queueLimit: 200, defaultServiceTimeMillis: 8 }),
          comp({ id: "db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 400 } }),
        ], [
          ["client", "ingress"],
          ["ingress", "pods"],
          ["pods", "db"],
        ]),
      },
    ],
  },
  {
    id: "fintech",
    label: "Fintech",
    systems: [
      {
        id: "stripe-payment-api",
        name: "Stripe · payment API",
        description:
          "Write-heavy payment API straight into a transactional database, with an async ledger worker. Reads are scarce: the database write path is the bottleneck, and queueing shows up as authorization latency.",
        architecture: build("stripe-payment-api", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "ledger-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 300 } }),
          comp({ id: "ledger-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "ledger-worker", kind: ComponentKind.WORKER, concurrency: 12, defaultServiceTimeMillis: 45 }),
        ], [
          ["client", "api"],
          ["api", "ledger-db"],
          ["api", "ledger-queue"],
          ["ledger-queue", "ledger-worker"],
        ]),
      },
      {
        id: "coinbase-ledger",
        name: "Coinbase · exchange ledger",
        description:
          "Trades mutate an append-only ledger behind a modest cache, with balance updates flowing through a queue. Inject database latency: order confirmations stall while the API still reports green.",
        architecture: build("coinbase-ledger", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { memoryMb: 4096, units: 3 } }),
          comp({ id: "quotes", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.6 }),
          comp({ id: "ledger", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 400 } }),
          comp({ id: "settle-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "settle-worker", kind: ComponentKind.WORKER, concurrency: 16, defaultServiceTimeMillis: 40 }),
        ], [
          ["client", "api"],
          ["api", "quotes"],
          ["quotes", "ledger"],
          ["api", "settle-queue"],
          ["settle-queue", "settle-worker"],
        ]),
      },
      {
        id: "paypal-wallet",
        name: "PayPal · wallet service",
        description:
          "Edge-fronted wallet API with a balance cache, a transactional database, and an async fraud queue. The break is a cache-cold spike during a flash sale: database connections are only 8 wide.",
        architecture: build("paypal-wallet", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "aws", service: "cloudfront" }),
          comp({ id: "wallet-api", kind: ComponentKind.API_SERVER, provider: "aws", service: "lambda", config: { memoryMb: 1024 } }),
          comp({ id: "balances", kind: ComponentKind.CACHE, provider: "aws", service: "elasticache", hitRatio: 0.7 }),
          comp({ id: "txn-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 400 } }),
          comp({ id: "fraud-queue", kind: ComponentKind.QUEUE, provider: "aws", service: "sqs" }),
          comp({ id: "fraud-worker", kind: ComponentKind.WORKER, concurrency: 16, defaultServiceTimeMillis: 50 }),
        ], [
          ["client", "edge"],
          ["edge", "wallet-api"],
          ["wallet-api", "balances"],
          ["balances", "txn-db"],
          ["wallet-api", "fraud-queue"],
          ["fraud-queue", "fraud-worker"],
        ]),
      },
      {
        id: "block-payouts",
        name: "Block · seller payouts",
        description:
          "Payout requests are enqueued and drained by a slow, throughput-capped worker pool — correctness over speed. Lower worker concurrency and watch the queue depth become the backlog metric.",
        architecture: build("block-payouts", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "aws", service: "lambda", config: { memoryMb: 512 } }),
          comp({ id: "payout-queue", kind: ComponentKind.QUEUE, provider: "aws", service: "sqs" }),
          comp({ id: "payout-worker", kind: ComponentKind.WORKER, concurrency: 20, defaultServiceTimeMillis: 60 }),
          comp({ id: "payout-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 200 } }),
        ], [
          ["client", "api"],
          ["api", "payout-queue"],
          ["payout-queue", "payout-worker"],
          ["api", "payout-db"],
        ]),
      },
      {
        id: "webhook-fanout",
        name: "Stripe · webhook fan-out",
        description:
          "Event notifications buffered through Pub/Sub into a large worker pool with per-delivery retries. The system's whole health is queue drain rate: slow workers and delivery SLAs slide.",
        architecture: build("webhook-fanout", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 512 } }),
          comp({ id: "event-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 150 } }),
          comp({ id: "event-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "deliver-worker", kind: ComponentKind.WORKER, concurrency: 32, defaultServiceTimeMillis: 30 }),
        ], [
          ["client", "api"],
          ["api", "event-db"],
          ["api", "event-queue"],
          ["event-queue", "deliver-worker"],
        ]),
      },
    ],
  },
  {
    id: "marketplaces",
    label: "Marketplaces",
    systems: [
      {
        id: "uber-geo",
        name: "Uber · geospatial matching",
        description:
          "A large compute fleet serving location reads from a geo cache into a sharded database. The fleet is sized for peak surge; the database is not — raise the load and watch which saturates first.",
        architecture: build("uber-geo", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { memoryMb: 8192, units: 6 }, concurrency: 240, defaultServiceTimeMillis: 6 }),
          comp({ id: "geo-cache", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.85 }),
          comp({ id: "geo-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 500 } }),
        ], [
          ["client", "api"],
          ["api", "geo-cache"],
          ["geo-cache", "geo-db"],
        ]),
      },
      {
        id: "surge-pricing",
        name: "Uber · surge pricing",
        description:
          "Pricing events stream through a queue into a compute worker pool while trip reads hit a cache. The pricing loop is only as fast as its slowest worker — model surge storms here.",
        architecture: build("surge-pricing", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 512 } }),
          comp({ id: "trip-cache", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.8 }),
          comp({ id: "trip-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 250 } }),
          comp({ id: "price-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "price-worker", kind: ComponentKind.WORKER, concurrency: 24, defaultServiceTimeMillis: 30 }),
        ], [
          ["client", "api"],
          ["api", "trip-cache"],
          ["trip-cache", "trip-db"],
          ["api", "price-queue"],
          ["price-queue", "price-worker"],
        ]),
      },
      {
        id: "airbnb-search",
        name: "Airbnb · search ranking",
        description:
          "CDN-fronted search with a 90% cache hit rate over listings, plus listing photos in storage. Cache misses are expensive full scans: drop the hit ratio to feel a holiday-weekend search outage.",
        architecture: build("airbnb-search", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "gcp", service: "cloud_cdn" }),
          comp({ id: "search-api", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { memoryMb: 4096, units: 3 } }),
          comp({ id: "listing-cache", kind: ComponentKind.CACHE, provider: "aws", service: "elasticache", hitRatio: 0.9 }),
          comp({ id: "listing-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 500 } }),
          comp({ id: "photos", kind: ComponentKind.OBJECT_STORAGE, provider: "aws", service: "s3" }),
        ], [
          ["client", "edge"],
          ["edge", "search-api"],
          ["search-api", "listing-cache"],
          ["listing-cache", "listing-db"],
          ["search-api", "photos"],
        ]),
      },
      {
        id: "doordash-dispatch",
        name: "DoorDash · dispatch loop",
        description:
          "Order intake stays fast while dispatch decisions flow through a queue to a worker pool. Dispatch latency is invisible until you read queue depth — exactly the failure this shape teaches.",
        architecture: build("doordash-dispatch", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "order-cache", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.75 }),
          comp({ id: "order-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 300 } }),
          comp({ id: "dispatch-queue", kind: ComponentKind.QUEUE, provider: "aws", service: "sqs" }),
          comp({ id: "dispatch-worker", kind: ComponentKind.WORKER, concurrency: 28, defaultServiceTimeMillis: 25 }),
        ], [
          ["client", "api"],
          ["api", "order-cache"],
          ["order-cache", "order-db"],
          ["api", "dispatch-queue"],
          ["dispatch-queue", "dispatch-worker"],
        ]),
      },
      {
        id: "shopify-storefronts",
        name: "Shopify · multi-tenant storefronts",
        description:
          "The full storefront shape: CDN, serverless render tier, product cache, transactional database, object storage, and a background worker queue. One shape, four independent ceilings to discover.",
        architecture: build("shopify-storefronts", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "aws", service: "cloudfront" }),
          comp({ id: "storefront", kind: ComponentKind.API_SERVER, provider: "aws", service: "lambda", config: { memoryMb: 1024 } }),
          comp({ id: "product-cache", kind: ComponentKind.CACHE, provider: "aws", service: "elasticache", hitRatio: 0.85 }),
          comp({ id: "catalog-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 400 } }),
          comp({ id: "assets", kind: ComponentKind.OBJECT_STORAGE, provider: "aws", service: "s3" }),
          comp({ id: "jobs-queue", kind: ComponentKind.QUEUE, provider: "aws", service: "sqs" }),
          comp({ id: "jobs-worker", kind: ComponentKind.WORKER, concurrency: 16, defaultServiceTimeMillis: 40 }),
        ], [
          ["client", "edge"],
          ["edge", "storefront"],
          ["storefront", "product-cache"],
          ["product-cache", "catalog-db"],
          ["storefront", "assets"],
          ["storefront", "jobs-queue"],
          ["jobs-queue", "jobs-worker"],
        ]),
      },
    ],
  },
  {
    id: "media",
    label: "Media",
    systems: [
      {
        id: "netflix-open-connect",
        name: "Netflix · Open Connect",
        description:
          "Edge CDN serving video while a small control-plane fleet and database handle signs-in and playback tokens. Almost all bytes come from the edge; the origin's story is about request rate, not bandwidth.",
        architecture: build("netflix-open-connect", [
          client(),
          comp({ id: "cdn", kind: ComponentKind.NETWORK, provider: "aws", service: "cloudfront" }),
          comp({ id: "control-plane", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { memoryMb: 4096, units: 3 } }),
          comp({ id: "tokens-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 200 } }),
          comp({ id: "video", kind: ComponentKind.OBJECT_STORAGE, provider: "aws", service: "s3", config: { storageGb: 1000 } }),
        ], [
          ["client", "cdn"],
          ["cdn", "control-plane"],
          ["control-plane", "tokens-db"],
          ["control-plane", "video"],
        ]),
      },
      {
        id: "netflix-personalization",
        name: "Netflix · personalization",
        description:
          "Row recommendations from a 90% cache while viewing events stream to an offline worker pool. Break the event pipeline: recommendations silently go stale while the API looks perfectly healthy.",
        architecture: build("netflix-personalization", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "recs", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.9 }),
          comp({ id: "recs-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 200 } }),
          comp({ id: "events-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "events-worker", kind: ComponentKind.WORKER, concurrency: 32, defaultServiceTimeMillis: 90 }),
        ], [
          ["client", "api"],
          ["api", "recs"],
          ["recs", "recs-db"],
          ["api", "events-queue"],
          ["events-queue", "events-worker"],
        ]),
      },
      {
        id: "spotify-playback",
        name: "Spotify · audio playback",
        description:
          "CDN-fronted playback API reading tracks from object storage with a metadata cache over the catalog database. Storage GET latency (60ms class) is the p99 floor — model a cold-listener session.",
        architecture: build("spotify-playback", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "gcp", service: "cloud_cdn" }),
          comp({ id: "playback-api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "meta-cache", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.85 }),
          comp({ id: "catalog-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 250 } }),
          comp({ id: "tracks", kind: ComponentKind.OBJECT_STORAGE, provider: "gcp", service: "cloud_storage" }),
        ], [
          ["client", "edge"],
          ["edge", "playback-api"],
          ["playback-api", "meta-cache"],
          ["meta-cache", "catalog-db"],
          ["playback-api", "tracks"],
        ]),
      },
      {
        id: "youtube-transcoding",
        name: "YouTube · transcoding farm",
        description:
          "Uploads land in object storage while a small worker pool grinds through 250ms-class transcode jobs. Upload burst + slow workers = the defining queue-growth curve of video platforms.",
        architecture: build("youtube-transcoding", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "ingest", kind: ComponentKind.OBJECT_STORAGE, provider: "gcp", service: "cloud_storage", config: { storageGb: 1000 } }),
          comp({ id: "meta-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 200 } }),
          comp({ id: "transcode-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "transcode-worker", kind: ComponentKind.WORKER, concurrency: 16, defaultServiceTimeMillis: 250 }),
        ], [
          ["client", "api"],
          ["api", "ingest"],
          ["api", "meta-db"],
          ["api", "transcode-queue"],
          ["transcode-queue", "transcode-worker"],
        ]),
      },
      {
        id: "twitch-live-chat",
        name: "Twitch · live chat fan-in",
        description:
          "An edge API feeding a coordinated chat tier (Durable-Objects style) whose writes land in the database, with presence in a 95% cache. Chat write fan-in is the bottleneck: widen the room and queue it.",
        architecture: build("twitch-live-chat", [
          client(),
          comp({ id: "edge", kind: ComponentKind.API_SERVER, provider: "cloudflare", service: "workers" }),
          comp({ id: "chat-rooms", kind: ComponentKind.API_SERVER, provider: "cloudflare", service: "durable_objects" }),
          comp({ id: "presence", kind: ComponentKind.CACHE, provider: "aws", service: "elasticache", hitRatio: 0.95 }),
          comp({ id: "chat-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 300 } }),
        ], [
          ["client", "edge"],
          ["edge", "chat-rooms"],
          ["chat-rooms", "chat-db"],
          ["edge", "presence"],
          ["presence", "chat-db"],
        ]),
      },
    ],
  },
  {
    id: "developer",
    label: "Developer",
    systems: [
      {
        id: "github-monorepo-ci",
        name: "GitHub · monorepo CI",
        description:
          "Pushes hit an API that enqueues build jobs for a large, slow worker pool; artifacts land in object storage. CI backlog during a merge stampede is this queue's depth chart, exactly.",
        architecture: build("github-monorepo-ci", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { memoryMb: 4096, units: 3 } }),
          comp({ id: "build-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 400 } }),
          comp({ id: "artifacts", kind: ComponentKind.OBJECT_STORAGE, provider: "aws", service: "s3", config: { storageGb: 1000 } }),
          comp({ id: "build-queue", kind: ComponentKind.QUEUE, provider: "aws", service: "sqs" }),
          comp({ id: "build-worker", kind: ComponentKind.WORKER, concurrency: 32, defaultServiceTimeMillis: 120 }),
        ], [
          ["client", "api"],
          ["api", "build-db"],
          ["api", "artifacts"],
          ["api", "build-queue"],
          ["build-queue", "build-worker"],
        ]),
      },
      {
        id: "gitlab-runners",
        name: "GitLab · runner fleet",
        description:
          "Serverless API fronting a Pub/Sub-fed runner pool with a pipeline database and job logs in storage. Slow the runners and pipelines stall without any API error — the silent failure mode.",
        architecture: build("gitlab-runners", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 1024 } }),
          comp({ id: "pipeline-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 300 } }),
          comp({ id: "job-logs", kind: ComponentKind.OBJECT_STORAGE, provider: "gcp", service: "cloud_storage", config: { storageGb: 800 } }),
          comp({ id: "runner-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "runner", kind: ComponentKind.WORKER, concurrency: 24, defaultServiceTimeMillis: 100 }),
        ], [
          ["client", "api"],
          ["api", "pipeline-db"],
          ["api", "job-logs"],
          ["api", "runner-queue"],
          ["runner-queue", "runner"],
        ]),
      },
      {
        id: "slack-gateway",
        name: "Slack · websocket gateway",
        description:
          "Edge-routed gateway API with a 90% presence cache and async message delivery workers. Long-lived connections pin gateway concurrency: raise concurrent users and watch rejections climb.",
        architecture: build("slack-gateway", [
          client(),
          comp({ id: "edge", kind: ComponentKind.NETWORK, provider: "aws", service: "cloudfront" }),
          comp({ id: "gateway", kind: ComponentKind.API_SERVER, provider: "gcp", service: "cloud_run", config: { memoryMb: 2048 } }),
          comp({ id: "presence", kind: ComponentKind.CACHE, provider: "gcp", service: "memorystore", hitRatio: 0.9 }),
          comp({ id: "msgs-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 300 } }),
          comp({ id: "deliver-queue", kind: ComponentKind.QUEUE, provider: "gcp", service: "pub_sub" }),
          comp({ id: "deliver-worker", kind: ComponentKind.WORKER, concurrency: 16, defaultServiceTimeMillis: 25 }),
        ], [
          ["client", "edge"],
          ["edge", "gateway"],
          ["gateway", "presence"],
          ["presence", "msgs-db"],
          ["gateway", "deliver-queue"],
          ["deliver-queue", "deliver-worker"],
        ]),
      },
      {
        id: "linear-sync",
        name: "Linear · sync engine",
        description:
          "Edge Workers delegate to strongly consistent sync objects (Durable-Objects class) that serialize writes per issue, backed by a database and KV cache. Contention, not capacity, is the lesson.",
        architecture: build("linear-sync", [
          client(),
          comp({ id: "edge", kind: ComponentKind.API_SERVER, provider: "cloudflare", service: "workers" }),
          comp({ id: "sync-engine", kind: ComponentKind.API_SERVER, provider: "cloudflare", service: "durable_objects", config: { concurrency: 100, queueLimit: 100 } }),
          comp({ id: "issues-db", kind: ComponentKind.DATABASE, provider: "gcp", service: "cloud_sql", config: { storageGb: 200 } }),
          comp({ id: "kv-cache", kind: ComponentKind.CACHE, provider: "cloudflare", service: "kv", hitRatio: 0.9 }),
        ], [
          ["client", "edge"],
          ["edge", "sync-engine"],
          ["sync-engine", "issues-db"],
          ["edge", "kv-cache"],
          ["kv-cache", "issues-db"],
        ]),
      },
      {
        id: "dropbox-sync",
        name: "Dropbox · file sync",
        description:
          "Client sync API writing blocks to object storage, metadata to a database, and index updates through a queue. The metadata database is narrow (8 connections) — a sync storm finds it instantly.",
        architecture: build("dropbox-sync", [
          client(),
          comp({ id: "api", kind: ComponentKind.API_SERVER, provider: "aws", service: "ec2", config: { memoryMb: 4096, units: 3 } }),
          comp({ id: "blocks", kind: ComponentKind.OBJECT_STORAGE, provider: "aws", service: "s3", config: { storageGb: 2000 } }),
          comp({ id: "meta-db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds", config: { storageGb: 300 } }),
          comp({ id: "index-queue", kind: ComponentKind.QUEUE, provider: "aws", service: "sqs" }),
          comp({ id: "index-worker", kind: ComponentKind.WORKER, concurrency: 16, defaultServiceTimeMillis: 45 }),
        ], [
          ["client", "api"],
          ["api", "blocks"],
          ["api", "meta-db"],
          ["api", "index-queue"],
          ["index-queue", "index-worker"],
        ]),
      },
    ],
  },
];

/* ------------------------------------------------------------------ lookup -- */

export function exploreSystemById(id: string): ExploreSystem | undefined {
  for (const c of EXPLORE_CATEGORIES) {
    const found = c.systems.find((s) => s.id === id);
    if (found) return found;
  }
  return undefined;
}

/** The gallery as template-shaped objects, so loading reuses templateState. */
export function exploreAsTemplate(s: ExploreSystem): ArchitectureTemplate {
  return { id: s.id, name: s.name, description: s.description, architecture: s.architecture };
}

/**
 * Editor state for a gallery system: the system's architecture plus the
 * user's current workload and options (a comparison stays on one
 * workload), retry reasons that still exist, and a cleared failure list.
 */
export function exploreState(s: ExploreSystem, base: EditorState): EditorState {
  return templateState(exploreAsTemplate(s), base);
}
