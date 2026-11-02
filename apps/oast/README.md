# OAST Engine

Standalone out-of-band application security testing (OAST) callback engine for
LOADLINE. It receives HTTP and authoritative DNS callbacks, records evidence,
and returns it through an in-process registry. An optional Model Context
Protocol (MCP) server exposes the same engine to a locally launched AI agent.

This engine is intentionally narrow. It does **not** include an admin API, a
dashboard, a vulnerable lab, Docker deployment, public DNS, database
persistence, automated scanning, or any outbound/proxy functionality. The MCP
layer is a transport adapter only: authorization, lifecycle, evidence capture,
and limits all remain in the existing engine packages.

## Safety and scope

- Loading fails closed unless `OAST_AUTHORIZED_SCOPES` contains at least one
  valid scope.
- Every test requires an `AuthorizationRef`. This is caller-supplied engagement
  context, not independent proof of authorization.
- Targets are checked against the configured scopes before a test is created.
- Evidence is always marked `observed_interaction_only`. A callback is not proof
  of SSRF or any other vulnerability.
- HTTP receivers use a static response and never forward requests.
- DNS is authoritative for one zone and performs no recursion, forwarding, or
  zone transfers.
- State is in memory and is lost on restart.

Use this engine only against systems you are explicitly authorized to test.

## Layout

```text
cmd/oastd/             HTTP + DNS process entrypoint
cmd/oast-demo/         deterministic end-to-end demo (no API key)
internal/callback/     HTTP and DNS callback receivers
internal/clock/        injectable time source
internal/config/       environment parsing and validation
internal/evidence/     typed interaction records and redaction helpers
internal/integration/  end-to-end HTTP/DNS tests
internal/mcp/          MCP transport adapter over the service layer
internal/ratelimit/    injected-clock token bucket
lab/                   intentionally vulnerable local demo app (see lab/README.md)
internal/registry/     in-memory test/evidence store
internal/scope/        target parsing and authorized-scope matching
internal/service/      test lifecycle use cases
internal/token/        cryptographically random callback tokens
```

## Local development

Prerequisites: Go 1.26 or newer. From the repository root:

```bash
cd apps/oast
go test ./...
go build ./...
```

Run locally with loopback defaults:

```bash
export OAST_AUTHORIZED_SCOPES="lab.local,*.apps.lab.test,10.0.0.0/8"
go run ./cmd/oastd
```

PowerShell equivalent:

```powershell
$env:OAST_AUTHORIZED_SCOPES = "lab.local,*.apps.lab.test,10.0.0.0/8"
go run ./cmd/oastd
```

Defaults bind HTTP to `127.0.0.1:8081` and DNS UDP/TCP to
`127.0.0.1:5353`. DNS callbacks answer with `127.0.0.1` for A and `::1` for AAAA
unless configured otherwise.

The callback receivers have no create-test endpoint. Tests are created either
through `internal/service` inside the engine/tests or through the MCP server
(below). Do not add an unauthenticated administrative HTTP API just to create
tests locally.

## MCP server

The MCP server lets a locally launched AI agent create callback tests and read
structured HTTP/DNS evidence. It calls the same `internal/service` and
`internal/registry` packages as the callback receivers, so scope enforcement,
lifecycle, evidence limits, and the `observed_interaction_only` assessment all
apply unchanged. The engine remains fully usable and testable without starting
any MCP client.

- **SDK:** [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk)
  `v1.8.0`, pinned in `go.mod`.
- **Transports:** `stdio` (recommended for a locally launched server) and an
  optional stateless Streamable HTTP transport bound to loopback. The MCP
  listener is always separate from the callback HTTP and DNS listeners.
- **Security:** the HTTP transport refuses to bind to a non-loopback address.
  Run `stdio` for a client that launches the executable itself; only enable the
  HTTP transport for a deliberately configured local client.

### Running the MCP server

stdio only (callback receivers still run so callbacks can be recorded):

