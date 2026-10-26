"use client";

/**
 * LOADLINE — Arena challenge picker.
 *
 * The first half of the Arena view. It lists the embedded challenges and
 * shows ONLY the difficulty and category of each; the problem statement
 * itself is rendered by the build workspace (./arenasubmit and the arena
 * brief in app/page.tsx) once a challenge is chosen.
 *
 * Selecting a challenge does not leave the Arena view: the page swaps the
 * picker for the one-page build/run/submit workspace, so the whole Arena
 * loop — read the brief, build, run, submit — happens in one place.
 *
 * The benchmark is server-authoritative. Nothing here computes or guesses a
 * benchmark number.
 */
import { useEffect, useMemo, useReducer } from "react";
import { createArenaClient } from "@loadline/api";
import type { ChallengeSummary } from "@loadline/api";
import { userError } from "../app/usererror";
import { EmptyState, Panel } from "./ui";

interface PickerState {
  challenges: ChallengeSummary[];
  error: string;
}

type PickerAction =
  | { type: "challenges"; challenges: ChallengeSummary[] }
  | { type: "error"; message: string };

function reducer(s: PickerState, a: PickerAction): PickerState {
  switch (a.type) {
    case "challenges":
      return { ...s, challenges: a.challenges };
    case "error":
      return { ...s, error: a.message };
  }
}

export function ArenaView({
  serverUrl,
  onSelectChallenge,
  onNotice,
}: {
  serverUrl: string;
  /** Open a challenge in the build/run/submit workspace. */
  onSelectChallenge: (slug: string) => void;
  onNotice: (message: string) => void;
}) {
  const arena = useMemo(() => createArenaClient({ baseUrl: serverUrl }), [serverUrl]);
  const [s, d] = useReducer(reducer, { challenges: [], error: "" });

  useEffect(() => {
    let alive = true;
    arena
      .listChallenges({})
      .then((res) => alive && d({ type: "challenges", challenges: res.challenges }))
      .catch(
        (e: unknown) =>
          alive && d({ type: "error", message: userError(e) }),
      );
    return () => {
      alive = false;
    };
  }, [arena]);

  return (
    <div className="ws-view">
      {s.error && (
        <div className="banner banner-bad" role="status">
          <span className="banner-mark">!</span>
          <span>{s.error}</span>
        </div>
      )}

      <div className="panel arena-picker-head">
        <header className="panel-header">
          <h2 className="panel-title">Loadline Arena</h2>
          <div className="panel-actions">
            <span className="panel-tag">read it · build it · run it · submit it</span>
          </div>
        </header>
        <div className="panel-body">
          <p className="arena-blurb">
            Standardized system-design benchmarks. Pick a challenge to open a
            single workspace: the problem statement stays above the canvas while
            you build, you can run the live simulation as often as you like, and
            one submit runs the authoritative benchmark and saves it. LOADLINE
            supplies the workload, runs the real engine, and computes a
            deterministic score — the client cannot supply a metric, score, or
            rank.
          </p>
        </div>
      </div>

      <Panel title="Challenges" tag={`${s.challenges.length} active`}>
        {s.challenges.length === 0 ? (
          <EmptyState>
            no challenges yet — start the Go server; definitions ship with it.
          </EmptyState>
        ) : (
          <ul className="ws-list">
            {s.challenges.map((c) => (
              <li key={c.slug} className="ws-row">
                <button
                  type="button"
                  className="ws-row-main"
                  onClick={() => {
                    onSelectChallenge(c.slug);
                    onNotice(`${c.name} — build the architecture, run it, then submit`);
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
    </div>
  );
}
