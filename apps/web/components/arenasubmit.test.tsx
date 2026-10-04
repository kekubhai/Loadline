/**
 * ArenaSubmit tests — the record half of the one-page Arena workspace.
 * Submitting must (1) persist the canvas as a workspace version and (2)
 * benchmark that stored version, in that order, with the server owning every
 * number. The Connect clients are mocked; the real transport, engine, and
 * scoring are covered by the Go tests and the cross-stack e2e.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Mock } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  ArchitectureRecordSchema,
  ArchitectureSchema,
  ArchitectureVersionSchema,
  ChallengeConstraintsSchema,
  ChallengeSummarySchema,
  create,
  LeaderboardEntrySchema,
  ProjectSchema,
  ScoreBreakdownSchema,
  ScoreComponentSchema,
  SubmissionSchema,
  SystemMetricsSchema,
  WorkloadSpecSchema,
} from "@loadline/api";
import type { Architecture, ChallengeSummary } from "@loadline/api";
import { ArenaSubmit } from "./arenasubmit";

/* ------------------------------------------------------------- fixtures -- */

const challenge: ChallengeSummary = create(ChallengeSummarySchema, {
  slug: "social-feed",
  version: 1,
  name: "Build a Social Feed",
  description: "A social feed for 20,000,000 registered users.",
  difficulty: "expert",
  category: "social",
  status: "active",
  workload: create(WorkloadSpecSchema, { totalUsers: 20_000_000n, dau: 2_000_000n }),
  constraints: create(ChallengeConstraintsSchema, { targetP99Ms: 300 }),
});

const architecture: Architecture = create(ArchitectureSchema, {
  schemaVersion: "1",
  name: "draft",
  components: [{ id: "client" }, { id: "api" }],
  links: [{ from: "client", to: "api" }],
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
      ],
      positives: ["p99 291ms at or under target 300ms"],
      negatives: ["monthly cost over budget"],
    }),
  });
}

const leaderboard = [
  create(LeaderboardEntrySchema, {
    rank: 1n,
    submissionId: "s1",
    displayName: "Rahul",
    score: 94.2,
    throughputRps: 52_000,
    p95Ms: 182,
    p99Ms: 288,
    errorRate: 0.003,
    monthlyCostUsd: 3920,
    costKnown: true,
  }),
];

/* ----------------------------------------------------------------- mock --- */

interface MockArenaClient {
  getChallengeLeaderboard: Mock;
  submitChallenge: Mock;
}
interface MockWorkspaceClient {
  listProjects: Mock;
  createProject: Mock;
  listArchitectures: Mock;
  createArchitecture: Mock;
  createArchitectureVersion: Mock;
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
    getChallengeLeaderboard: vi.fn(async () => ({
      slug: "social-feed",
      version: 1,
      entries: leaderboard,
    })),
    submitChallenge: vi.fn(async () => ({ submission: submission() })),
    ...over,
  };
}

function mockWorkspace(over: Partial<MockWorkspaceClient> = {}): MockWorkspaceClient {
  return {
    listProjects: vi.fn(async () => ({ projects: [] })),
    createProject: vi.fn(async () => ({
      project: create(ProjectSchema, { id: "p1", name: "Arena" }),
    })),
    listArchitectures: vi.fn(async () => ({ architectures: [] })),
    createArchitecture: vi.fn(async () => ({
      architecture: create(ArchitectureRecordSchema, {
        id: "a1",
        projectId: "p1",
        name: "Build a Social Feed",
        versionCount: 1,
        latestVersion: 1,
      }),
      version: create(ArchitectureVersionSchema, {
        id: "v1",
        architectureId: "a1",
        version: 1,
      }),
    })),
    createArchitectureVersion: vi.fn(async () => ({
      version: create(ArchitectureVersionSchema, {
        id: "v2",
        architectureId: "a1",
        version: 2,
      }),
    })),
    ...over,
  };
}

function renderView() {
  const arena = mockArena();
  const ws = mockWorkspace();
  arenaFactory = () => arena;
  workspaceFactory = () => ws;
  const onNotice = vi.fn();
  render(
    <ArenaSubmit
      serverUrl="http://test:8080"
      challenge={challenge}
      architecture={architecture}
      onNotice={onNotice}
    />,
  );
  return { arena, ws, onNotice };
}

beforeEach(() => {
  arenaFactory = () => mockArena();
  workspaceFactory = () => mockWorkspace();
});

/* ---------------------------------------------------------------- tests -- */

describe("ArenaSubmit", () => {
  it("saves a new project + architecture version before benchmarking", async () => {
    const user = userEvent.setup();
    const { arena, ws, onNotice } = renderView();

    const submit = screen.getByRole("button", { name: /submit & benchmark/i });
    expect(submit).toBeDisabled();

    await user.type(screen.getByLabelText(/display name/i), "Anirban");
    expect(submit).toBeEnabled();
    await user.click(submit);

    await waitFor(() => expect(arena.submitChallenge).toHaveBeenCalledTimes(1));

    // The canvas is persisted first: project, then architecture (v1).
    expect(ws.createProject).toHaveBeenCalledWith({ name: "Arena" });
    expect(ws.createArchitecture).toHaveBeenCalledWith({
      projectId: "p1",
      name: "Build a Social Feed",
      definition: architecture,
    });
    expect(ws.createArchitectureVersion).not.toHaveBeenCalled();

    expect(arena.submitChallenge).toHaveBeenCalledWith({
      challengeSlug: "social-feed",
      architectureVersionId: "v1",
      displayName: "Anirban",
    });

    expect((await screen.findAllByText("91.8")).length).toBeGreaterThan(0);
    expect(await screen.findByText("rank #2")).toBeInTheDocument();
    expect(await screen.findByText("Rahul")).toBeInTheDocument();
    expect(onNotice).toHaveBeenCalled();
  });

  it("appends a new version when the Arena project already exists", async () => {
    const user = userEvent.setup();
    const ws = mockWorkspace({
      listProjects: vi.fn(async () => ({
        projects: [create(ProjectSchema, { id: "p1", name: "Arena" })],
      })),
      listArchitectures: vi.fn(async () => ({
        architectures: [
          create(ArchitectureRecordSchema, {
            id: "a1",
            projectId: "p1",
            name: "Build a Social Feed",
            versionCount: 1,
            latestVersion: 1,
          }),
        ],
      })),
    });
    workspaceFactory = () => ws;
    const arena = mockArena();
    arenaFactory = () => arena;

    render(
      <ArenaSubmit
        serverUrl="http://test:8080"
        challenge={challenge}
        architecture={architecture}
        onNotice={vi.fn()}
      />,
    );

    await user.type(screen.getByLabelText(/display name/i), "Anirban");
    await user.click(screen.getByRole("button", { name: /submit & benchmark/i }));

    await waitFor(() => expect(arena.submitChallenge).toHaveBeenCalledTimes(1));

    expect(ws.createProject).not.toHaveBeenCalled();
    expect(ws.createArchitecture).not.toHaveBeenCalled();
    expect(ws.createArchitectureVersion).toHaveBeenCalledWith({
      architectureId: "a1",
      definition: architecture,
    });
    expect(arena.submitChallenge).toHaveBeenCalledWith({
      challengeSlug: "social-feed",
      architectureVersionId: "v2",
      displayName: "Anirban",
    });
  });
});
