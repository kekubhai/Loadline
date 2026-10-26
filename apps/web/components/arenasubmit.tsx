"use client";

/**
 * LOADLINE — Arena submission bench.
 *
 * The last step of the one-page Arena workspace. Submitting does two things
 * in order:
 *
 *   1. SAVE — the editor's current architecture is persisted to the
 *      workspace (project → architecture → immutable version). The Arena
 *      project is created on first submit; each submit appends a new
 *      version, so history is append-only and every stored benchmark stays
 *      reproducible.
 *   2. BENCHMARK — the stored version id is submitted to the server, which
 *      runs the real engine with the challenge's workload, failures, and
 *      seed and computes the score.
 *
 * The client sends only {challenge slug, stored version id, display name}.
 * Metrics, score, and rank are the server's; nothing here computes them.
 */
import { useCallback, useEffect, useMemo, useReducer, useState } from "react";
import { createArenaClient, createWorkspaceClient } from "@loadline/api";
import type {
  Architecture,
  ChallengeSummary,
  LeaderboardEntry,
  Submission,
} from "@loadline/api";
import { f0, f1, f2, pct, price } from "./format";
import { userError, userErrorText } from "../app/usererror";
import { Badge, Button, EmptyState, Field, Input, Panel, Section } from "./ui";

/** Workspace project that collects Arena submissions. */
const ARENA_PROJECT = "Arena";

interface BenchState {
  displayName: string;
  busy: string;
  error: string;
  submission: Submission | null;
  leaderboard: LeaderboardEntry[];
}

type BenchAction =
  | { type: "name"; name: string }
  | { type: "busy"; what: string }
  | { type: "idle" }
  | { type: "error"; message: string }
  | { type: "submission"; submission: Submission }
  | { type: "clearSubmission" }
  | { type: "leaderboard"; entries: LeaderboardEntry[] };

const initialState: BenchState = {
  displayName: "",
  busy: "",
  error: "",
  submission: null,
  leaderboard: [],
};

function reducer(s: BenchState, a: BenchAction): BenchState {
  switch (a.type) {
    case "name":
      return { ...s, displayName: a.name };
    case "busy":
      return { ...s, busy: a.what, error: "" };
    case "idle":
      return { ...s, busy: "" };
    case "error":
      return { ...s, busy: "", error: a.message };
    case "submission":
      return { ...s, submission: a.submission };
    case "clearSubmission":
      return { ...s, submission: null };
    case "leaderboard":
      return { ...s, leaderboard: a.entries };
  }
}

