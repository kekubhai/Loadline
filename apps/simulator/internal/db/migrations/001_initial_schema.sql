-- 001_initial_schema.sql
--
-- LOADLINE workspace persistence: the minimum schema for
--
--   Dashboard → My Architectures → Build → Save → Run → Save Run
--            → Modify → Run Again → Compare Runs
--
-- Design rules applied here (see AGENTS.md):
--
--   * Relational columns for identity, relationships, timestamps, status,
--     version, and seed.
--   * JSONB for structures the simulation model is still evolving:
--     architecture definitions, workload configurations, metrics,
--     bottlenecks, failures, capacity, and cost breakdowns. The payloads
--     are the engine's own output (protojson of the API messages), not a
--     second schema invented for storage.
--   * Every foreign key is explicit about what happens on delete. Deleting
--     a project or architecture is an intentional user action that removes
--     the documents it contains; nothing cascades "by accident", and a run
--     that outlives its workload keeps its history (workload_id is set to
--     NULL rather than deleting the run).
--
-- gen_random_uuid() is built into PostgreSQL 13+ (no extension needed) and
-- is therefore available on Neon without pgcrypto.
--
-- Idempotent by construction (IF NOT EXISTS): migrations are tracked in
-- schema_migrations and applied once, but re-running this file is safe.

-- ---------------------------------------------------------------------------
-- projects: a LOADLINE workspace
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS projects (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL CHECK (length(btrim(name)) > 0),
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- architectures: a logical architecture inside a project
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS architectures (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name        text NOT NULL CHECK (length(btrim(name)) > 0),
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS architectures_project_id_idx
    ON architectures (project_id);

-- ---------------------------------------------------------------------------
-- architecture_versions: an immutable snapshot of an architecture
--
-- definition holds the canonical architecture document (protojson of the
-- API's Architecture message: schema_version, name, components, links).
-- Versions are append-only: a modification produces a NEW row, so a
-- simulation run always refers to exactly the architecture it ran.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS architecture_versions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    architecture_id uuid NOT NULL REFERENCES architectures (id) ON DELETE CASCADE,
    version         integer NOT NULL CHECK (version > 0),
    definition      jsonb NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT architecture_versions_architecture_version_key
        UNIQUE (architecture_id, version)
);

-- ---------------------------------------------------------------------------
-- workloads: a load definition attached to one architecture version
--
-- configuration holds the canonical workload document (protojson of the
-- API's WorkloadSpec message: total_users, dau, requests_per_user_per_day,
-- peak_multiplier, read_write_ratio, payload_bytes). The derived load plan
-- is deliberately NOT stored here — it is a pure function of this spec,
-- and it is stored per run (see simulation_results.plan).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS workloads (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    architecture_version_id uuid NOT NULL REFERENCES architecture_versions (id) ON DELETE CASCADE,
    name                    text NOT NULL CHECK (length(btrim(name)) > 0),
    configuration           jsonb NOT NULL,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS workloads_architecture_version_id_idx
    ON workloads (architecture_version_id);

-- ---------------------------------------------------------------------------
-- simulation_runs: one execution of a simulation
--
-- status mirrors the engine's lifecycle, restricted to the states that are
-- persistable: pending, running, completed, failed, stopped (the engine's
-- STOPPED: the horizon was not reached and the metrics are a partial view).
-- Transient control states (paused/stopping) are live-process concerns and
-- are not persisted.
--
-- seed is persisted for EVERY run: a seeded run is reproducible, and a
-- stored run without its seed could not be reproduced. duration_ms is the
-- simulated horizon actually measured (not wall time).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS simulation_runs (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    architecture_version_id uuid NOT NULL REFERENCES architecture_versions (id) ON DELETE CASCADE,
    -- A run whose workload was deleted keeps its history; the link is
    -- cleared instead of the run being destroyed.
    workload_id             uuid REFERENCES workloads (id) ON DELETE SET NULL,
    status                  text NOT NULL
        CHECK (status IN ('pending', 'running', 'completed', 'failed', 'stopped')),
    seed                    bigint NOT NULL,
    duration_ms             double precision NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    error                   text NOT NULL DEFAULT '',
    created_at              timestamptz NOT NULL DEFAULT now(),
    started_at              timestamptz,
    finished_at             timestamptz
);

CREATE INDEX IF NOT EXISTS simulation_runs_architecture_version_id_idx
    ON simulation_runs (architecture_version_id, created_at DESC);

CREATE INDEX IF NOT EXISTS simulation_runs_workload_id_idx
    ON simulation_runs (workload_id);

-- ---------------------------------------------------------------------------
-- simulation_results: the useful summary of a finished run
--
-- One row per run (UNIQUE). Every column is protojson of the corresponding
-- API message, produced by the engine and the provider models during the
-- run — nothing here is derived after the fact, and no metric is invented
-- for storage. Discrete simulation events are deliberately NOT persisted.
--
-- Deleting a run deletes its result: a result without its run has no
-- meaning (and a run without a result is already representable, e.g. a
-- failed run).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS simulation_results (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    simulation_run_id uuid NOT NULL UNIQUE
        REFERENCES simulation_runs (id) ON DELETE CASCADE,
    -- RunSummary: engine-level counters (events processed/scheduled/pending,
    -- stop reason).
    summary          jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- LoadPlan: the derived, inspectable workload chain
    -- (users → DAU → requests/day → avg RPS → peak RPS).
    plan             jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- SystemMetrics: throughput, latency (avg/p50/p95/p99/max), error and
    -- timeout rates, and the per-component report (arrivals, completions,
    -- rejections, queue depth, utilization, throughput, capacity).
    metrics          jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- CapacityReport[]: per-component modeled ceiling, utilization,
    -- headroom, saturation flags, and the assumptions behind them.
    capacity         jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- Diagnosis: flagged bottlenecks with their machine-computed reasons.
    bottlenecks      jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- FailureRecord[]: the per-request failure log.
    failures         jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- CostEstimate: the monthly estimate from local pricing models, with
    -- its full assumption trail (always an ESTIMATE, never live billing).
    cost             jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS simulation_results_simulation_run_id_idx
    ON simulation_results (simulation_run_id);
