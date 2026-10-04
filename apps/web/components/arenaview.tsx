"use client";

/**
 * LOADLINE — Arena view.
 *
 * Browse standardized system-design challenges, read the problem statement,
 * build an architecture for it on the canvas, submit that architecture with
 * only a display name, and read the deterministic benchmark result and
 * leaderboard.
 *
 * A challenge shows ONLY the problem idea — the business scale (registered
 * users and daily active users) and the network it must serve. Targets,
 * workload numbers, required components, failure scenarios, and scoring
 * weights are deliberately hidden, so the user designs from the requirement
 * rather than toward a metric.
 *
 * The benchmark is server-authoritative: this view sends ONLY
 * {challenge slug, stored architecture version id, display name}. The
 * workload, failures, seed, metrics, and score all come back from the Go
 * server. Nothing here computes or guesses a benchmark number.
 */
import { useCallback, useEffect, useMemo, useReducer } from "react";
import { createArenaClient, createWorkspaceClient } from "@loadline/api";
import type {
  ArchitectureRecord,
  ArchitectureVersion,
  Challenge,
  ChallengeSummary,
  LeaderboardEntry,
  Project,
  Submission,
} from "@loadline/api";
import { f0, f1, f2, pct, price } from "./format";
import { Badge, Button, EmptyState, Field, Input, Panel, Section, Select } from "./ui";

interface ArenaState {
  challenges: ChallengeSummary[];
  slug: string;
  challenge: Challenge | null;
  leaderboard: LeaderboardEntry[];
  submission: Submission | null;
  displayName: string;
  projects: Project[];
  architectures: ArchitectureRecord[];
  versions: ArchitectureVersion[];
  projectId: string;
  architectureId: string;
  versionId: string;
  busy: string;
  error: string;
}

function initialArena(slug: string): ArenaState {
  return {
    challenges: [],
    slug,
    challenge: null,
    leaderboard: [],
    submission: null,
    displayName: "",
    projects: [],
    architectures: [],
    versions: [],
    projectId: "",
    architectureId: "",
    versionId: "",
    busy: "",
    error: "",
  };
}

type ArenaAction =
  | { type: "busy"; what: string }
  | { type: "idle" }
  | { type: "error"; message: string }
  | { type: "challenges"; challenges: ChallengeSummary[] }
  | { type: "select"; slug: string }
  | { type: "challenge"; challenge: Challenge }
  | { type: "leaderboard"; entries: LeaderboardEntry[] }
  | { type: "submission"; submission: Submission }
  | { type: "clearSubmission" }
  | { type: "name"; name: string }
  | { type: "projects"; projects: Project[] }
  | { type: "architectures"; architectures: ArchitectureRecord[] }
  | { type: "versions"; versions: ArchitectureVersion[] }
  | { type: "selectProject"; id: string }
  | { type: "selectArchitecture"; id: string }
  | { type: "selectVersion"; id: string };

function reducer(s: ArenaState, a: ArenaAction): ArenaState {
  switch (a.type) {
    case "busy":
      return { ...s, busy: a.what, error: "" };
    case "idle":
      return { ...s, busy: "" };
    case "error":
      return { ...s, busy: "", error: a.message };
    case "challenges":
      return { ...s, challenges: a.challenges };
    case "select":
      return { ...s, slug: a.slug, challenge: null, leaderboard: [], submission: null };
    case "challenge":
      return { ...s, challenge: a.challenge };
    case "leaderboard":
      return { ...s, leaderboard: a.entries };
    case "submission":
      return { ...s, submission: a.submission };
    case "clearSubmission":
      return { ...s, submission: null };
    case "name":
      return { ...s, displayName: a.name };
    case "projects":
      return { ...s, projects: a.projects };
    case "architectures":
      return { ...s, architectures: a.architectures };
    case "versions":
      return { ...s, versions: a.versions };
    case "selectProject":
      return { ...s, projectId: a.id, architectureId: "", versionId: "", architectures: [], versions: [] };
    case "selectArchitecture":
      return { ...s, architectureId: a.id, versionId: "", versions: [] };
    case "selectVersion":
      return { ...s, versionId: a.id };
  }
}