export function ArenaSubmit({
  serverUrl,
  challenge,
  architecture,
  onNotice,
}: {
  serverUrl: string;
  /** The challenge being built. Its name labels the saved architecture. */
  challenge: ChallengeSummary;
  /** The editor's current architecture — snapshotted into a version. */
  architecture: Architecture;
  onNotice: (message: string) => void;
}) {
  const arena = useMemo(() => createArenaClient({ baseUrl: serverUrl }), [serverUrl]);
  const workspace = useMemo(
    () => createWorkspaceClient({ baseUrl: serverUrl }),
    [serverUrl],
  );
  const [s, d] = useReducer(reducer, initialState);
  /** Accumulated save steps shown before/while the benchmark runs. */
  const [savedVersionId, setSavedVersionId] = useState("");

  const reloadLeaderboard = useCallback(async () => {
    const res = await arena.getChallengeLeaderboard({ slug: challenge.slug }).catch(() => null);
    if (res) d({ type: "leaderboard", entries: res.entries });
  }, [arena, challenge.slug]);

  useEffect(() => {
    void reloadLeaderboard();
  }, [reloadLeaderboard]);

  /** Find or create the shared Arena project; return its id. */
  const ensureProject = useCallback(async (): Promise<string> => {
    const res = await workspace.listProjects({});
    const found = res.projects.find((p) => p.name === ARENA_PROJECT);
    if (found) return found.id;
    const created = await workspace.createProject({ name: ARENA_PROJECT });
    if (!created.project) throw new Error("the server did not return the created project");
    return created.project.id;
  }, [workspace]);

  /** Snapshot the architecture as a version of the challenge's architecture. */
  const ensureVersion = useCallback(
    async (projectId: string): Promise<string> => {
      const name = challenge.name || challenge.slug;
      const res = await workspace.listArchitectures({ projectId });
      const found = res.architectures.find((a) => a.name === name);
      if (!found) {
        const created = await workspace.createArchitecture({
          projectId,
          name,
          definition: architecture,
        });
        if (!created.version) throw new Error("the server did not return a saved version");
        return created.version.id;
      }
      const created = await workspace.createArchitectureVersion({
        architectureId: found.id,
        definition: architecture,
      });
      if (!created.version) throw new Error("the server did not return a saved version");
      return created.version.id;
    },
    [architecture, challenge.name, challenge.slug, workspace],
  );

  const submit = useCallback(async () => {
    const displayName = s.displayName.trim();
    if (!displayName) return;
    d({ type: "clearSubmission" });
    try {
      d({ type: "busy", what: "save" });
      const projectId = await ensureProject();
      const versionId = await ensureVersion(projectId);
      setSavedVersionId(versionId);

      d({ type: "busy", what: "benchmark" });
      const res = await arena.submitChallenge({
        challengeSlug: challenge.slug,
        architectureVersionId: versionId,
        displayName,
      });
      if (!res.submission) throw new Error("the server returned no submission");
      d({ type: "submission", submission: res.submission });
      d({ type: "idle" });
      if (res.submission.status === "completed") {
        onNotice(`benchmarked ${displayName} — score ${res.submission.score.toFixed(1)}`);
      } else {
        onNotice(
          `benchmark failed: ${userErrorText(res.submission.error || "unknown error")}`,
        );
      }
      void reloadLeaderboard();
    } catch (e) {
      d({ type: "error", message: msg(e) });
    }
  }, [arena, challenge.slug, ensureProject, ensureVersion, onNotice, reloadLeaderboard, s.displayName]);

  const busy = s.busy !== "";
  const canSubmit = s.displayName.trim() !== "" && !busy;

  return (
    <div className="ws-view">
      {s.error && (
        <div className="banner banner-bad" role="status">
          <span className="banner-mark">!</span>
          <span>{s.error}</span>
        </div>
      )}

      <Panel title="Submit" tag="saves a version, then benchmarks it">
        <div className="ws-run">
          <div className="ws-run-facts">
            <Field label="display name">
              <Input
                value={s.displayName}
                onChange={(v) => d({ type: "name", name: v })}
                width={180}
              />
            </Field>
            <Fact
              label="saves to"
              value={savedVersionId ? `version ${savedVersionId.slice(0, 8)}` : `${ARENA_PROJECT} project`}
            />
          </div>
          <Button variant="primary" onClick={() => void submit()} disabled={!canSubmit}>
            {s.busy === "save"
              ? "saving…"
              : s.busy === "benchmark"
                ? "benchmarking…"
                : "submit & benchmark"}
          </Button>
        </div>
        <p className="arena-blurb">
          Submitting snapshots the canvas as an immutable version of{" "}
          <b>{challenge.name}</b> in the workspace, then runs that stored version
          with the challenge&apos;s workload, seed, and failures. The metrics and
          score come from LOADLINE&apos;s engine, never from this page. The run
          above is a what-if you can repeat freely; this submit is the recorded
          benchmark.
        </p>
      </Panel>

      {s.submission && (
        <Panel title="Your result" tag={s.submission.status}>
          {s.submission.status !== "completed" ? (
            <EmptyState>
              benchmark failed — {s.submission.error || "the engine rejected the run"}
            </EmptyState>
          ) : (
            <div className="ws-detail">
              <div className="ws-detail-head">
                <span className="arena-score">{f1(s.submission.score)}</span>
                <span className="arena-score-max">/ 100</span>
                <Badge tone="ok">rank #{String(s.submission.rank)}</Badge>
                <span className="ws-row-meta">seed {String(s.submission.seed)}</span>
              </div>

              {s.submission.breakdown && (
                <Section label="why">
                  {s.submission.breakdown.components.map((c) => (
                    <div key={c.name} className="arena-bar">
                      <span className="arena-bar-label">{c.name}</span>
                      <span className="arena-bar-track">
                        <span
                          className="arena-bar-fill"
                          style={{ width: `${Math.max(0, Math.min(100, c.score))}%` }}
                        />
                      </span>
                      <span className="arena-bar-value">{f1(c.score)}</span>
                      <span className="arena-bar-weight">×{f1(c.weight)}</span>
                    </div>
                  ))}
                  <ul className="arena-why">
                    {s.submission.breakdown.positives.map((p, i) => (
                      <li key={`p${i}`} className="arena-plus">
                        + {p}
                      </li>
                    ))}
                    {s.submission.breakdown.negatives.map((p, i) => (
                      <li key={`n${i}`} className="arena-minus">
                        − {p}
                      </li>
                    ))}
                  </ul>
                </Section>
              )}

              {s.submission.metrics && (
                <Section label="measured">
                  <div className="ws-facts">
                    <Fact label="completed" value={f0(s.submission.metrics.completed)} />
                    <Fact label="p95" value={`${f2(s.submission.metrics.p95Ms)} ms`} />
                    <Fact label="p99" value={`${f2(s.submission.metrics.p99Ms)} ms`} />
                    <Fact label="error rate" value={pct(s.submission.metrics.errorRate)} />
                    <Fact
                      label="monthly cost"
                      value={s.submission.cost ? price(s.submission.cost.total) : "—"}
                    />
                  </div>
                </Section>
              )}
            </div>
          )}
        </Panel>
      )}

      <Panel title="Leaderboard" tag={`${s.leaderboard.length} scored`}>
        {s.leaderboard.length === 0 ? (
          <EmptyState>no submissions yet — be the first to run this benchmark.</EmptyState>
        ) : (
          <div className="ws-table-wrap">
            <table className="ws-table">
              <thead>
                <tr>
                  <th>rank</th>
                  <th>architect</th>
                  <th>score</th>
                  <th>throughput</th>
                  <th>p95</th>
                  <th>p99</th>
                  <th>errors</th>
                  <th>cost</th>
                </tr>
              </thead>
              <tbody>
                {s.leaderboard.map((e) => (
                  <tr key={e.submissionId}>
                    <td>#{String(e.rank)}</td>
                    <td>{e.displayName}</td>
                    <td>{f1(e.score)}</td>
                    <td>{f0(e.throughputRps)} rps</td>
                    <td>{f2(e.p95Ms)} ms</td>
                    <td>{f2(e.p99Ms)} ms</td>
                    <td>{pct(e.errorRate)}</td>
                    <td>{e.costKnown ? price(e.monthlyCostUsd) : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </div>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="ws-fact">
      <span className="ws-fact-label">{label}</span>
      <span className="ws-fact-value">{value}</span>
    </div>
  );
}

function msg(e: unknown): string {
  return userError(e);
}
