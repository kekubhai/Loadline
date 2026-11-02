# OAST Lab — intentionally vulnerable demo application

> **WARNING — INTENTIONALLY VULNERABLE.** This application deliberately performs
> server-side HTTP requests and server-side DNS lookups on behalf of its caller
> so that an out-of-band (OAST) callback can be observed. It exists only to
> demonstrate the OAST engine locally. **Do not deploy it anywhere**, do not
> expose it to a network you do not fully control, and never point it at a
> production system or domain.

The lab is the "victim" in the demo. An MCP-driven AI agent (or an operator)
creates an OAST test, feeds the generated callback location to the lab, and the
lab reaches back to the local OAST receiver — producing HTTP or DNS evidence the
agent can retrieve through the `oast_get_evidence` MCP tool.

This package imports **nothing** from the OAST engine. It is a stand-alone
program that the engine merely observes.

## Containment controls

These are enforced in code, not by convention:

- **Disabled by default.** Each scenario returns `403` until explicitly enabled.
- **Pinned HTTP dialer.** Scenario A's HTTP client ignores the request's host
  when opening a socket and always dials one configured loopback target
  (`LAB_OAST_HTTP_TARGET`). It cannot connect to private-network ranges, cloud
  metadata endpoints (`169.254.169.254`, `metadata.google.internal`), or any
  other host — those requests are rejected before any connection.
- **Zone allowlist.** Both scenarios only accept a destination whose host is a
  single label inside the configured callback zone (`<token>.oast.local`).
- **No redirects.** Scenario A never follows redirects.
- **Pinned resolver.** Scenario B's resolver only ever queries one configured
  DNS address (`LAB_OAST_DNS_ADDR`). It is not a general-purpose resolver.
- **Bounded.** Short timeouts, a response-size cap, and no forwarding of caller
  headers or credentials.
- **Loopback-only config.** Startup fails closed unless the pinned HTTP and DNS
  targets are loopback addresses.

## Scenarios

### Scenario A — controlled HTTP callback (SSRF-style)

`POST /lab/http-fetch` with `{"url": "http://<token>.oast.local:<port>/..."}`.
The lab validates the URL against the allowlist and issues a single `GET` to the
pinned OAST HTTP receiver. The receiver records an **HTTP** evidence record.

### Scenario B — controlled DNS callback

`POST /lab/dns-lookup` with `{"hostname": "<token>.oast.local"}`. The lab
validates the hostname and resolves it against the pinned OAST DNS receiver,
which records a **DNS** evidence record.

## Configuration

The process reads environment variables directly (nothing is loaded from a
file). All defaults are local-only and both demos are off.

| Variable | Default | Meaning |
| --- | --- | --- |
| `LAB_LISTEN_ADDR` | `127.0.0.1:8090` | Lab's own control endpoint |
| `LAB_CALLBACK_ZONE` | `oast.local` | Only zone the demos touch; match `OAST_PUBLIC_DOMAIN` |
| `LAB_OAST_HTTP_TARGET` | `127.0.0.1:8081` | Pinned HTTP dial target (OAST receiver); loopback only |
| `LAB_OAST_DNS_ADDR` | `127.0.0.1:5353` | Pinned DNS resolver (OAST receiver); loopback only |
| `LAB_ENABLE_HTTP_CALLBACK` | `false` | Enable Scenario A |
| `LAB_ENABLE_DNS_CALLBACK` | `false` | Enable Scenario B |
| `LAB_HTTP_TIMEOUT` | `3s` | Per-fetch timeout (max `30s`) |
| `LAB_DNS_TIMEOUT` | `3s` | Per-lookup timeout (max `30s`) |
| `LAB_MAX_RESPONSE_BYTES` | `4096` | Response body read cap (max `1048576`) |

Example local-only configuration (never use a production domain or real
secrets):

```bash
export LAB_LISTEN_ADDR="127.0.0.1:8090"
export LAB_CALLBACK_ZONE="oast.local"
export LAB_OAST_HTTP_TARGET="127.0.0.1:8081"
export LAB_OAST_DNS_ADDR="127.0.0.1:5353"
export LAB_ENABLE_HTTP_CALLBACK="true"
export LAB_ENABLE_DNS_CALLBACK="true"
export LAB_HTTP_TIMEOUT="3s"
export LAB_DNS_TIMEOUT="3s"
export LAB_MAX_RESPONSE_BYTES="4096"
```

## Running the full demo locally

Two processes: the OAST engine (`oastd`) and the lab (`labd`). Use matching
domains/ports.

**Terminal 1 — OAST engine (with MCP over stdio for an agent, or just the
receivers):**

```bash
cd apps/oast
export OAST_AUTHORIZED_SCOPES="lab.local"
export OAST_PUBLIC_DOMAIN="oast.local"
# HTTP receiver on 127.0.0.1:8081, DNS on 127.0.0.1:5353 by default.
go run ./cmd/oastd
```

**Terminal 2 — vulnerable lab:**

```bash
cd apps/oast
export LAB_CALLBACK_ZONE="oast.local"
export LAB_OAST_HTTP_TARGET="127.0.0.1:8081"
export LAB_OAST_DNS_ADDR="127.0.0.1:5353"
export LAB_ENABLE_HTTP_CALLBACK="true"
export LAB_ENABLE_DNS_CALLBACK="true"
go run ./lab/cmd/labd
```

PowerShell uses `$env:NAME = "value"` instead of `export`.

### Health check

```bash
curl -s http://127.0.0.1:8090/healthz
```

```json
{
  "status": "ok",
  "warning": "intentionally vulnerable lab; local testing only",
  "callback_zone": "oast.local",
  "http_callback_enabled": true,
  "dns_callback_enabled": true,
  "oast_http_target": "127.0.0.1:8081",
  "oast_dns_addr": "127.0.0.1:5353",
  "max_response_bytes": 4096,
  "timeout_ms": 3000
}
```

### Driving the demo

An MCP agent calls `oast_create_test` to obtain `http_callback_url` and
`dns_callback_hostname`, then calls the lab. The same flow by hand (substitute a
real token from a created test):

```bash
# Scenario A — produce an HTTP callback
curl -s -X POST http://127.0.0.1:8090/lab/http-fetch \
  -H 'content-type: application/json' \
  -d '{"url":"http://<token>.oast.local:8081/"}'

# Scenario B — produce a DNS callback
curl -s -X POST http://127.0.0.1:8090/lab/dns-lookup \
  -H 'content-type: application/json' \
  -d '{"hostname":"<token>.oast.local"}'
```

Then retrieve evidence through MCP with `oast_get_evidence` (test_id from the
create call). The end-to-end flow is covered automatically by
`internal/integration/lab_e2e_test.go`.

### Shutdown

Press `Ctrl+C` in each terminal. Both processes shut down gracefully and hold no
persistent state.

## Error cases

- **Demo disabled** → `403` with a hint naming the enable variable.
- **Destination outside the zone / wrong scheme / credentials in URL / multi-
  label host** → `400`; no connection is attempted.
- **OAST receiver unreachable** → `502` with a hint to check that `oastd` is
  running on the configured target.
- **Unknown or closed token (DNS)** → `200` noting `NXDOMAIN`; the query was
  still sent (and recorded if the token is an active test).

## What a callback proves

An observed callback is evidence that a network interaction reached the OAST
receiver — here, that the lab performed the server-side request it was asked to.
It is **not**, on its own, proof of a real-world vulnerability or impact. In
this lab the behavior is deliberate and sandboxed. Treat all callback content as
untrusted. Every evidence record is marked `observed_interaction_only`.
