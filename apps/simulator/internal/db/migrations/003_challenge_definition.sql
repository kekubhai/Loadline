-- 003_challenge_definition.sql
--
-- Forward migration: store the canonical challenge document on the
-- challenges row.
--
-- 002_arena.sql already creates `challenges` with this column. This
-- migration exists because 002 was applied to a development database before
-- the column was added; ALTER ... IF NOT EXISTS makes the column present on
-- every database regardless of when 002 ran, and is a no-op on fresh ones.
--
-- The column holds the challenge definition VERBATIM (the same JSON as the
-- embedded file under internal/arena/definitions/). The server reconstructs
-- a benchmark from it, so a stored version is self-describing and pinned:
-- changing a definition file does not silently change a historical run.
ALTER TABLE challenges
    ADD COLUMN IF NOT EXISTS definition jsonb NOT NULL DEFAULT '{}'::jsonb;
