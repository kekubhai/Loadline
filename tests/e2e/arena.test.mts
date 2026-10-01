/**
 * LOADLINE — Arena cross-stack e2e.
 *
 * Browsing challenges must work over the real Connect client against the
 * real Go server, with NO database: definitions ship with the binary, so the
 * Arena is always available. (Submission and leaderboards need persistence
 * and are covered by the Go service tests against PostgreSQL.)
 *
 * Prereq: loadline-server listening on E2E_BASE_URL (default
 * http://127.0.0.1:8080).
 */
import { describe, expect, it, beforeAll } from "vitest";
import { createArenaClient } from "../../packages/api/src/index";
import type { ArenaClient } from "../../packages/api/src/index";

const BASE = process.env.E2E_BASE_URL ?? "http://127.0.0.1:8080";
let client: ArenaClient;

beforeAll(() => {
  client = createArenaClient({ baseUrl: BASE });
});

describe("arena", () => {
  it("lists the five standardized challenges", async () => {
    const res = await client.listChallenges({});
    expect(res.challenges).toHaveLength(5);
    for (const c of res.challenges) {
      expect(c.slug).not.toBe("");
      expect(c.name).not.toBe("");
      expect(["beginner", "intermediate", "advanced", "expert"]).toContain(c.difficulty);
      expect(c.constraints?.targetP99Ms).toBeGreaterThan(0);
      expect(c.workload?.peakMultiplier).toBeGreaterThan(0);
    }
  });

  it("exposes exactly how a challenge is evaluated", async () => {
    const res = await client.getChallenge({ slug: "social-feed" });
    const ch = res.challenge!;
    expect(ch.summary?.status).toBe("active");
    expect(Number(ch.seed)).toBeGreaterThan(0);
    // The benchmark definition is fully public: workload, targets, required
    // components, failure scenarios, and scoring weights.
    expect(ch.requirements.length).toBeGreaterThan(0);
    expect(ch.summary?.failures.length).toBeGreaterThan(0);
    expect(ch.scoring?.latency).toBeGreaterThan(0);
    expect(ch.summary?.constraints?.monthlyBudgetUsd).toBeGreaterThan(0);
  });

  it("returns a clean not-found for an unknown challenge", async () => {
    await expect(client.getChallenge({ slug: "nope" })).rejects.toThrow();
  });

  it("leaderboard requires persistence and fails cleanly without it", async () => {
    // In CI the server intentionally runs without DATABASE_URL; the call
    // must fail with a structured precondition error, not hang or crash.
    await expect(client.getChallengeLeaderboard({ slug: "social-feed" })).rejects.toThrow();
  });
});
