#!/usr/bin/env bash
# Step 6 wire-level E2E validation: exercises the REAL server binary over
# HTTP with curl, driving the exact workflow the frontend uses.
# Server must be listening on $BASE.
set -u
BASE="${1:-http://127.0.0.1:8099}"
SVC="$BASE/loadline.v1.SimulationService"
OUT="apps/e2e-tmp"   # project-local: Git Bash, curl.exe, and node.exe resolve it identically
rm -rf "$OUT"
mkdir -p "$OUT"
fail=0
check() { # check <name> <condition-exit-code>
  if [ "$2" -eq 0 ]; then echo "  ok   $1"; else echo "  FAIL $1"; fail=1; fi
}

echo "== E2E over the wire: $BASE =="

# --- 1. catalog ---
curl -s -m 10 -X POST "$SVC/ListCatalog" -H "Content-Type: application/json" -d "{}" > "$OUT/catalog.json"
[ "$(grep -o '"service"' "$OUT/catalog.json" | wc -l)" -eq 18 ]
check "ListCatalog returns 18 services" $?

# --- 2. create (baseline, healthy) ---
cat > "$OUT/create1.json" <<'EOF'
{
  "architecture": {
    "schemaVersion": "1",
    "name": "e2e-web",
    "components": [
      {"id": "client", "kind": "COMPONENT_KIND_CLIENT"},
      {"id": "api", "kind": "COMPONENT_KIND_API_SERVER", "provider": "aws", "service": "lambda", "config": {"memoryMb": 512}},
      {"id": "cache", "kind": "COMPONENT_KIND_CACHE", "provider": "aws", "service": "elasticache", "hitRatio": 0.8},
      {"id": "db", "kind": "COMPONENT_KIND_DATABASE", "provider": "aws", "service": "rds", "config": {"storageGb": 100}}
    ],
    "links": [
      {"from": "client", "to": "api"},
      {"from": "api", "to": "cache"},
      {"from": "cache", "to": "db"}
    ]
  },
  "workload": {
    "totalUsers": "10000000", "dau": "1000000", "requestsPerUserPerDay": 40,
    "peakMultiplier": 5, "readWriteRatio": 4, "payloadBytes": "4096"
  },
  "options": {"seed": "7", "durationMs": 10000, "maxRetries": 2, "backoffBaseMs": 5, "timeoutMs": 50, "retryOn": ["api", "cache"]}
}
EOF
curl -s -m 10 -X POST "$SVC/CreateSimulation" -H "Content-Type: application/json" -d @"$OUT/create1.json" > "$OUT/created1.json"
SIM1=$(node -e "console.log(JSON.parse(require('fs').readFileSync('$OUT/created1.json')).simulation.id)")
[ -n "$SIM1" ]
check "CreateSimulation assigns id ($SIM1)" $?

# --- 3. invalid workload rejected cleanly ---
sed 's/"peakMultiplier": 5/"peakMultiplier": 0.5/' "$OUT/create1.json" > "$OUT/bad-wl.json"
CODE=$(curl -s -m 10 -o "$OUT/bad-wl-resp.json" -w "%{http_code}" -X POST "$SVC/CreateSimulation" -H "Content-Type: application/json" -d @"$OUT/bad-wl.json")
[ "$CODE" = "400" ]
check "invalid workload → HTTP 400 (got $CODE)" $?
grep -qi "peak\|multiplier" "$OUT/bad-wl-resp.json"
check "error message explains the problem" $?

# --- 4. invalid architecture rejected cleanly (unknown service) ---
sed 's/"service": "lambda"/"service": "not_a_service"/' "$OUT/create1.json" > "$OUT/bad-arch.json"
CODE=$(curl -s -m 10 -o /dev/null -w "%{http_code}" -X POST "$SVC/CreateSimulation" -H "Content-Type: application/json" -d @"$OUT/bad-arch.json")
[ "$CODE" = "400" ]
check "unknown provider service → HTTP 400 (got $CODE)" $?

# --- 5. results before run rejected ---
CODE=$(curl -s -m 10 -o /dev/null -w "%{http_code}" -X POST "$SVC/GetResults" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM1\"}")
[ "$CODE" != "200" ]
check "GetResults before run rejected (HTTP $CODE)" $?

# --- 6. run baseline ---
curl -s -m 10 -X POST "$SVC/RunSimulation" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM1\"}" > "$OUT/run1.json"
CODE=$(curl -s -m 10 -o /dev/null -w "%{http_code}" -X POST "$SVC/RunSimulation" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM1\"}")
[ "$CODE" != "200" ]
check "double-run rejected with non-200 (got $CODE, Connect FailedPrecondition)" $?

# --- 7. poll to completion ---
for i in $(seq 1 100); do
  curl -s -m 10 -X POST "$SVC/GetSimulationStatus" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM1\"}" > "$OUT/st1.json"
  ST=$(node -e "console.log(JSON.parse(require('fs').readFileSync('$OUT/st1.json')).status)")
  [ "$ST" != "RUN_STATUS_RUNNING" ] && [ "$ST" != "RUN_STATUS_PENDING" ] && break
  sleep 0.2
