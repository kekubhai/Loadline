# Submission checklist

Mandatory assignment requirements, where each is implemented, how it was
verified, and the **actual** result observed on this machine. Nothing is marked
passing that was not actually run. Detailed per-component traceability is in
[`REQUIREMENTS.md`](REQUIREMENTS.md).

All commands below are run from `apps/oast/`.

| # | Requirement | Implementation | Verification | Actual result |
| --- | --- | --- | --- | --- |
| 1 | Unique callback identifier per test | `internal/token` (128-bit base32) + `internal/registry` mints a distinct test ID and callback token per test | `go test ./internal/token ./internal/registry`; `go run ./cmd/oast-demo` shows different tokens per test | **pass** — tests pass; demo minted two distinct tokens |
| 2 | HTTP callback capture | `internal/callback/http.go` | `go test ./internal/callback`; demo steps 6–7 | **pass** — 1 HTTP record captured in demo |
| 3 | DNS callback capture | `internal/callback/dns.go` | `go test ./internal/callback`; demo steps 8–9 | **pass** — DNS records captured in demo |
| 4 | Structured evidence retrieval through MCP | `internal/mcp` `oast_get_evidence` | `go test ./internal/mcp ./internal/integration` | **pass** — HTTP+DNS evidence retrieved via MCP client |
| 5 | Authorized-scope enforcement | `internal/scope`, `internal/service` | `go test ./internal/scope ./internal/service` | **pass** |
| 6 | Out-of-scope rejection **and logging** | reject: `service`/`scope`; log: `mcp/server.go` `logToolError` | `go test ./internal/mcp -run 'OutOfScope\|RejectsOutOfScope'` | **pass** — `TestCreateRejectsOutOfScopeTarget`, `TestOutOfScopeRejectionIsLogged` |
| 7 | Controlled blind-SSRF demo vs. vulnerable lab | `lab/` Scenario A + `internal/integration/lab_e2e_test.go` + `cmd/oast-demo` | `go test ./lab ./internal/integration`; `go run ./cmd/oast-demo` | **pass, with scope caveat** (see note) |
| 8 | Reproducible local setup | `cmd/oast-demo`, README | `go run ./cmd/oast-demo` (exit 0) | **pass** |
| 9 | Public-repository-ready README | `README.md` (+ `lab/README.md`) | manual review | **pass** |
| 10 | 2–3 minute demo plan | [`DEMO_SCRIPT.md`](DEMO_SCRIPT.md) | manual review | **pass** |

## Verification commands actually run (this machine)

```text
gofmt -l .                      -> no output (clean)
go vet ./...                    -> ok
go build ./...                  -> ok
go test -count=1 ./...          -> all packages ok (11 with tests)
GOWORK=off go build ./...       -> ok (standalone module)
GOWORK=off go test -count=1 ./... -> all packages ok
go run ./cmd/oast-demo/         -> 10/10 steps, "DEMO OK", exit 0
```

External MCP client over stdio against the real `oastd` process: verified once
with a throwaway `CommandTransport` check (all four tools listed, test created);
see `REQUIREMENTS.md` → "External client verification".

## Requirement 7 caveat (blind SSRF)

The HTTP scenario demonstrates the blind-SSRF **detection workflow**: the agent
hands the lab a URL, the lab makes a *server-side* request, and the agent
confirms it **only** through the out-of-band OAST callback. Exploitation scope
is deliberately bounded — the lab is pinned to the local OAST receiver by an
allowlist and a pinned dialer — so it demonstrates the vulnerable pattern and
the confirmation technique, not unrestricted access to arbitrary internal
targets. A callback is `observed_interaction_only`: evidence of an interaction,
not proof of a vulnerability.

## Outstanding limitation

- **`-race` not verified on this host.** `CGO_ENABLED=1 go test -race ./...`
  fails with `cgo: C compiler "gcc" not found`; the Windows toolchain has no C
  compiler. Run it in CI or a dev box that has CGO + GCC/Clang:

  ```bash
  # Linux/macOS CI with a C compiler available:
  cd apps/oast && CGO_ENABLED=1 go test -race ./...
  ```

No other requirement is unverified.
