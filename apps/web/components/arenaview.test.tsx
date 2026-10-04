/**
 * ArenaView (picker) tests — the list half of the Arena view. It shows only
 * each challenge's name, difficulty, and category, and selecting one opens
 * the single-page build/run/submit workspace by reporting the slug upward.
 * The Connect client is mocked; the real transport, engine, and scoring are
 * covered by the Go tests and the cross-stack e2e.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Mock } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  ChallengeConstraintsSchema,
  ChallengeSummarySchema,
  create,
  FailureScenarioSchema,
  WorkloadSpecSchema,
} from "@loadline/api";
import { ArenaView } from "./arenaview";

/* ------------------------------------------------------------- fixtures -- */

const workload = create(WorkloadSpecSchema, {
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

const socialFeed = create(ChallengeSummarySchema, {
  slug: "social-feed",
  version: 1,
  name: "Build a Social Feed",
  description:
    "A social feed for 20,000,000 registered users and 2,000,000 daily active users, served globally.",
  difficulty: "expert",
  category: "social",
  status: "active",
  workload,
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

const highScaleApi = create(ChallengeSummarySchema, {
  slug: "high-scale-api",
  version: 1,
  name: "Design a High-Scale API",
  difficulty: "advanced",
  category: "api",
  status: "active",
  workload,
  constraints,
});

/* ----------------------------------------------------------------- mock --- */

interface MockArenaClient {
  listChallenges: Mock;
}

vi.mock("@loadline/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@loadline/api")>();
  return {
    ...actual,
    createArenaClient: vi.fn(() => arenaFactory()),
  };
});

let arenaFactory: () => MockArenaClient = () => mockArena();

function mockArena(over: Partial<MockArenaClient> = {}): MockArenaClient {
  return {
    listChallenges: vi.fn(async () => ({ challenges: [socialFeed, highScaleApi] })),
    ...over,
  };
}

function renderView() {
  const arena = mockArena();
  arenaFactory = () => arena;
  const onSelectChallenge = vi.fn();
  const onNotice = vi.fn();
  render(
    <ArenaView
      serverUrl="http://test:8080"
      onSelectChallenge={onSelectChallenge}
      onNotice={onNotice}
    />,
  );
  return { arena, onSelectChallenge, onNotice };
}

beforeEach(() => {
  arenaFactory = () => mockArena();
});

/* ---------------------------------------------------------------- tests -- */

describe("ArenaView picker", () => {
  it("lists challenges with only their difficulty and category", async () => {
    renderView();

    expect(await screen.findByText("Build a Social Feed")).toBeInTheDocument();
    expect(screen.getByText("Design a High-Scale API")).toBeInTheDocument();
    expect(screen.getByText(/expert · social/)).toBeInTheDocument();

    // No hints: targets, required components, failure scenarios, and
    // scoring weights stay server-side.
    expect(screen.queryByText("Benchmark rules")).not.toBeInTheDocument();
    expect(screen.queryByText(/crash · cache/)).not.toBeInTheDocument();
    expect(screen.queryByText("required components")).not.toBeInTheDocument();
    expect(screen.queryByText("targets")).not.toBeInTheDocument();
  });

  it("opens the selected challenge's build workspace", async () => {
    const user = userEvent.setup();
    const { onSelectChallenge, onNotice } = renderView();

    await user.click(await screen.findByText("Build a Social Feed"));

    expect(onSelectChallenge).toHaveBeenCalledWith("social-feed");
    expect(onNotice).toHaveBeenCalledTimes(1);
  });
});
