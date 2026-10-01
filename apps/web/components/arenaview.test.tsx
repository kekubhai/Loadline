/**
 * ArenaView tests — the benchmark workflow inside the component: browse
 * challenges, read the evaluation rules, load the challenge workload into
 * the editor, submit a stored architecture version, and render the score
 * breakdown + leaderboard. The Connect clients are mocked; the real
 * transport, engine, and scoring are covered by the Go tests and the
 * cross-stack e2e.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Mock } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  ArchitectureRecordSchema,
  ArchitectureVersionSchema,
  ChallengeConstraintsSchema,
  ChallengeSchema,
  ChallengeSummarySchema,
  create,
  FailureScenarioSchema,
  KindRequirementSchema,
  LeaderboardEntrySchema,
  ProjectSchema,
  ScoreBreakdownSchema,
  ScoreComponentSchema,
  ScoringWeightsSchema,
  SubmissionSchema,
  SystemMetricsSchema,
  WorkloadSpecSchema,
} from "@loadline/api";
import { ArenaView } from "./arenaview";

/* ------------------------------------------------------------- fixtures -- */

const challengeWorkload = create(WorkloadSpecSchema, {
  totalUsers: 20_000_000n,
  dau: 2_000_000n,
  requestsPerUserPerDay: 270,
  peakMultiplier: 8,
  readWriteRatio: 9,
  payloadBytes: 4096n,
});

const constraints = create(ChallengeConstraintsSchema, {
  targetThroughputRps: 40_000,
  targetP95Ms: 150,
  targetP99Ms: 300,
  targetErrorRate: 0.01,
  monthlyBudgetUsd: 5000,
});

const summary = create(ChallengeSummarySchema, {
  slug: "social-feed",
  version: 1,
  name: "Build a Social Feed",
  description: "A 90/10 read-heavy feed with a cache outage.",
  difficulty: "expert",
  category: "social",
  status: "active",
  workload: challengeWorkload,
  constraints,
  failures: [
    create(FailureScenarioSchema, {
      targetKind: "cache",
      type: "crash",
      startMs: 30_000,
      durationMs: 20_000,
      passThrough: true,
    }),
  ],
});

const challenge = create(ChallengeSchema, {
  summary,
  requirements: [
    create(KindRequirementSchema, { kind: "cache", min: 1 }),
    create(KindRequirementSchema, { kind: "database", min: 1 }),
  ],
  scoring: create(ScoringWeightsSchema, {
    throughput: 0.3,
    latency: 0.25,
    reliability: 0.2,
    cost: 0.1,
    failureRecovery: 0.15,
  }),
  seed: 2002n,
  durationMs: 60_000,
});

const otherSummary = create(ChallengeSummarySchema, {
  slug: "high-scale-api",
  version: 1,
  name: "Design a High-Scale API",
  difficulty: "advanced",
  category: "api",
  status: "active",
  workload: challengeWorkload,
  constraints,
});

function submission() {
  return create(SubmissionSchema, {
    id: "sub-1",
    challengeSlug: "social-feed",
    challengeVersion: 1,
    displayName: "Anirban",
    status: "completed",
    score: 91.8,
    rank: 2n,
    seed: 2002n,
    metrics: create(SystemMetricsSchema, {
      durationMs: 60_000,
      completed: 2_400_000n,
      p95Ms: 184,
      p99Ms: 291,
      errorRate: 0.007,
    }),
    breakdown: create(ScoreBreakdownSchema, {
      score: 91.8,
      components: [
        create(ScoreComponentSchema, { name: "Throughput", score: 92, weight: 0.3 }),
        create(ScoreComponentSchema, { name: "Latency", score: 88, weight: 0.25 }),
      ],
      positives: ["p99 291ms at or under target 300ms"],
      negatives: ["monthly cost over budget"],
    }),
  });
}

const leaderboard = [
  create(LeaderboardEntrySchema, { rank: 1n, submissionId: "s1", displayName: "Rahul", score: 94.2, throughputRps: 52_000, p95Ms: 182, p99Ms: 288, errorRate: 0.003, monthlyCostUsd: 3920, costKnown: true }),
  create(LeaderboardEntrySchema, { rank: 2n, submissionId: "sub-1", displayName: "Anirban", score: 91.8, throughputRps: 48_200, p95Ms: 184, p99Ms: 291, errorRate: 0.007, monthlyCostUsd: 4120, costKnown: true }),
];

/* ----------------------------------------------------------------- mock --- */

interface MockArenaClient {
  listChallenges: Mock;
  getChallenge: Mock;
  getChallengeLeaderboard: Mock;
  submitChallenge: Mock;
}
interface MockWorkspaceClient {
  listProjects: Mock;
  listArchitectures: Mock;
  listArchitectureVersions: Mock;
}