```bash
export OAST_AUTHORIZED_SCOPES="lab.local,*.apps.lab.test,10.0.0.0/8"
export OAST_MCP_TRANSPORT="stdio"
go run ./cmd/oastd
```

PowerShell:

```powershell
$env:OAST_AUTHORIZED_SCOPES = "lab.local,*.apps.lab.test,10.0.0.0/8"
$env:OAST_MCP_TRANSPORT = "stdio"
go run ./cmd/oastd
```

Use `OAST_MCP_TRANSPORT=both` to also expose the loopback Streamable HTTP
endpoint at `http://127.0.0.1:8082/mcp`.

### Connecting a local MCP client

A stdio client launches the built binary directly. Build it first:

```bash
go build -o ./bin/oastd ./cmd/oastd
```

Example client configuration (local development only):

```json
{
  "mcpServers": {
    "loadline-oast": {
      "command": "/absolute/path/to/apps/oast/bin/oastd",
      "env": {
        "OAST_AUTHORIZED_SCOPES": "lab.local,*.apps.lab.test",
        "OAST_MCP_TRANSPORT": "stdio",
        "OAST_PUBLIC_DOMAIN": "oast.local"
      }
    }
  }
}
```

Do not point `OAST_PUBLIC_DOMAIN` or any scope at LOADLINE's production domain.
Use a lab domain you control.

### Tools

All tools return a JSON envelope: `{"data": ...}` on success or
`{"error": {"code", "message"}}` on failure. Failures also set the MCP result's
error flag, so an agent never reads a failed call as a success. Error codes:
`invalid_input`, `unauthorized`, `test_not_found`, `test_closed`,
`test_expired`, `limit_exceeded`, `timeout`, `canceled`, `internal`.

**`oast_create_test`** — create a scoped test.

- Input: `target` (required), `authorization_ref` (required), `ttl_seconds`
  (optional, `0`-`86400`, bounded by `OAST_MAX_TEST_LIFETIME`).
- Output: `test_id`, `target`, `state`, `expires_at`, `http_callback_url`,
  `dns_callback_hostname`, `assessment`, `guidance[]`.
- Out-of-scope targets return `unauthorized` and no test is created. The
  `authorization_ref` is caller-supplied context, not proof of ownership, and
  is never echoed back in output.

```json
{"target": "lab.local", "authorization_ref": "ENG-123", "ttl_seconds": 600}
```

```json
{"data": {
  "test_id": "…",
  "state": "active",
  "expires_at": "2026-11-02T12:34:56Z",
  "http_callback_url": "http://<token>.oast.local:8081/",
  "dns_callback_hostname": "<token>.oast.local",
  "assessment": "observed_interaction_only",
  "guidance": ["Observed callbacks show reachability, not proof of SSRF or exploitation.", "…"]
}}
```

**`oast_get_evidence`** — read evidence for one test.

- Input: `test_id` (required), `after_sequence` (optional cursor),
  `wait_seconds` (optional long poll, `0`-`30`, default `0` = no wait).
- Output: `test_id`, `state`, `evidence[]` (full structured HTTP/DNS records
  with timestamps, source IP, and sequence numbers), `next_after_sequence`.
- Pass the returned `next_after_sequence` back as `after_sequence` to poll
  incrementally without re-reading records. Waiting stops as soon as new
  evidence arrives, the test closes/expires, or the bound is reached.

```json
{"test_id": "…", "after_sequence": 3, "wait_seconds": 10}
```

**`oast_list_tests`** — list the caller's tests.

- Input: `include_closed` (optional, default `false`), `limit` (optional,
  `1`-`200`, default `50`).
- Output: `tests[]` with `id`, `target`, `state`, `created_at`, `expires_at`,
  `evidence_count`, plus `count` and `include_closed`. Newest first.
- List results deliberately omit callback tokens and raw evidence (headers,
  bodies, DNS names).

**`oast_close_test`** — close a test early.

- Input: `test_id` (required).
- Output: `test_id`, `state`, `evidence_count`.
- Closing is idempotent. After a test is closed, later callbacks are rejected
  and not recorded. Unknown tests return `test_not_found`; the call never
  exposes data about other tests.

