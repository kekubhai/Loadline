package main

// pageHTML is the single-page local console. No external assets or libraries;
// it talks to the /api/* endpoints on this same origin.
const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>OAST Console</title>
<style>
  :root { color-scheme: light dark; --bg:#0f1115; --panel:#171a21; --line:#2a2f3a;
    --fg:#e6e9ef; --muted:#9aa4b2; --accent:#5aa9e6; --ok:#4cc38a; --warn:#e6b35a; --err:#e8706b; }
  * { box-sizing: border-box; }
  body { margin:0; font:14px/1.5 system-ui,Segoe UI,Roboto,sans-serif; background:var(--bg); color:var(--fg); }
  header { padding:14px 20px; border-bottom:1px solid var(--line); background:var(--panel); }
  header h1 { margin:0; font-size:16px; }
  .banner { color:var(--warn); font-size:12px; margin-top:4px; }
  main { max-width:1000px; margin:0 auto; padding:16px; display:grid; gap:16px; grid-template-columns:1fr 1fr; }
  .card { background:var(--panel); border:1px solid var(--line); border-radius:10px; padding:14px; }
  .card.wide { grid-column:1 / -1; }
  h2 { margin:0 0 10px; font-size:13px; text-transform:uppercase; letter-spacing:.05em; color:var(--muted); }
  label { display:block; font-size:12px; color:var(--muted); margin:8px 0 2px; }
  input { width:100%; padding:7px 9px; background:var(--bg); border:1px solid var(--line); border-radius:6px; color:var(--fg); font:inherit; }
  button { margin-top:10px; padding:8px 12px; background:var(--accent); color:#04121f; border:0; border-radius:6px; font-weight:600; cursor:pointer; }
  button.secondary { background:#2a2f3a; color:var(--fg); }
  button:disabled { opacity:.5; cursor:not-allowed; }
  .row { display:flex; gap:8px; flex-wrap:wrap; }
  .row button { flex:1; }
  pre { background:var(--bg); border:1px solid var(--line); border-radius:6px; padding:10px; overflow:auto; max-height:320px; font:12px/1.5 ui-monospace,Consolas,monospace; white-space:pre-wrap; word-break:break-word; }
  code { background:var(--bg); padding:1px 5px; border-radius:4px; }
  .kv { font:12px ui-monospace,monospace; color:var(--muted); margin:6px 0; word-break:break-all; }
  .kv b { color:var(--fg); }
  .pill { font-size:11px; padding:1px 7px; border-radius:999px; border:1px solid var(--line); }
  .ok{color:var(--ok)} .warn{color:var(--warn)} .err{color:var(--err)}
  .note { font-size:12px; color:var(--muted); }
</style>
</head>
<body>
<header>
  <h1>OAST Console <span class="pill warn">local · intentionally vulnerable lab enabled</span></h1>
  <div class="banner">A callback is <b>evidence of an observed interaction</b>, not proof of a vulnerability. Do not expose this console.</div>
</header>
<main>
  <section class="card">
    <h2>1 · Create test (oast_create_test)</h2>
    <label>Target (must be in authorized scope)</label>
    <input id="target" value="lab.local">
    <label>Authorization reference</label>
    <input id="authref" value="console-demo">
    <label>TTL seconds (optional)</label>
    <input id="ttl" type="number" placeholder="default server TTL">
    <button onclick="createTest()">Create test</button>
    <div id="created" class="kv"></div>
  </section>

  <section class="card">
    <h2>2 · Trigger callback (via the lab)</h2>
    <p class="note">Uses the callback locations from the created test.</p>
    <div class="row">
      <button onclick="triggerHTTP()" id="btnHttp" disabled>Trigger HTTP (Scenario A)</button>
      <button onclick="triggerDNS()" id="btnDns" disabled class="secondary">Trigger DNS (Scenario B)</button>
    </div>
    <div id="trigger" class="kv"></div>
  </section>

  <section class="card">
    <h2>3 · Evidence (oast_get_evidence)</h2>
    <label>Test ID</label>
    <input id="evid" placeholder="auto-filled from create">
    <div class="row">
      <button onclick="getEvidence()">Get evidence</button>
      <button onclick="closeTest()" class="secondary">Close test (oast_close_test)</button>
    </div>
    <pre id="evidenceOut">—</pre>
  </section>

  <section class="card">
    <h2>4 · All tests (oast_list_tests)</h2>
    <button onclick="listTests()">Refresh list</button>
    <pre id="listOut">—</pre>
  </section>

  <section class="card wide">
    <h2>Raw output / log</h2>
    <pre id="log">ready.</pre>
  </section>
</main>

<script>
let currentTest = null;

function logln(x){ const el=document.getElementById('log'); el.textContent = (typeof x==='string'?x:JSON.stringify(x,null,2)) + "\n\n" + el.textContent; }

async function api(path, body){
  const opt = body ? {method:'POST', headers:{'content-type':'application/json'}, body:JSON.stringify(body)} : {};
  const r = await fetch(path, opt);
  let data; try { data = await r.json(); } catch(e){ data = {error:{message:'non-JSON response'}}; }
  return {ok:r.ok, status:r.status, data};
}

async function createTest(){
  const ttl = parseInt(document.getElementById('ttl').value||'0',10);
  const body = { target: document.getElementById('target').value.trim(),
                 authorization_ref: document.getElementById('authref').value.trim() };
  if(ttl>0) body.ttl_seconds = ttl;
  const res = await api('/api/create', body);
  logln({create:res});
  if(!res.ok){ document.getElementById('created').innerHTML = '<span class="err">rejected: '+(res.data.error&&res.data.error.code)+' — '+(res.data.error&&res.data.error.message)+'</span>'; return; }
  currentTest = res.data;
  document.getElementById('evid').value = res.data.test_id;
  document.getElementById('btnHttp').disabled = false;
  document.getElementById('btnDns').disabled = false;
  document.getElementById('created').innerHTML =
    '<div><b>test_id:</b> '+res.data.test_id+'</div>'+
    '<div><b>http_callback_url:</b> <code>'+res.data.http_callback_url+'</code></div>'+
    '<div><b>dns_callback_hostname:</b> <code>'+res.data.dns_callback_hostname+'</code></div>'+
    '<div><b>expires_at:</b> '+res.data.expires_at+'  ·  <span class="warn">'+res.data.assessment+'</span></div>';
}

async function triggerHTTP(){
  if(!currentTest) return;
  const res = await api('/api/trigger-http', {url: currentTest.http_callback_url});
  logln({triggerHTTP:res});
  document.getElementById('trigger').innerHTML = res.ok
    ? '<span class="ok">HTTP callback sent — now Get evidence (protocol http)</span>'
    : '<span class="err">'+JSON.stringify(res.data)+'</span>';
}

async function triggerDNS(){
  if(!currentTest) return;
  const res = await api('/api/trigger-dns', {hostname: currentTest.dns_callback_hostname});
  logln({triggerDNS:res});
  document.getElementById('trigger').innerHTML = res.ok
    ? '<span class="ok">DNS lookup sent — now Get evidence (protocol dns)</span>'
    : '<span class="err">'+JSON.stringify(res.data)+'</span>';
}

async function getEvidence(){
  const id = document.getElementById('evid').value.trim();
  if(!id){ return; }
  const res = await api('/api/evidence', {test_id:id});
  document.getElementById('evidenceOut').textContent = JSON.stringify(res.data, null, 2);
  logln({getEvidence:{status:res.status}});
}

async function closeTest(){
  const id = document.getElementById('evid').value.trim();
  if(!id){ return; }
  const res = await api('/api/close', {test_id:id});
  logln({close:res});
  document.getElementById('evidenceOut').textContent = JSON.stringify(res.data, null, 2);
}

async function listTests(){
  const res = await api('/api/tests');
  document.getElementById('listOut').textContent = JSON.stringify(res.data, null, 2);
}

listTests();
</script>
</body>
</html>`
