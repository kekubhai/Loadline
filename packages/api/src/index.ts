/**
 * @loadline/api — generated Protobuf/Connect types plus a thin factory for
 * the SimulationService client. The browser talks only to this package;
 * it never reimplements simulation behavior.
 */
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { create as createMessage } from "@bufbuild/protobuf";
import { SimulationService } from "./loadline/v1/simulation_pb";

export * from "./loadline/v1/simulation_pb";
export { createMessage as create };

export interface LoadlineClientOptions {
  /** Base URL of the Go Connect server, e.g. http://localhost:8080 */
  baseUrl: string;
  /** Optional fetch override (tests, SSR). */
  fetch?: typeof fetch;
}

/** createLoadlineClient returns a typed SimulationService client. */
export function createLoadlineClient(opts: LoadlineClientOptions) {
  const transport = createConnectTransport({
    baseUrl: opts.baseUrl,
    // Binary proto keeps payloads compact; Connect handlers accept both.
    useBinaryFormat: true,
    fetch: opts.fetch,
  });
  return createClient(SimulationService, transport);
}

export type LoadlineClient = ReturnType<typeof createLoadlineClient>;