vi.mock("@loadline/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@loadline/api")>();
  return {
    ...actual,
    createArenaClient: vi.fn(() => arenaFactory()),
    createWorkspaceClient: vi.fn(() => workspaceFactory()),
  };
});

let arenaFactory: () => MockArenaClient = () => mockArena();
let workspaceFactory: () => MockWorkspaceClient = () => mockWorkspace();

function mockArena(over: Partial<MockArenaClient> = {}): MockArenaClient {
  return {
    listChallenges: vi.fn(async () => ({ challenges: [summary, otherSummary] })),
    getChallenge: vi.fn(async () => ({ challenge })),
    getChallengeLeaderboard: vi.fn(async () => ({ slug: "social-feed", version: 1, entries: leaderboard })),
    submitChallenge: vi.fn(async () => ({ submission: submission() })),
    ...over,
  };
}

function mockWorkspace(over: Partial<MockWorkspaceClient> = {}): MockWorkspaceClient {
  const project = create(ProjectSchema, { id: "p1", name: "prod-system" });
  const arch = create(ArchitectureRecordSchema, { id: "a1", projectId: "p1", name: "social-web", versionCount: 1, latestVersion: 1 });
  const version = create(ArchitectureVersionSchema, { id: "v1", architectureId: "a1", version: 1 });
  return {
    listProjects: vi.fn(async () => ({ projects: [project] })),
    listArchitectures: vi.fn(async () => ({ architectures: [arch] })),
    listArchitectureVersions: vi.fn(async () => ({ versions: [version] })),
    ...over,
  };
}

function renderView() {
  const arena = mockArena();
  const ws = mockWorkspace();
  arenaFactory = () => arena;
  workspaceFactory = () => ws;
  const onLoadWorkload = vi.fn();
  const onNotice = vi.fn();
  render(<ArenaView serverUrl="http://test:8080" onLoadWorkload={onLoadWorkload} onNotice={onNotice} />);
  return { arena, ws, onLoadWorkload, onNotice };
}

beforeEach(() => {
  arenaFactory = () => mockArena();
  workspaceFactory = () => mockWorkspace();
});

/* ---------------------------------------------------------------- tests -- */

describe("ArenaView", () => {
  it("lists challenges and shows how the selected one is evaluated", async () => {
    const user = userEvent.setup();
    renderView();

    expect(await screen.findByText("Build a Social Feed")).toBeInTheDocument();
    expect(screen.getByText("Design a High-Scale API")).toBeInTheDocument();

    await user.click(screen.getByText("Build a Social Feed"));

    // Targets, required components, failure scenario, and weights are public.
    expect(await screen.findByText("Benchmark rules")).toBeInTheDocument();
    expect(await screen.findByText("expert")).toBeInTheDocument();
    expect(await screen.findByText(/crash · cache/)).toBeInTheDocument();
    expect(await screen.findByText("failure recovery")).toBeInTheDocument();
    expect((await screen.findAllByText("≥ 1")).length).toBeGreaterThan(0);
  });

  it("loads the challenge workload into the editor", async () => {
    const user = userEvent.setup();
    const { onLoadWorkload } = renderView();

    await user.click(await screen.findByText("Build a Social Feed"));
    await user.click(await screen.findByText("build this architecture"));

    expect(onLoadWorkload).toHaveBeenCalledTimes(1);
    const passed = onLoadWorkload.mock.calls[0][0];
    expect(passed.dau).toBe(2_000_000n);
    expect(passed.peakMultiplier).toBe(8);
  });

  it("submits a stored version and renders the score, breakdown, and leaderboard", async () => {
    const user = userEvent.setup();
    const { arena } = renderView();

    await user.click(await screen.findByText("Build a Social Feed"));

    // Run benchmark is gated until a name and a version are chosen.
    const run = screen.getByRole("button", { name: /run benchmark/i });
    expect(run).toBeDisabled();

    await user.type(screen.getByLabelText(/display name/i), "Anirban");
    await user.selectOptions(screen.getByLabelText(/^project$/i), "p1");
    await user.selectOptions(await screen.findByLabelText(/^architecture$/i), "a1");
    await user.selectOptions(await screen.findByLabelText(/^version$/i), "v1");

    await user.click(screen.getByRole("button", { name: /run benchmark/i }));

    await waitFor(() => expect(arena.submitChallenge).toHaveBeenCalledTimes(1));
    expect(arena.submitChallenge).toHaveBeenCalledWith({
      challengeSlug: "social-feed",
      architectureVersionId: "v1",
      displayName: "Anirban",
    });

    // The result and the refreshed leaderboard render.
    expect((await screen.findAllByText("91.8")).length).toBeGreaterThan(0);
    expect(await screen.findByText("rank #2")).toBeInTheDocument();
    expect(await screen.findByText("Rahul")).toBeInTheDocument();
    expect((await screen.findAllByText(/291\.00 ms/)).length).toBeGreaterThan(0);
  });
});
