# OAST — requirements traceability

Each assignment requirement is mapped to where it is implemented and the test
or artifact that verifies it. Statuses were confirmed by running
`go test -count=1 ./...` from `apps/oast/` (all packages pass) plus
`go run ./cmd/oast-demo/` (full end-to-end, exit 0). `-race` is **unverified on
this host** — no C compiler is installed; see the note at the end.

Legend: **pass** = implemented and covered by a passing test; **n/a** =
explicitly out of scope for the assignment.

## Phase 1 — OAST engine

| Requirement | Implementation | Evidence | Status |
| --- | --- | --- | --- |
| Unpredictable callback tokens | `internal/token/token.go` (128-bit, base32, 26 chars) | `token_test.go` `TestCryptoGeneratorFormatAndUniqueness`, `TestIsValid` | pass |
| Deterministic clock for tests | `internal/clock/clock.go` | used by registry/ratelimit tests | pass |
| Authorized-scope allowlist (host, wildcard, IP, CIDR) | `internal/scope/scope.go` | `scope_test.go` `TestValidatorMatches`, `TestParseTarget`, `TestNormalizeHostname` | pass |
| Fail closed with no scopes configured | `scope.New` → `ErrNoScopes`; `config.Load` requires scopes | `scope_test.go` `TestNewFailsClosedAndRejectsBadPatterns`; `config_test.go` `TestLoadRequiresAuthorizedScopes`; `mcp_test.go` `TestServerFailsClosedWithoutAuthorizedScopes` | pass |
| Scope never resolves DNS / verifies ownership | `scope.go` (declarative only) | package doc + `TestValidatorMatches` | pass |
| Typed HTTP/DNS evidence, `observed_interaction_only` | `internal/evidence/evidence.go` | `evidence_test.go` `TestSizeAndClone`, `TestHashBody` | pass |
| In-memory lifecycle (active/closed/expired), caps | `internal/registry/memory.go` | `memory_test.go` (12 tests) | pass |
| Service applies scope + TTL policy | `internal/service/service.go` | `service_test.go` (6 tests) | pass |
| Global per-listener rate limiting | `internal/ratelimit/ratelimit.go` | `ratelimit_test.go`; receiver tests below | pass |
| HTTP receiver: token from `Host` only, static response | `internal/callback/http.go` | `http_test.go` `TestHTTPCallbackCapturesEvidence` | pass |
| HTTP receiver: unknown/closed/expired → no evidence | `http.go` `ServeHTTP` | `http_test.go` `TestHTTPCallbackUnknownMalformedClosedAndExpired` | pass |
| HTTP receiver: bounded body/headers/query, truncation policy | `http.go` | `http_test.go` `TestHTTPCallbackBodyTruncation`, `...OversizedHeadersAndURI`, `...QueryTruncation` | pass |
| HTTP receiver: sensitive headers redacted | `http.go` `sensitiveHeaders` | `http_test.go` `TestHTTPCallbackCapturesEvidence` | pass |
| HTTP receiver: forwarded headers cannot spoof source IP | `http.go` `remoteIP(r.RemoteAddr)` | `http_test.go` `TestHTTPCallbackCapturesEvidence` (asserts `SourceIP` ignores XFF/Forwarded) | pass |
| DNS receiver: authoritative for one zone only | `internal/callback/dns.go` | `dns_test.go` `TestDNSValidAAndEvidence`, `TestDNSUnknownClosedExpiredAndInvalid` | pass |
| DNS receiver: no recursion/forwarding/zone transfer | `dns.go` (refuses AXFR/IXFR, non-IN, multi-question) | `dns_test.go` `TestDNSRejectsUnsupportedOperationsAndMalformedQueries` | pass |
| DNS receiver: evidence tied to the correct test | `dns.go` + registry token map | `dns_test.go` `TestDNSValidAAndEvidence` | pass |
| Fail-closed configuration from env | `internal/config/config.go` | `config_test.go` (4 tests) | pass |
| `oastd` runs receivers with graceful shutdown | `cmd/oastd/main.go` | `integration_test.go` `TestHTTPAndDNSLifecycle` | pass |

## Phase 2 — MCP integration

