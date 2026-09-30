/**
 * LOADLINE — architecture templates ("start from").
 *
 * A template is a complete, runnable architecture document: every
 * component carries a real provider service (or an explicit generic
 * capacity), so capacity and cost reports work the moment the user runs
 * it. Templates model common system shapes, not empty canvases — each
 * one is meant to be broken: change the load, inject a failure, read the
 * diagnosis.
 *
 * Component kinds mirror the simulator's own provider kind mapping
 * (providers.kindForService), so the canvas, the metrics, and the
 * diagnosis all agree on what each node is.
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

export interface ArchitectureTemplate {
  id: string;
  name: string;
  /** What this shape models and what it is good at breaking. */
  description: string;
  architecture: Architecture;
}

type ComponentInit = MessageInitShape<typeof ComponentSpecSchema> & { id: string };

function comp(init: ComponentInit): ComponentSpec {
  return create(ComponentSpecSchema, init);
}

function arch(
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

/* ------------------------------------------------------------- templates -- */

export const TEMPLATES: ArchitectureTemplate[] = [
  {
    id: "aws-web",
    name: "cache-backed web API",
    description:
      "client → lambda → elasticache → rds. Cache misses fall through to the database, so the classic break is a cold cache saturating RDS.",
    architecture: arch(
      "aws-web",
      [
        comp({ id: "client", kind: ComponentKind.CLIENT }),
        comp({
          id: "api",
          kind: ComponentKind.API_SERVER,
          provider: "aws",
          service: "lambda",
          config: { memoryMb: 512 },
        }),
        comp({
          id: "cache",
          kind: ComponentKind.CACHE,
          provider: "aws",
          service: "elasticache",
          hitRatio: 0.8,
        }),
        comp({
          id: "db",
          kind: ComponentKind.DATABASE,
          provider: "aws",
          service: "rds",
          config: { storageGb: 100 },
        }),
      ],
      [
        ["client", "api"],
        ["api", "cache"],
        ["cache", "db"],
      ],
    ),
  },
  {
    id: "queue-workers",
    name: "async worker pipeline",
    description:
      "client → lambda → rds for sync reads, plus lambda → sqs → worker pool for buffered work (the worker is terminal: its completions end the async request). Raise the load and watch the queue grow instead of the API failing.",
    architecture: arch(
      "queue-workers",
      [
        comp({ id: "client", kind: ComponentKind.CLIENT }),
        comp({
          id: "api",
          kind: ComponentKind.API_SERVER,
          provider: "aws",
          service: "lambda",
          config: { memoryMb: 1024 },
        }),
        comp({ id: "queue", kind: ComponentKind.QUEUE, provider: "aws", service: "sqs" }),
        comp({
          id: "worker",
          kind: ComponentKind.WORKER,
          concurrency: 24,
          defaultServiceTimeMillis: 40,
        }),
        comp({
          id: "db",
          kind: ComponentKind.DATABASE,
          provider: "aws",
          service: "rds",
          config: { storageGb: 200 },
        }),
      ],
      [
        ["client", "api"],
        ["api", "db"],
        ["api", "queue"],
        ["queue", "worker"],
      ],
    ),
  },
  {
    id: "edge-cdn",
    name: "CDN + origin",
    description:
      "client → cloudfront → ec2 origin → rds, with s3 for objects. Tests how much load the edge absorbs before the origin sees it.",
    architecture: arch(
      "edge-cdn",
      [
        comp({ id: "client", kind: ComponentKind.CLIENT }),
        comp({ id: "cdn", kind: ComponentKind.NETWORK, provider: "aws", service: "cloudfront" }),
        comp({
          id: "origin",
          kind: ComponentKind.API_SERVER,
          provider: "aws",
          service: "ec2",
          config: { memoryMb: 4096 },
        }),
        comp({ id: "objects", kind: ComponentKind.OBJECT_STORAGE, provider: "aws", service: "s3" }),
        comp({
          id: "db",
          kind: ComponentKind.DATABASE,
          provider: "aws",
          service: "rds",
          config: { storageGb: 500 },
        }),
      ],
      [
        ["client", "cdn"],
        ["cdn", "origin"],
        ["origin", "db"],
        ["origin", "objects"],
      ],
    ),
  },
  {
    id: "cloudflare-edge",
    name: "cloudflare workers + kv",
    description:
      "client → workers → kv, with r2 for objects. A single-provider edge stack: cheap until KV or the worker's own concurrency becomes the ceiling.",
    architecture: arch(
      "cloudflare-edge",
      [
        comp({ id: "client", kind: ComponentKind.CLIENT }),
        comp({
          id: "worker",
          kind: ComponentKind.API_SERVER,
          provider: "cloudflare",
          service: "workers",
        }),
        comp({
          id: "kv",
          kind: ComponentKind.CACHE,
          provider: "cloudflare",
          service: "kv",
          hitRatio: 0.9,
        }),
        comp({
          id: "objects",
          kind: ComponentKind.OBJECT_STORAGE,
          provider: "cloudflare",
          service: "r2",
        }),
      ],
      [
        ["client", "worker"],
        ["worker", "kv"],
        ["worker", "objects"],
      ],
    ),
  },
  {
    id: "gcp-serverless",
    name: "gcp serverless trio",
    description:
      "client → cloud run → memorystore → cloud_sql. The same web shape on GCP, for comparing providers under one workload.",
    architecture: arch(
      "gcp-serverless",
      [
        comp({ id: "client", kind: ComponentKind.CLIENT }),
        comp({
          id: "api",
          kind: ComponentKind.API_SERVER,
          provider: "gcp",
          service: "cloud_run",
          config: { memoryMb: 512 },
        }),
        comp({
          id: "cache",
          kind: ComponentKind.CACHE,
          provider: "gcp",
          service: "memorystore",
          hitRatio: 0.8,
        }),
        comp({
          id: "db",
          kind: ComponentKind.DATABASE,
          provider: "gcp",
          service: "cloud_sql",
          config: { storageGb: 100 },
        }),
      ],
      [
        ["client", "api"],
        ["api", "cache"],
        ["cache", "db"],
      ],
    ),
  },
];

export function templateById(id: string): ArchitectureTemplate | undefined {
  return TEMPLATES.find((t) => t.id === id);
}

/**
 * Editor state for a template: the template's architecture plus the
 * user's current workload and options (so a comparison stays on one
 * workload), retry reasons that still exist, and a cleared failure list.
 */
export function templateState(t: ArchitectureTemplate, base: EditorState): EditorState {
  const ids = new Set(t.architecture.components.map((c) => c.id));
  return {
    ...base,
    architecture: t.architecture,
    failures: [],
    positions: {},
    selected: null,
    options: {
      ...base.options,
      retryOn: base.options.retryOn.filter((id) => ids.has(id)),
    },
  };
}