done
[ "$ST" = "RUN_STATUS_COMPLETED" ]
check "baseline run completed (status=$ST)" $?

# --- 8. results sanity: real traffic + conservation + percentiles ---
curl -s -m 10 -X POST "$SVC/GetResults" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM1\"}" > "$OUT/res1.json"
node -e "
const r = JSON.parse(require('fs').readFileSync('$OUT/res1.json'));
const m = r.metrics;
const gen = Number(m.generated), comp = Number(m.completed), rej = Number(m.rejected ?? 0),
      fail = Number(m.failed ?? 0), fl = Number(m.inFlight ?? 0);
const ok = gen > 0 && comp > 0 &&
  gen === comp + rej + fail + fl &&
  m.p50Ms > 0 && m.p95Ms >= m.p50Ms && m.p99Ms >= m.p95Ms &&
  m.components.length === 4 &&
  r.plan.peakRps > 0 && Number(r.summary.eventsProcessed) > 0;
process.exit(ok ? 0 : 1);
"
check "results: conservation, percentiles, 4 components, plan, summary" $?

# --- 9. capacity + cost on baseline ---
curl -s -m 10 -X POST "$SVC/GetCapacity" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM1\"}" > "$OUT/cap1.json"
node -e "
const r = JSON.parse(require('fs').readFileSync('$OUT/cap1.json'));
const ok = r.reports.length === 3 && r.reports.every(x => x.maxSustainableRps > 0 && Math.abs(x.utilization + x.headroom - 1) < 0.001 && x.assumptions.length > 0);
process.exit(ok ? 0 : 1);
"
check "capacity: 3 reports, headroom+util=1, assumptions" $?
curl -s -m 10 -X POST "$SVC/GetCostEstimate" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM1\"}" > "$OUT/cost1.json"
node -e "
const r = JSON.parse(require('fs').readFileSync('$OUT/cost1.json'));
const ok = r.estimate.total > 0 && r.estimate.assumptions.some(a => a.includes('not live billing'));
process.exit(ok ? 0 : 1);
"
check "cost estimate: positive total, ESTIMATE marker" $?

# --- 10. failure run: cache crash with pass-through, same seed ---
node -e "
const fs = require('fs');
const req = JSON.parse(fs.readFileSync('$OUT/create1.json'));
req.options.failures = [{target: 'cache', type: 'FAILURE_TYPE_CRASH', startMs: 1000, durationMs: 5000, config: {passThrough: true}}];
fs.writeFileSync('$OUT/create2.json', JSON.stringify(req));
"
curl -s -m 10 -X POST "$SVC/CreateSimulation" -H "Content-Type: application/json" -d @"$OUT/create2.json" > "$OUT/created2.json"
SIM2=$(node -e "console.log(JSON.parse(require('fs').readFileSync('$OUT/created2.json')).simulation.id)")
curl -s -m 10 -X POST "$SVC/RunSimulation" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM2\"}" > /dev/null
for i in $(seq 1 100); do
  curl -s -m 10 -X POST "$SVC/GetSimulationStatus" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM2\"}" > "$OUT/st2.json"
  ST=$(node -e "console.log(JSON.parse(require('fs').readFileSync('$OUT/st2.json')).status)")
  [ "$ST" != "RUN_STATUS_RUNNING" ] && [ "$ST" != "RUN_STATUS_PENDING" ] && break
  sleep 0.2
done
[ "$ST" = "RUN_STATUS_COMPLETED" ]
check "failure run completed (status=$ST)" $?

# --- 11. failure must change results (same seed → same arrivals) ---
curl -s -m 10 -X POST "$SVC/GetResults" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM2\"}" > "$OUT/res2.json"
node -e "
const fs = require('fs');
const a = JSON.parse(fs.readFileSync('$OUT/res1.json')).metrics;
const b = JSON.parse(fs.readFileSync('$OUT/res2.json')).metrics;
const db = id => m => Number(m.components.find(c => c.id === id).arrived);
const ok = db('db')(b) > db('db')(a) && b.p99Ms > a.p99Ms;
console.log('  base db arrivals', db('db')(a), '→ crash', db('db')(b), '; p99', a.p99Ms.toFixed(1), '→', b.p99Ms.toFixed(1));
process.exit(ok ? 0 : 1);
"
check "cache crash → db arrivals increase AND p99 latency rises" $?

# --- 12. diagnosis flags db as critical, with baseline deltas ---
curl -s -m 10 -X POST "$SVC/GetDiagnosis" -H "Content-Type: application/json" -d "{\"simulationId\": \"$SIM2\", \"baselineSimulationId\": \"$SIM1\"}" > "$OUT/diag2.json"
node -e "
const d = JSON.parse(require('fs').readFileSync('$OUT/diag2.json')).diagnosis;
const db = d.bottlenecks.find(b => b.componentId === 'db');
const ok = !d.healthy && db && db.severity === 'critical' && db.reasons.length > 0;
process.exit(ok ? 0 : 1);
"
check "diagnosis: unhealthy, db critical with reasons" $?

echo "== result: $([ $fail -eq 0 ] && echo ALL PASS || echo FAILURES) =="
exit $fail