| Requirement | Implementation | Evidence | Status |
| --- | --- | --- | --- |
| Official Go MCP SDK, pinned | `go.mod` `modelcontextprotocol/go-sdk v1.8.0` | builds; `mcp_test.go` uses SDK client/server | pass |
| stdio transport (primary) | `mcp/server.go` `RunStdio`; `cmd/oastd` | `cmd/oastd/main.go` wiring | pass |
| Optional Streamable HTTP, loopback-only | `mcp/server.go` `StreamableHTTPHandler`; `config.validateMCPListenHost` | `config_test.go` loopback enforcement; off by default | pass |
| `oast_create_test` (scope-checked, returns URLs/guidance) | `mcp/tools.go` `createTest` | `mcp_test.go` `TestCreateTestReturnsCallbackLocations`, `TestCreateRejectsOutOfScopeTarget`, `TestCreateRejectsMalformedTargetViaMCP` | pass |
| `oast_get_evidence` (cursor + bounded long poll) | `mcp/tools.go` `getEvidence` | `mcp_test.go` `TestGetEvidenceCursorAndLongPoll`, `TestGetEvidenceReturnsDNSCallback`, `TestGetEvidenceWaitTimesOutWithoutEvidence` | pass |
| Long poll respects timeout **and cancellation** | `getEvidence` loop + `registry.WaitForChange` | `mcp_test.go` `TestGetEvidenceRespectsContextCancellation` | pass |
| `oast_list_tests` (no tokens/raw evidence) | `mcp/tools.go` `listTests` | `mcp_test.go` `TestListAndClose`, `TestListDoesNotLeakRawBodyOrToken` | pass |
| `oast_close_test` (idempotent, rejects later evidence) | `mcp/tools.go` `closeTest` | `mcp_test.go` `TestCloseIsIdempotentAndRejectsNewEvidence` | pass |
| Structured, safe errors (no tokens/stack/env) | `mcp/server.go` `toolErrorFrom` | `mcp_test.go` `TestToolErrorsAreStructured` | pass |
| MCP cannot bypass scope / active-limit | handlers call `service` only | `mcp_test.go` `TestActiveTestLimitIsNotBypassedViaMCP`, `TestCreateRejectsOutOfScopeTarget` | pass |

## Phase 3 — controlled lab + end-to-end

| Requirement | Implementation | Evidence | Status |
| --- | --- | --- | --- |
| Lab imports no engine packages | `lab/lab.go` | package doc; build graph | pass |
| Both scenarios disabled by default | `lab.go` enable flags | `lab_test.go` `TestHTTPFetchDisabledByDefault`, `TestDNSLookupDisabledByDefault` | pass |
| Scenario A: only the pinned loopback destination is contacted | `lab.go` pinned `DialContext` | `lab_test.go` `TestHTTPFetchRejectsOutOfZoneWithoutDialing`, `...DialsOnlyPinnedTargetAndCapsBody` | pass |
| Scenario A: metadata/SSRF/credential/scheme rejected | `lab.go` `validateCallbackURL` | `lab_test.go` `TestHTTPFetchRejectsOutOfZoneWithoutDialing` | pass |
| Scenario A: no redirects | `lab.go` `CheckRedirect` | `lab_test.go` `TestHTTPFetchRefusesRedirects` | pass |
| Scenario A: response-size cap, no forwarded headers | `lab.go` `readCapped`, fresh request | `lab_test.go` `TestHTTPFetchDialsOnlyPinnedTargetAndCapsBody` | pass |
| Scenario B: DNS limited to callback zone, pinned resolver | `lab.go` pinned `net.Resolver`, `hostInZone` | `lab_test.go` `TestDNSLookupRejectsOutOfZone` | pass |
| Config fails closed on non-loopback targets | `lab.go` `requireLoopbackHostPort` | `lab_test.go` `TestConfigFailsClosedOnNonLoopbackTarget` | pass |
| Health check + enabled-state reporting | `lab.go` `/healthz` | `lab_test.go` `TestHealthReportsEnabledState` | pass |
| Full workflow through MCP (HTTP + DNS) | `internal/integration/lab_e2e_test.go` | `TestLabEndToEndThroughMCP` | pass |
| Per-test evidence isolation | registry snapshots | `lab_e2e_test.go` `TestLabDNSCallbackIsIsolatedPerTest`; `memory_test.go` `TestSnapshotIsolation` | pass |

## Phase 4 — hardening, reproducibility, submission readiness

| Requirement | Implementation | Evidence | Status |
| --- | --- | --- | --- |
| Concurrent creation respects active limit | `registry/memory.go` | `memory_test.go` `TestConcurrentCreateRespectsActiveLimit` | pass |
| Concurrent append / close race | `registry/memory.go` | `memory_test.go` `TestConcurrentAppendAndClose` | pass |
| Evidence count + byte limits | `registry/memory.go` | `memory_test.go` `TestEvidenceByteLimit`, `TestEvidenceSequenceAssessmentAndLimits` | pass |
| Rate-limit boundaries | `ratelimit`, receivers | `ratelimit_test.go`, `http_test.go` `TestHTTPCallbackRateLimit`, `dns_test.go` `TestDNSRateLimit` | pass |
| Cross-test token isolation | registry | `memory_test.go` `TestSnapshotIsolation`, `TestDuplicateTokenIsRejected` | pass |
| In-memory state loss on restart | registry (no persistence) | `memory_test.go` `TestStateIsNotSharedAcrossInstances` | pass |
| Deterministic, no-API-key end-to-end demo | `cmd/oast-demo/main.go` | `go run ./cmd/oast-demo/` → 10 steps, exit 0 | pass |
| Standalone module build | `go.mod` | `GOWORK=off go build ./...` | pass |
| Repo hygiene (no unrelated/secret/artifact changes) | branch diff | only `apps/oast/` + `go.work*`; secret scan clean | pass |

## Out of scope (per assignment)

AI/LLM features, auth, payments, DB persistence, public DNS deployment, public
MCP endpoints, a frontend/dashboard, automated scanning/exploitation. — **n/a**

## `-race` note

`go test -race ./...` could not be verified here: the Windows host has no C
compiler (`cgo: C compiler "gcc" not found`), which the race detector requires.
Run it on a machine with CGO + GCC/Clang to complete that check. All other
verification was run and passed.
