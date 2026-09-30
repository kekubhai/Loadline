/**
 * @loadline/api — generated Protobuf/Connect types plus thin factories for
 * the LOADLINE service clients. The browser talks only to this package; it
 * never reimplements simulation behavior.
 *
 * Two services:
 *   - SimulationService: ephemeral what-if runs (in-memory, no database).
 *   - WorkspaceService:  persisted projects, architectures, versions,
 *                        workloads, and simulation runs (PostgreSQL).
 */
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { create as createMessage } from "@bufbuild/protobuf";
import { SimulationService } from "./loadline/v1/simulation_pb";
import { WorkspaceService } from "./loadline/v1/workspace_pb";

export * from "./loadline/v1/simulation_pb";
export * from "./loadline/v1/workspace_pb";
export { createMessage as create };

export interface LoadlineClientOptions {
  /** Base URL of the Go Connect server, e.g. http://localhost:8080 */
  baseUrl: string;
  /** Optional fetch override (tests, SSR). */
  fetch?: typeof fetch;
}

/** createLoadlineClient returns a typed SimulationService client. */
export function createLoadlineClient(opts: LoadlineClientOptions) {
  return createClient(SimulationService, createTransport(opts));
}

/** createWorkspaceClient returns a typed WorkspaceService client. */
export function createWorkspaceClient(opts: LoadlineClientOptions) {
  return createClient(WorkspaceService, createTransport(opts));
}

export type LoadlineClient = ReturnType<typeof createLoadlineClient>;
export type WorkspaceClient = ReturnType<typeof createWorkspaceClient>;

function createTransport(opts: LoadlineClientOptions) {
  return createConnectTransport({
    baseUrl: opts.baseUrl,
    // Binary proto keeps payloads compact; Connect handlers accept both.
    useBinaryFormat: true,
    fetch: opts.fetch,
  });
}
