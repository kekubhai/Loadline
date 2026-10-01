-- 002_arena.sql
--
-- LOADLINE Arena: benchmark challenges and anonymous submissions.
--
-- Design rules (see AGENTS.md):
--
--   * A challenge is versioned. `challenges` is a PROJECTION of the
--     version-controlled definition files embedded in the server binary
--     (apps/simulator/internal/arena/definitions/*.json), refreshed at
--     boot. It is never hand-edited, so there is exactly one source of
--     truth. (slug, version) identifies one immutable benchmark.
--
--   * `challenge_submissions` stores only measured, server-computed
--     results. No score, metric, or rank is ever accepted from a client.
--     Rank is deliberately NOT stored: it is derived from the leaderboard
--     query (score DESC, submitted_at ASC, id ASC) and would go stale.
--
--   * A submission reuses the existing simulation_runs / simulation_results
--     tables for its benchmark execution (simulation_run_id), so the
--     engine's output is stored exactly once, by the same code path as any
--     other run. The challenge workload is not a stored `workloads` row
--     (it lives in the challenge), so simulation_runs.workload_id is NULL
--     for benchmark runs.
--
--   * display_name is NOT an identity. It is a public label only. There is
--     no account, no email, no login.
--
-- Idempotent by construction (IF NOT EXISTS).

-- ---------------------------------------------------------------------------
-- challenges: a versioned benchmark definition (projection of embedded files)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS challenges (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug              text NOT NULL CHECK (length(btrim(slug)) > 0),
    version           integer NOT NULL CHECK (version > 0),
    name              text NOT NULL,
    description       text NOT NULL DEFAULT '',
    difficulty        text NOT NULL
        CHECK (difficulty IN ('beginner', 'intermediate', 'advanced', 'expert')),
    category          text NOT NULL
        CHECK (category IN ('api', 'social', 'ecommerce', 'saas', 'realtime', 'distributed', 'cloud')),
    status            text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'active', 'closed')),
    -- definition is the canonical challenge document VERBATIM (the same
    -- JSON as the embedded file). It is the projection's source of truth:
    -- the server reconstructs the benchmark from it, so a stored version
    -- keeps behaviour even if the files describe a newer version.
    definition        jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Protojson of the API's WorkloadSpec: the standardized load.
    workload_config   jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Protojson of ChallengeConstraints: the scoring targets.
    constraints       jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Protojson of ScoringWeights.
    scoring_config    jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Protojson of the FailureScenario list container.
    failure_scenarios jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- Protojson of the KindRequirement list container.
    requirements      jsonb NOT NULL DEFAULT '{}'::jsonb,
    seed              bigint NOT NULL CHECK (seed > 0),
    duration_ms       double precision NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT challenges_slug_version_key UNIQUE (slug, version)
);

CREATE INDEX IF NOT EXISTS challenges_slug_idx ON challenges (slug);

-- ---------------------------------------------------------------------------
-- challenge_submissions: one benchmarked architecture
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS challenge_submissions (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The historical benchmark is pinned by version: editing a challenge
    -- creates a new version and never rewrites past leaderboards.
    challenge_id            uuid NOT NULL REFERENCES challenges (id) ON DELETE CASCADE,
    challenge_slug          text NOT NULL,
    challenge_version       integer NOT NULL,
    -- Public label only; never an identity.
    display_name            text NOT NULL CHECK (length(btrim(display_name)) > 0),
    architecture_version_id uuid NOT NULL REFERENCES architecture_versions (id) ON DELETE CASCADE,
    -- The benchmark run. Kept when the run row survives; a deleted run
    -- leaves the submission's stored metrics intact (SET NULL).
    simulation_run_id       uuid REFERENCES simulation_runs (id) ON DELETE SET NULL,
    status                  text NOT NULL
        CHECK (status IN ('completed', 'failed')),
    -- score is 0 for failed submissions, which never rank.
    score                   double precision NOT NULL DEFAULT 0,
    -- Protojson of SystemMetrics / LoadPlan / Diagnosis / CapacityReport[] /
    -- CostEstimate / ScoreBreakdown, each stored verbatim as the server
    -- computed it.
    metrics                 jsonb NOT NULL DEFAULT '{}'::jsonb,
    plan                    jsonb NOT NULL DEFAULT '{}'::jsonb,
    bottlenecks             jsonb NOT NULL DEFAULT '{}'::jsonb,
    capacity                jsonb NOT NULL DEFAULT '[]'::jsonb,
    cost                    jsonb NOT NULL DEFAULT '{}'::jsonb,
    score_breakdown         jsonb NOT NULL DEFAULT '{}'::jsonb,
    seed                    bigint NOT NULL,
    error                   text NOT NULL DEFAULT '',
    submitted_at            timestamptz NOT NULL DEFAULT now()
);

-- Leaderboard query: completed submissions for (challenge, version) ordered
-- by score DESC, submitted_at ASC, id ASC — a fully deterministic ordering.
CREATE INDEX IF NOT EXISTS challenge_submissions_leaderboard_idx
    ON challenge_submissions (challenge_id, challenge_version, score DESC, submitted_at ASC, id ASC)
    WHERE status = 'completed';

-- "Most recent" ordering.
CREATE INDEX IF NOT EXISTS challenge_submissions_recent_idx
    ON challenge_submissions (challenge_id, challenge_version, submitted_at DESC);

-- Cooldown lookup: recent submissions by one display name for one challenge.
CREATE INDEX IF NOT EXISTS challenge_submissions_display_name_idx
    ON challenge_submissions (challenge_id, display_name, submitted_at DESC);