### What a callback proves

A recorded callback is evidence that a network interaction reached the OAST
listener from some source. It is **not**, on its own, proof of SSRF,
unauthorized access, data exfiltration, or any other vulnerability or impact.
Every evidence record is marked `observed_interaction_only`. Treat all callback
content (headers, bodies, query strings, DNS names, source addresses) as
untrusted attacker-influenced input. Confirming a vulnerability requires
correlating the callback with the request you sent and the authorization for
the engagement.

### Callback URLs

For a test with token `TOKEN`:

```text
HTTP: http://TOKEN.oast.local:8081/path?query=value
DNS:  TOKEN.oast.local
```

The HTTP receiver accepts the token only in the `Host` header. Forwarded-host
and forwarded-IP headers are recorded only as untrusted metadata and are never
used to resolve the test.

### Environment variables

The process reads environment variables directly. The included `.env.example`
is documentation only; it is not loaded automatically.

| Variable | Default | Allowed |
| --- | --- | --- |
| `OAST_HTTP_ADDR` | `127.0.0.1` | IP or hostname |
| `OAST_HTTP_PORT` | `8081` | `0`-`65535` |
| `OAST_DNS_ADDR` | `127.0.0.1` | IP or hostname |
| `OAST_DNS_PORT` | `5353` | `0`-`65535` |
| `OAST_PUBLIC_DOMAIN` | `oast.local` | DNS name, not an IP |
| `OAST_CALLBACK_IPV4` | `127.0.0.1` | IPv4 or empty |
| `OAST_CALLBACK_IPV6` | `::1` | IPv6 or empty |
| `OAST_AUTHORIZED_SCOPES` | **required** | comma-separated hosts, wildcards, CIDRs |
| `OAST_MAX_TEST_LIFETIME` | `1h` | `1m`-`24h` |
| `OAST_DEFAULT_TEST_TTL` | `15m` | `1m`-max lifetime |
| `OAST_MAX_ACTIVE_TESTS` | `100` | `1`-`10000` |
| `OAST_MAX_BODY_BYTES` | `65536` | `1`-`10485760` |
| `OAST_MAX_HEADER_BYTES` | `16384` | `1024`-`1048576` |
| `OAST_MAX_QUERY_BYTES` | `4096` | `128`-`65536` |
| `OAST_MAX_EVIDENCE_PER_TEST` | `500` | `1`-`10000` |
| `OAST_MAX_EVIDENCE_BYTES_PER_TEST` | `1048576` | `1024`-`67108864` |
| `OAST_RATE_LIMIT_PER_SECOND` | `100` | `0.001`-`1000000` |
| `OAST_RATE_LIMIT_BURST` | `200` | `1`-`1000000` |
| `OAST_MCP_TRANSPORT` | `off` | `off`, `stdio`, `http`, `both` |
| `OAST_MCP_HTTP_ADDR` | `127.0.0.1` | loopback address or `localhost` |
| `OAST_MCP_HTTP_PORT` | `8082` | `1`-`65535` |
| `OAST_MCP_HTTP_PATH` | `/mcp` | absolute path, no query/fragment/whitespace |

At least one of `OAST_CALLBACK_IPV4` or `OAST_CALLBACK_IPV6` must be set.

`OAST_MCP_TRANSPORT` defaults to `off`. The MCP layer is only started when it is
set to `stdio`, `http`, or `both`. `OAST_MCP_HTTP_ADDR` is rejected at load time
unless it is a loopback address or `localhost`, because the MCP endpoint is
unauthenticated.

Scope examples:

```text
lab.local
*.apps.lab.test
10.0.0.0/8
192.0.2.10
```

`lab.local` matches that exact host. `*.apps.lab.test` matches subdomains only,
not `apps.lab.test` itself. CIDRs match IP targets.

## HTTP callback behavior

