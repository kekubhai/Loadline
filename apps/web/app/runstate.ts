/**
 * LOADLINE — run-outcome state (pure logic, no JSX).
 *
 * The page tracks the terminal outcome of console runs plus the analysis
 * artifacts (results, diagnosis, capacity, cost) it fetches for each
 * terminal run. All artifact values come verbatim from the simulation
 * service; this module only sequences state transitions.
 */
import { create } from "@loadline/api";
import { FinalResultsSchema } from "@loadline/api";
import type {
  Diagnosis,
  FinalResults,
  GetCapacityResponse,
  GetCostEstimateResponse,
  LoadPlan,
  SystemMetrics,
  FailureRecord,
  RunSummary,
} from "@loadline/api";
import type { SimOutcome } from "../components/simconsole";

export type Phase = "idle" | "running" | "done" | "error";

export interface RunState {
  phase: Phase;
  simId: string | null;
  results: FinalResults | null;
  diagnosis: Diagnosis | null;
  capacity: GetCapacityResponse | null;
  cost: GetCostEstimateResponse | null;
  error: string;
}

export const initialRun: RunState = {
  phase: "idle",
  simId: null,
  results: null,
  diagnosis: null,
  capacity: null,
  cost: null,
  error: "",
};

export type RunAction =
  | { type: "reset" }
  | { type: "created"; id: string }
  | { type: "results"; results: FinalResults }
  | { type: "diagnosis"; diagnosis: Diagnosis }
  | { type: "capacity"; capacity: GetCapacityResponse }
  | { type: "cost"; cost: GetCostEstimateResponse }
  | { type: "error"; message: string };

export function runReducer(s: RunState, a: RunAction): RunState {
  switch (a.type) {
    case "reset":
      return initialRun;
    case "created":
      return { ...s, simId: a.id };
    case "results":
      return { ...s, phase: "done", results: a.results };
    case "diagnosis":
      return { ...s, diagnosis: a.diagnosis };
    case "capacity":
      return { ...s, capacity: a.capacity };
    case "cost":
      return { ...s, cost: a.cost };
    case "error":
      return { ...s, phase: "error", error: a.message };
  }
}

/**
 * Run-state derivation for the ops strip. The console owns the live
 * RunStatus while a run executes (it sees the control frames); the page
 * tracks the terminal outcome for the comparison-ready banner.
 */
export interface PageRunState {
  /** Live console status while a run is active. */
  active: boolean;
  /** Last terminal outcome of a console run. */
  outcome: SimOutcome | null;
}

export function pageStatus(
  s: PageRunState,
): { state: "ok" | "warn" | "bad" | "idle"; label: string } {
  if (s.active) return { state: "warn", label: "running" };
  switch (s.outcome?.status) {
    case "completed":
      return { state: "ok", label: `complete · ${s.outcome.simId}` };
    case "stopped":
      return { state: "bad", label: `stopped · ${s.outcome.simId}` };
    case "failed":
      return { state: "bad", label: `failed · ${s.outcome.simId}` };
    default:
      return { state: "idle", label: "idle" };
  }
}

/** Shape of one terminal outcome the page reacts to. */
export type OutcomeHandler = (outcome: SimOutcome) => void;

/** Minimal client surface the artifact fetcher consumes (structural). */
interface OutcomeClient {
  getResults(i: { simulationId: string }): Promise<{
    plan?: LoadPlan;
    metrics?: SystemMetrics;
    failures: FailureRecord[];
    summary?: RunSummary;
  }>;
  getDiagnosis(i: {
    simulationId: string;
    baselineSimulationId: string;
  }): Promise<{ diagnosis?: Diagnosis }>;
  getCapacity(i: { simulationId: string }): Promise<GetCapacityResponse>;
  getCostEstimate(i: { simulationId: string }): Promise<GetCostEstimateResponse>;
}

/**
 * fetchOutcomeArtifacts loads the analysis artifacts for a terminal run:
 * results, diagnosis (vs the last healthy baseline when the run had
 * failures), capacity, and cost. `healthy` says whether THIS run ran
 * without configured failures; `baselineId` is the last healthy run's id
 * (null when the previous run failed — a failed run must never be a
 * baseline).
 */
export async function fetchOutcomeArtifacts(
  outcome: SimOutcome,
  opts: {
    client: OutcomeClient;
    healthy: boolean;
    baselineId: string | null;
    dispatch: (a: RunAction) => void;
  },
): Promise<void> {
  const { client, healthy, baselineId, dispatch } = opts;
  const id = outcome.simId;
  try {
    const res = await client.getResults({ simulationId: id });
    dispatch({
      type: "results",
      results: create(FinalResultsSchema, {
        plan: res.plan,
        metrics: res.metrics,
        failures: res.failures ?? [],
        summary: res.summary,
      }),
    });
    const diag = await client.getDiagnosis({
      simulationId: id,
      baselineSimulationId: healthy ? "" : (baselineId ?? ""),
    });
    if (diag.diagnosis) {
      dispatch({ type: "diagnosis", diagnosis: diag.diagnosis });
    }
    const cap = await client.getCapacity({ simulationId: id });
    dispatch({ type: "capacity", capacity: cap });
    const cost = await client.getCostEstimate({ simulationId: id });
    dispatch({ type: "cost", cost });
  } catch (e) {
    // Analysis artifacts are best-effort: a stopped run or a generic
    // (provider-less) architecture legitimately has no capacity/cost
    // report. Surface the message, keep the results.
    dispatch({
      type: "error",
      message: e instanceof Error ? e.message : String(e),
    });
  }
}
