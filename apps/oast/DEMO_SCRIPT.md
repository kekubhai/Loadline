# 2–3 minute demo script

A spoken walkthrough of the OAST engine, its MCP tools, and the controlled lab.
It is designed around the deterministic `cmd/oast-demo`, which runs the whole
flow in one command with no API key and no internet.

## Before recording

1. Open a terminal in `apps/oast/`.
2. Confirm Go is installed: `go version` (needs Go 1.26+).
3. Dry-run once so the build is cached and you know it's green:

   ```bash
   go run ./cmd/oast-demo/
   ```

   PowerShell is identical:

   ```powershell
   go run ./cmd/oast-demo/
   ```

   Expect the 10 numbered steps and a final `DEMO OK: ...` line (exit code 0).
4. Have `README.md` open to the "Architecture" diagram and `oast_*` tool table,
   and `lab/lab.go` open to the pinned dialer / `validateCallbackURL`.
5. Clear the terminal so the demo output starts clean.

## Timed sequence (target ~2:40)

### 0:00–0:20 — The problem
- **Say:** "Some vulnerabilities are *blind* — a server can be tricked into
  making an outbound request (blind SSRF) or a DNS lookup, but you never see the
  result in the HTTP response. The only reliable signal is an out-of-band
  interaction hitting a server you control. That's what OAST does."
- **Show:** the first paragraph of `README.md`.

### 0:20–0:45 — Architecture and MCP tools
- **Say:** "An engine mints a unique callback identity per test and receives
  HTTP and authoritative DNS callbacks. An MCP server exposes it to an AI agent
  with four tools — create a test, get evidence, list tests, close a test. A
  separate intentionally vulnerable lab plays the victim and imports none of the
  engine."
- **Show:** the architecture diagram and the four-tool table in `README.md`.

### 0:45–1:20 — Create a test and observe an HTTP callback
- **Run:** `go run ./cmd/oast-demo/`
- **Show / point at steps 4–7 as they scroll:**
  - step 5: `oast_create_test` returns a `test_id` and an `http_callback_url`
    like `http://<token>.oast.local:<port>/`.
  - step 6: the lab's `/lab/http-fetch` makes the server-side request.
  - step 7: `oast_get_evidence` returns 1 HTTP record,
    `assessment="observed_interaction_only"`.
- **Key point:** "The agent confirms the server-side request happened *only*
  through the callback — never from the lab's HTTP reply. That is the blind-SSRF
  detection pattern."

### 1:20–1:50 — DNS evidence retrieval
- **Show steps 8–9:** a *separate* test is created, `/lab/dns-lookup` resolves
  `<token>.oast.local` against the authoritative receiver, and
  `oast_get_evidence` returns DNS records for that test.
- **Key point:** "Same out-of-band idea over DNS, and evidence is attributed to
  the correct test — tests are isolated."

### 1:50–2:15 — Scope enforcement and closure
- **Show step 10:** both tests are closed; state becomes `closed` with the
  recorded evidence counts.
- **Say:** "Creation is scope-checked: an out-of-scope target is rejected and
  logged, and no test is created. I can show that in the tests:"
- **Optional run:** `go test ./internal/mcp -run 'OutOfScope' -v`
  (shows `TestCreateRejectsOutOfScopeTarget` and `TestOutOfScopeRejectionIsLogged`).

### 2:15–2:45 — Security boundaries and limitations
- **Say, pointing at `lab/lab.go`:** "The lab is contained: both scenarios are
  off by default, the HTTP client is *pinned* to the local OAST receiver and
  follows no redirects, and DNS is restricted to the callback zone. So this
  demonstrates the vulnerable pattern and the confirmation technique — not
  unrestricted SSRF."
- **Key closing point:** "A callback is `observed_interaction_only` — evidence
  of an interaction, not proof of a vulnerability. Confirming impact means
  correlating it with the request you sent and your authorization."

## Expected observable output (reference)

```text
[ 5/10] create a test through MCP (oast_create_test)
    test_id=oast_...
    http_callback_url=http://<token>.oast.local:<port>/
[ 7/10] retrieve HTTP evidence through MCP (oast_get_evidence)
    observed 1 HTTP record(s); first assessment="observed_interaction_only"
[ 9/10] retrieve DNS evidence through MCP (oast_get_evidence)
    observed 2 DNS record(s); first name="<token>.oast.local."
[10/10] close both tests ...
    oast_... -> state=closed evidence_count=1
    oast_... -> state=closed evidence_count=2

DEMO OK: full HTTP and DNS OAST callbacks were observed through MCP.
```

## Fallback plan (must stay transparent)

If the live run fails during recording:

1. **Say so out loud** — "the live run failed, let me show the verification
   instead." Never present a recording or pasted text as a live execution.
2. Run the test suite, which covers the same end-to-end path:
   `go test ./internal/integration/ -run TestLab -v`
   (`TestLabEndToEndThroughMCP` and `TestLabDNSCallbackIsIsolatedPerTest`).
3. If even that cannot run, walk through `SUBMISSION_CHECKLIST.md` and point to
   the actual recorded results there, clearly labeled as previously captured,
   not live.

Do not disable a safety control or edit the lab to "make the demo work." If
something is genuinely broken, say it is broken.