export function ArenaView({
  serverUrl,
  initialSlug = "",
  onSelectChallenge,
  onOpenCanvas,
  onNotice,
}: {
  serverUrl: string;
  /** Challenge selected before this view mounted (survives a canvas trip). */
  initialSlug?: string;
  /** Remember the selected challenge so it survives a trip to the canvas. */
  onSelectChallenge?: (slug: string) => void;
  /** Send the user to the canvas to build the selected challenge. */
  onOpenCanvas: () => void;
  onNotice: (message: string) => void;
}) {
  const arena = useMemo(() => createArenaClient({ baseUrl: serverUrl }), [serverUrl]);
  const workspace = useMemo(() => createWorkspaceClient({ baseUrl: serverUrl }), [serverUrl]);
  const [s, d] = useReducer(reducer, initialSlug, initialArena);

  const call = useCallback(
    async <T,>(what: string, fn: () => Promise<T>): Promise<T | null> => {
      d({ type: "busy", what });
      try {
        const out = await fn();
        d({ type: "idle" });
        return out;
      } catch (e) {
        d({ type: "error", message: e instanceof Error ? e.message : String(e) });
        return null;
      }
    },
    [],
  );

  // Challenge list + the stored architecture versions a user can submit.
  useEffect(() => {
    let alive = true;
    void arena
      .listChallenges({})
      .then((res) => alive && d({ type: "challenges", challenges: res.challenges }))
      .catch((e: unknown) => alive && d({ type: "error", message: msg(e) }));
    void workspace
      .listProjects({})
      .then((res) => alive && d({ type: "projects", projects: res.projects }))
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [arena, workspace]);

  // Detail + leaderboard for the selected challenge.
  useEffect(() => {
    if (!s.slug) return;
    let alive = true;
    void arena
      .getChallenge({ slug: s.slug })
      .then((res) => alive && res.challenge && d({ type: "challenge", challenge: res.challenge }))
      .catch((e: unknown) => alive && d({ type: "error", message: msg(e) }));
    void arena
      .getChallengeLeaderboard({ slug: s.slug })
      .then((res) => alive && d({ type: "leaderboard", entries: res.entries }))
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [arena, s.slug]);

  useEffect(() => {
    if (!s.projectId) return;
    let alive = true;
    void workspace
      .listArchitectures({ projectId: s.projectId })
      .then((res) => alive && d({ type: "architectures", architectures: res.architectures }))
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [workspace, s.projectId]);

  useEffect(() => {
    if (!s.architectureId) return;
    let alive = true;
    void workspace
      .listArchitectureVersions({ architectureId: s.architectureId })
      .then((res) => alive && d({ type: "versions", versions: res.versions }))
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [workspace, s.architectureId]);

  const reloadLeaderboard = useCallback(
    async (slug: string) => {
      const res = await arena.getChallengeLeaderboard({ slug }).catch(() => null);
      if (res) d({ type: "leaderboard", entries: res.entries });
    },
    [arena],
  );

  const submit = async () => {
    if (!s.slug || !s.versionId || !s.displayName.trim()) return;
    d({ type: "clearSubmission" });
    const res = await call("benchmark", () =>
      arena.submitChallenge({
        challengeSlug: s.slug,
        architectureVersionId: s.versionId,
        displayName: s.displayName.trim(),
      }),
    );
    if (!res?.submission) return;
    d({ type: "submission", submission: res.submission });
    if (res.submission.status === "completed") {
      onNotice(`benchmarked ${res.submission.displayName} — score ${res.submission.score.toFixed(1)}`);
    } else {
      onNotice(`benchmark failed: ${res.submission.error || "unknown error"}`);
    }
    void reloadLeaderboard(s.slug);
  };

  const canSubmit = s.slug !== "" && s.versionId !== "" && s.displayName.trim() !== "" && s.busy === "";
  const ch = s.challenge;

  return (
    <div className="ws-view">
      {s.error && (
        <div className="banner banner-bad" role="status">
          <span className="banner-mark">!</span>
          <span>{s.error}</span>
        </div>
      )}

      <div className="panel" style={{ marginBottom: 12 }}>
        <header className="panel-header">
          <h2 className="panel-title">Loadline Arena</h2>
          <div className="panel-actions">
            <span className="panel-tag">design it · run it · break it · compete</span>
          </div>
        </header>
        <div className="panel-body">
          <p className="arena-blurb">
            Standardized system-design benchmarks. Pick a challenge, read its
            problem statement, then build an architecture for it on the canvas
            and submit it with only a display name. LOADLINE supplies the
            workload, runs the real simulation engine, and computes a
            deterministic score — the client cannot supply a metric, score, or
            rank.
          </p>
        </div>
      </div>

      <div className="ws-grid">
        {/* --------------------------------------------------- challenge list */}
        <Panel title="Challenges" tag={`${s.challenges.length} active`}>
          {s.challenges.length === 0 ? (
            <EmptyState>
              no challenges yet — start the Go server; definitions ship with it.
            </EmptyState>
          ) : (
            <ul className="ws-list">
              {s.challenges.map((c) => (
                <li key={c.slug} className={`ws-row${c.slug === s.slug ? " active" : ""}`}>
                  <button
                    type="button"
                    className="ws-row-main"
                    onClick={() => {
                      d({ type: "select", slug: c.slug });
                      onSelectChallenge?.(c.slug);
                    }}
                  >
                    <span className="ws-row-name">{c.name}</span>
                    <span className="ws-row-meta">
                      {c.difficulty} · {c.category}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Panel>

        {/* ------------------------------------------------- challenge detail */}
        <Panel title="The challenge" tag={ch ? `${ch.summary?.slug} v${ch.summary?.version}` : "select a challenge"}>
          {!ch ? (
            <EmptyState>select a challenge to read the problem.</EmptyState>
          ) : (
            <div className="ws-detail">
              <div className="ws-detail-head">
                <Badge tone={toneForDifficulty(ch.summary?.difficulty ?? "")}>
                  {ch.summary?.difficulty}
                </Badge>
                <span className="ws-row-meta">{ch.summary?.category}</span>
              </div>
              <p className="arena-desc">{ch.summary?.description}</p>
              <p className="arena-blurb">
                That is the whole brief. No targets, no workload numbers, no
                component list, no failure list — design the architecture from
                the problem, then submit it to be benchmarked.
              </p>
              <Button
                onClick={() => {
                  onOpenCanvas();
                  onNotice(
                    `building ${ch.summary?.name ?? "challenge"} — design it on the canvas, save it, then return to Arena to submit`,
                  );
                }}
              >
                start building →
              </Button>
            </div>
          )}
        </Panel>
      </div>

      {/* ------------------------------------------------------------ submit */}
      <Panel title="Submit architecture" tag="anonymous · display name only">
        {!s.slug ? (
          <EmptyState>select a challenge first.</EmptyState>
        ) : (
          <>
            <div className="ws-run-facts">
              <Field label="display name">
                <Input value={s.displayName} onChange={(v) => d({ type: "name", name: v })} width={160} />
              </Field>
              <Field label="project">
                <Select
                  value={s.projectId}
                  onChange={(v) => d({ type: "selectProject", id: v })}
                  options={[
                    { value: "", label: "select project" },
                    ...s.projects.map((p) => ({ value: p.id, label: p.name })),
                  ]}
                />
              </Field>
              <Field label="architecture">
                <Select
                  value={s.architectureId}
                  onChange={(v) => d({ type: "selectArchitecture", id: v })}
                  options={[
                    { value: "", label: "select architecture" },
                    ...s.architectures.map((a) => ({ value: a.id, label: a.name })),
                  ]}
                />
              </Field>
              <Field label="version">
                <Select
                  value={s.versionId}
                  onChange={(v) => d({ type: "selectVersion", id: v })}
                  options={[
                    { value: "", label: "select version" },
                    ...s.versions.map((v) => ({ value: v.id, label: `v${v.version}` })),
                  ]}
                />
              </Field>
              <Button variant="primary" onClick={submit} disabled={!canSubmit}>
                {s.busy === "benchmark" ? "benchmarking…" : "run benchmark"}
              </Button>
            </div>
            <p className="arena-blurb">
              The benchmark runs the stored architecture version server-side with
              the challenge&apos;s workload, seed, and failures. Metrics and score
              are computed by LOADLINE, never sent from this page.
            </p>
          </>
        )}
      </Panel>

      {/* ------------------------------------------------------------ result */}
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
                        <span className="arena-bar-fill" style={{ width: `${Math.max(0, Math.min(100, c.score))}%` }} />
                      </span>
                      <span className="arena-bar-value">{f1(c.score)}</span>
                      <span className="arena-bar-weight">×{f1(c.weight)}</span>
                    </div>
                  ))}
                  <ul className="arena-why">
                    {s.submission.breakdown.positives.map((p, i) => (
                      <li key={`p${i}`} className="arena-plus">+ {p}</li>
                    ))}
                    {s.submission.breakdown.negatives.map((p, i) => (
                      <li key={`n${i}`} className="arena-minus">− {p}</li>
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
                    <Fact label="monthly cost" value={s.submission.cost ? price(s.submission.cost.total) : "—"} />
                  </div>
                </Section>
              )}
            </div>
          )}
        </Panel>
      )}

      {/* ------------------------------------------------------- leaderboard */}
      <Panel
        title="Leaderboard"
        tag={s.slug ? `${s.leaderboard.length} scored` : "select a challenge"}
      >
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

function toneForDifficulty(d: string): "ok" | "warn" | "bad" | "neutral" {
  switch (d) {
    case "expert":
      return "bad";
    case "advanced":
      return "warn";
    case "intermediate":
      return "neutral";
    default:
      return "ok";
  }
}

function msg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