- Resolves the test from the `Host` header only.
- Returns a static `200 ok`, `404` for unknown/closed/expired tokens, `400` for
  an unreadable body, `414` for an oversized request URI, `431` for oversized
  headers, `429` after rate limiting, and `503` when the request context is
  canceled. Evidence-cap drops still return `200` so callbacks cannot probe the
  registry's internal limits.
- Captures method, normalized host, raw host, escaped path, bounded query,
  bounded headers, bounded body, SHA-256 of the captured body, content type,
  source IP, and user agent.
- Redacts `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`,
  `X-Api-Key`, `API-Key`, and `X-Auth-Token` values.
- Never reflects request data and never sets cookies or redirects.

## DNS callback behavior

- UDP and TCP listeners share the same authoritative handler.
- Unknown, malformed, closed, or expired tokens return `NXDOMAIN`.
- Queries outside the configured zone, AXFR/IXFR, non-IN classes, and
  multi-question packets are refused or rejected.
- A and AAAA return configured callback addresses. Other types are recorded but
  return no answer records.
- No recursion, upstream forwarding, or zone transfer is supported.

## Reproducible demo (no API key)

`cmd/oast-demo` runs the entire workflow end-to-end in one process — no LLM API
key, no external DNS, no internet. It starts the engine (HTTP + DNS receivers)
on loopback, starts the intentionally vulnerable lab with both scenarios
enabled, connects a real MCP client to a real MCP server over an in-process
transport, and then: creates a test, triggers an HTTP callback and retrieves the
HTTP evidence, creates a second test, triggers a DNS callback and retrieves the
DNS evidence, and finally closes both tests and prints their lifecycle state.
Every listener and session is cleaned up on exit, and any failed step aborts
with a non-zero exit code (no silent fallback).

Unix shell:

```bash
cd apps/oast
go run ./cmd/oast-demo/
```

Windows PowerShell:

```powershell
cd apps/oast
go run ./cmd/oast-demo/
```

Expected final line: `DEMO OK: full HTTP and DNS OAST callbacks were observed
through MCP.` (exit code 0). The demo binds only `127.0.0.1` on OS-assigned
ports and needs no configuration.

To instead drive the system with your own MCP-compatible client over stdio, run
`oastd` with `OAST_MCP_TRANSPORT=stdio` and the lab as described under
[MCP server](#mcp-server) and in [`lab/README.md`](lab/README.md). The
requirements traceability table is in [`REQUIREMENTS.md`](REQUIREMENTS.md).

### Full end-to-end vs. unit-only

- `go test ./...` runs **everything**, including the real end-to-end flow in
  `internal/integration/` (live loopback receivers + lab + MCP). These tests are
  always-on; they are not skipped and there is no weaker fallback.
- `go run ./cmd/oast-demo/` is the same full flow as a runnable, human-readable
  demonstration.
- Individual package tests (e.g. `go test ./lab/...`) are unit-level and do not,
  by themselves, prove the cross-component workflow. If an integration test
  fails, the suite fails — it does not degrade to unit-only results.

## Verification

```bash
gofmt -l .
go vet ./...
go test ./...
go build ./...
```

The MCP integration tests drive the tools through the SDK's in-memory
client/server transport (`mcp.NewInMemoryTransports`), not by calling handler
functions directly, and require no network access.

To verify the module independently of the Go workspace:

```bash
GOWORK=off go build ./...
GOWORK=off go test ./...
```

For the race detector on Windows, CGO and a C compiler are required:

```powershell
$env:CGO_ENABLED = "1"
go test -race ./...
```

If no C compiler is installed, run `go test ./...` and record that the race
detector was not available. This repository currently verifies with the
non-racy suite on Windows.

## Deployment notes

- The defaults are loopback-only. Do not expose the receivers directly to the
  internet without an intentional deployment review.
- A public deployment needs DNS delegation for the callback zone, TLS in front
  of HTTP, and network controls around the listeners.
- There is no authentication layer for receiving callbacks by design; the
  unguessable token plus the target scope is the access boundary.
- There is no persistence or multi-instance coordination. Restarting the process
  discards tests and evidence.
