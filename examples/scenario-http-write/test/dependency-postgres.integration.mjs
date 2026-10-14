import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import { once } from "node:events";
import { randomUUID } from "node:crypto";
import http from "node:http";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import pg from "../payment-postgres/client.js";
import { migrate } from "../payment-postgres/migrate.mjs";
import { openPostgresLedger } from "../payment-postgres/ledger.js";
import { createPostgresProvider } from "../payment-postgres/server.js";
import { poolConfig } from "../payment-postgres/database.js";
import { createWriteCaller } from "../app/server.js";
import { order } from "./acceptance.mjs";
import { verifyDependency } from "./dependency-acceptance.mjs";
import { createCommitProxy } from "./pg-commit-proxy.mjs";
import { cleanupNamespace } from "./postgres-cleanup.mjs";

assert.equal(process.env.GREGALE_DEPENDENCY_DATABASE_DISPOSABLE, "1", "requires disposable Postgres opt-in");
const adminURL = process.env.GREGALE_DEPENDENCY_DATABASE_ADMIN_URL;
assert.ok(adminURL, "requires a disposable Postgres administrator URL");
await migrate(adminURL);
const admin = new pg.Pool(poolConfig(adminURL));
admin.on("error", () => {});
// Runtime DML can serve the ledger, but cannot migrate it or own its schema.
await admin.query("CREATE ROLE gregale_write_runtime LOGIN PASSWORD 'fixture-runtime-only' NOSUPERUSER NOCREATEDB NOCREATEROLE");
await admin.query("GRANT USAGE ON SCHEMA public TO gregale_write_runtime");
await admin.query("GRANT SELECT,INSERT,UPDATE,DELETE ON public.gregale_write_ledgers,public.gregale_write_charges TO gregale_write_runtime");
const runtime = new URL(adminURL);
runtime.username = "gregale_write_runtime";
runtime.password = "fixture-runtime-only";
const runtimeURL = runtime.href;
test.after(() => admin.end());

async function listen(server) {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return `http://127.0.0.1:${server.address().port}`;
}
async function close(server) {
  server.cancelHeldWrites?.();
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
}
async function get(url, path) {
  const response = await fetch(url + path, { signal: AbortSignal.timeout(5000) });
  assert.equal(response.status, 200);
  return response.json();
}
async function write(url, key = "in-flight", payload = order) {
  const response = await fetch(url + "/checkout", { method: "POST", headers: { "content-type": "application/json", "idempotency-key": key }, body: JSON.stringify(payload), signal: AbortSignal.timeout(10000) });
  return { status: response.status, body: await response.json() };
}
async function providerProcess(t, namespace, stage) {
  const ledgerModule = new URL("../payment-postgres/ledger.js", import.meta.url).href;
  const serverModule = new URL("../payment-postgres/server.js", import.meta.url).href;
  const childEnv = { ...process.env, DATABASE_URL: runtimeURL, LEDGER_NAMESPACE: namespace, HOLD_STAGE: stage };
  delete childEnv.GREGALE_DEPENDENCY_DATABASE_ADMIN_URL;
  delete childEnv.MIGRATION_DATABASE_URL;
  const child = spawn(process.execPath, ["--input-type=module", "-e", `
    import { openPostgresLedger } from ${JSON.stringify(ledgerModule)};
    import { createPostgresProvider } from ${JSON.stringify(serverModule)};
    try {
      const ledger = await openPostgresLedger({ connectionString: process.env.DATABASE_URL, namespace: process.env.LEDGER_NAMESPACE });
      const server = createPostgresProvider({ ledger, holdStage: process.env.HOLD_STAGE });
      server.listen(0, "127.0.0.1", () => console.log("http://127.0.0.1:" + server.address().port));
    } catch { console.error("fixture provider failed"); process.exitCode=1; }
  `], { env: childEnv, stdio: ["ignore", "pipe", "pipe"] });
  const exited = once(child, "exit");
  t.after(async () => { if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL"); await exited; });
  let output = "";
  const url = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("provider startup timeout")), 5000);
    child.stdout.on("data", chunk => { output += chunk; if (output.includes("\n")) { clearTimeout(timer); resolve(output.trim()); } });
    child.once("exit", () => { clearTimeout(timer); reject(new Error("provider exited before startup")); });
    child.once("error", reject);
  });
  return { child, exited, url };
}
for (const stage of ["pending", "committed"]) {
  test(`dependency SIGKILL at ${stage} retains Postgres intent and reconciles once`, async t => {
    const namespace = randomUUID().replaceAll("-", "");
    t.after(() => cleanupNamespace(runtimeURL, namespace));
    const original = await providerProcess(t, namespace, stage);
    let upstreamURL = original.url;
    const proxy = http.createServer(async (request, response) => {
      try {
        const chunks = [];
        for await (const chunk of request) chunks.push(chunk);
        const upstream = await fetch(upstreamURL + request.url, { method: request.method, headers: request.headers,
          body: request.method === "GET" ? undefined : Buffer.concat(chunks), signal: AbortSignal.timeout(10000) });
        response.writeHead(upstream.status, { "content-type": "application/json" });
        response.end(Buffer.from(await upstream.arrayBuffer()));
      } catch { if (!response.destroyed) { response.writeHead(503); response.end(); } }
    });
    const proxyURL = await listen(proxy);
    const caller = createWriteCaller({ dependencyURL: proxyURL, deadlineMS: 10000 });
    const appURL = await listen(caller);
    t.after(async () => { await close(caller); await close(proxy); });
    assert.equal((await write(appURL, "baseline")).status, 200);
    const interrupted = write(appURL).then(result => result, () => ({ status: "connection_ended" }));
    const deadline = Date.now() + 3000;
    let state;
    while (Date.now() < deadline) {
      state = await get(original.url, "/checkpoints/in-flight");
      if (state.held) break;
      await delay(10);
    }
    assert.equal(state.held, true);
    assert.equal(state.pending, stage === "pending");
    assert.equal(state.committed, stage === "committed");
    assert.equal((await get(appURL, "/in-flight")).active, 1);
    const before = await get(original.url, "/stats");
    const stored = await admin.query("SELECT state,payload FROM public.gregale_write_charges WHERE namespace=$1 AND operation_key='in-flight'", [namespace]);
    assert.equal(stored.rows[0].state, stage === "pending" ? "pending" : "confirmed");
    assert.deepEqual(stored.rows[0].payload, order);
    original.child.kill("SIGKILL");
    assert.deepEqual(await original.exited, [null, "SIGKILL"]);
    assert.equal((await interrupted).status, 503);
    const replacement = await providerProcess(t, namespace, stage);
    upstreamURL = replacement.url;
    const after = await get(replacement.url, "/stats");
    assert.equal(after.ledger_id, before.ledger_id);
    assert.notEqual(after.instance_id, before.instance_id);
    assert.equal(after.held, 0);
    assert.equal(after.pending, before.pending);
    assert.equal(after.charges, before.charges);
    await verifyDependency(appURL, replacement.url, stage);
    await cleanupNamespace(runtimeURL, namespace);
    assert.deepEqual(await cleanupNamespace(runtimeURL, namespace, true), { namespace, passed: true, remaining: 0 });
  });
}

test("concurrent providers share one payload-bound charge and isolate run namespaces", async t => {
  const namespace = randomUUID().replaceAll("-", "");
  const other = randomUUID().replaceAll("-", "");
  const ledgers = await Promise.all(Array.from({ length: 3 }, () => openPostgresLedger({ connectionString: runtimeURL, namespace })));
  const isolated = await openPostgresLedger({ connectionString: runtimeURL, namespace: other });
  t.after(async () => { await Promise.all([...ledgers, isolated].map(ledger => ledger.close())); await cleanupNamespace(runtimeURL, namespace); await cleanupNamespace(runtimeURL, other); });
  assert.equal(new Set(ledgers.map(ledger => ledger.id)).size, 1);
  assert.notEqual(isolated.id, ledgers[0].id);
  await Promise.all(Array.from({ length: 18 }, (_, i) => ledgers[i % 3].bind("parallel", "confirmed", order)));
  assert.equal((await ledgers[0].stats()).charges, 1);
  await assert.rejects(ledgers[1].bind("parallel", "confirmed", { ...order, quantity: 2 }), { status: 409 });
  await ledgers[2].bind("parallel", "pending", order);
  assert.equal((await ledgers[0].get("parallel")).state, "confirmed");
  await isolated.bind("parallel", "confirmed", { ...order, quantity: 2 });
  await cleanupNamespace(runtimeURL, namespace);
  assert.equal((await isolated.stats()).charges, 1);
});

test("runtime has no DDL; missing schema fails startup without a fallback ledger", async () => {
  await assert.rejects(migrate(runtimeURL), /permission denied/);
  const client = new pg.Client(poolConfig(runtimeURL));
  await client.connect();
  try { await assert.rejects(client.query("CREATE TABLE public.forbidden_schema(id int)"), /permission denied/); }
  finally { await client.end(); }
  await admin.query("CREATE DATABASE empty_write_fixture");
  const invalid = new URL(runtimeURL);
  invalid.pathname = "/empty_write_fixture";
  try { await assert.rejects(openPostgresLedger({ connectionString: invalid.href, namespace: randomUUID().replaceAll("-", "") }), /does not exist/); }
  finally { await admin.query("DROP DATABASE empty_write_fixture"); }
});

test("database pool shutdown returns redacted 503 and explicit replay recovers", async t => {
  const namespace = randomUUID().replaceAll("-", "");
  const ledger = await openPostgresLedger({ connectionString: runtimeURL, namespace });
  const provider = createPostgresProvider({ ledger, holdFirstKey: "" });
  const url = await listen(provider);
  t.after(async () => { await close(provider); await ledger.close(); await cleanupNamespace(runtimeURL, namespace); });
  await ledger.close();
  const response = await fetch(url + "/charge", { method: "POST", headers: { "content-type": "application/json", "idempotency-key": "failure" }, body: JSON.stringify(order) });
  assert.equal(response.status, 503);
  const body = await response.text();
  assert.equal(body, '{"error":"storage_unavailable"}');
  assert.ok(!body.includes(runtimeURL));
  const recovered = await openPostgresLedger({ connectionString: runtimeURL, namespace });
  try { await recovered.bind("failure", "confirmed", order); assert.equal((await recovered.stats()).charges, 1); }
  finally { await recovered.close(); }
});

test("failed SQL commit preserves the pending intent and explicit replay commits once", async t => {
  const namespace = randomUUID().replaceAll("-", "");
  const proxy = await createCommitProxy(runtimeURL);
  const ledger = await openPostgresLedger({ connectionString: proxy.connectionString, namespace });
  const provider = createPostgresProvider({ ledger, holdFirstKey: "" });
  const url = await listen(provider);
  t.after(async () => { await close(provider); await ledger.close(); await proxy.close(); await cleanupNamespace(runtimeURL, namespace); });
  await ledger.bind("commit-failure", "pending", order);
  await admin.query(`CREATE FUNCTION public.reject_write_commit() RETURNS trigger LANGUAGE plpgsql AS
    $$BEGIN RAISE EXCEPTION 'fixture commit rejected'; END$$;
    CREATE CONSTRAINT TRIGGER reject_write_commit AFTER UPDATE ON public.gregale_write_charges
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.reject_write_commit()`);
  proxy.arm();
  try {
    const response = await fetch(url + "/charge", { method: "POST", headers: { "content-type": "application/json", "idempotency-key": "commit-failure" }, body: JSON.stringify(order) });
    assert.equal(response.status, 503);
    assert.deepEqual(await response.json(), { error: "storage_unavailable" });
    assert.equal((await ledger.get("commit-failure")).state, "pending");
    assert.equal((await ledger.stats()).charges, 0);
    assert.equal(proxy.evidence().acknowledgements_dropped, 0);
    assert.equal(proxy.evidence().commit_errors_forwarded, 1);
  } finally { await admin.query("DROP TRIGGER reject_write_commit ON public.gregale_write_charges; DROP FUNCTION public.reject_write_commit()"); }
  await ledger.bind("commit-failure", "confirmed", order);
  await ledger.bind("commit-failure", "confirmed", order);
  assert.equal((await ledger.stats()).charges, 1);
});

test("serving refuses migration credentials; TLS and fixture contracts remain explicit", async () => {
  const childEnv = { ...process.env, DATABASE_URL: runtimeURL, MIGRATION_DATABASE_URL: adminURL,
    LEDGER_NAMESPACE: randomUUID().replaceAll("-", ""), LEDGER_TEST_DISPOSABLE: "1" };
  delete childEnv.GREGALE_DEPENDENCY_DATABASE_ADMIN_URL;
  const result = spawnSync(process.execPath, [new URL("../payment-postgres/start.js", import.meta.url).pathname],
    { env: childEnv, encoding: "utf8", timeout: 2000 });
  assert.equal(result.status, 1);
  assert.ok(!result.stderr.includes(adminURL));
  assert.ok(!result.stderr.includes(runtimeURL));
  assert.match(result.stderr, /Postgres provider startup failed/);
  assert.equal(poolConfig("postgres://runtime:private@example.invalid/db?sslmode=require").ssl.rejectUnauthorized, true);
  assert.throws(() => poolConfig("postgres://runtime:private@example.invalid/db?sslmode=no-verify"));
  for (const file of ["contract.js", "checkpoints.js"]) {
    assert.equal(await readFile(new URL("../payment-postgres/" + file, import.meta.url), "utf8"), await readFile(new URL("../payment/" + file, import.meta.url), "utf8"));
  }
});

const tlsDirectory = process.env.GREGALE_DEPENDENCY_TLS_DIRECTORY;
assert.ok(tlsDirectory, "requires disposable TLS fixture identity");
const tlsIdentity = { key: await readFile(tlsDirectory + "/key.pem"), cert: await readFile(tlsDirectory + "/cert.pem"),
  ca: await readFile(tlsDirectory + "/cert.pem"), servername: "localhost" };
const encryptedRuntime = new URL(runtimeURL);
encryptedRuntime.searchParams.set("sslmode", "verify-full");

for (const encrypted of [false, true]) test(`${encrypted ? "TLS" : "plaintext"} lost COMMIT acknowledgement reports unknown; independent SQL proves commit and replay reconciles once`, async t => {
  const namespace = randomUUID().replaceAll("-", "");
  const proxy = await createCommitProxy(encrypted ? encryptedRuntime.href : runtimeURL, encrypted ? { tls: tlsIdentity } : {});
  const ledger = await openPostgresLedger({ connectionString: proxy.connectionString, namespace });
  const provider = createPostgresProvider({ ledger, holdFirstKey: "" });
  const providerURL = await listen(provider);
  const caller = createWriteCaller({ dependencyURL: providerURL });
  const appURL = await listen(caller);
  t.after(async () => { await close(caller); await close(provider); await ledger.close(); await proxy.close(); await cleanupNamespace(runtimeURL, namespace); });
  proxy.arm();
  const first = await fetch(providerURL + "/charge", { method: "POST", headers: { "content-type": "application/json", "idempotency-key": "ambiguous-commit" }, body: JSON.stringify(order) });
  assert.equal(first.status, 503);
  assert.deepEqual(await first.json(), { error: "commit_outcome_unknown", payment_outcome: "unknown", retryable: true });
  assert.equal(proxy.evidence().acknowledgements_dropped, 1);
  assert.equal(proxy.evidence().commits_forwarded, 1);
  if (encrypted) { assert.ok(proxy.evidence().tls_connections_verified >= 1); assert.equal(proxy.evidence().tls_failures, 0); }
  const witness = await admin.query("SELECT state,payload FROM public.gregale_write_charges WHERE namespace=$1 AND operation_key=$2", [namespace, "ambiguous-commit"]);
  assert.deepEqual(witness.rows, [{ state: "confirmed", payload: order }]);
  assert.equal((await get(providerURL, "/stats")).attempts, 1);
  assert.equal((await write(appURL, "ambiguous-commit", { ...order, quantity: 2 })).status, 409);
  const replays = await Promise.all(Array.from({ length: 4 }, () => write(appURL, "ambiguous-commit")));
  for (const result of replays) { assert.equal(result.status, 200); assert.equal(result.body.status, "confirmed"); assert.equal(result.body.attempts, 1); }
  const after = await get(providerURL, "/stats");
  assert.equal(after.charges, 1); assert.equal(after.pending, 0);
  assert.equal(after.active_writes, 0); assert.equal(after.queued_writes, 0);
  assert.equal(after.pool_waiting, 0); assert.equal(after.pool_total, after.pool_idle);
  assert.equal(after.attempts, 6);
  assert.equal(proxy.evidence().acknowledgements_dropped, 1);
  assert.equal(proxy.evidence().invalid_frames, 0);
  const transactions = await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE application_name=$1 AND state LIKE 'idle in transaction%'", ["gregale-write-ledger-" + namespace]);
  assert.equal(transactions.rows[0].count, 0);
  if (encrypted) {
    const transport = await admin.query("SELECT ssl,version FROM pg_stat_ssl JOIN pg_stat_activity USING(pid) WHERE application_name=$1", ["gregale-write-ledger-" + namespace]);
    assert.ok(transport.rows.length > 0);
    for (const row of transport.rows) { assert.equal(row.ssl, true); assert.match(row.version, /^TLSv1\.[23]$/); }
    assert.equal(proxy.evidence().tls_failures, 0);
  }
  await ledger.close(); await proxy.close();
  assert.equal(proxy.evidence().connections, 0);
});

for (const encrypted of [false, true]) test(`${encrypted ? "TLS" : "plaintext"} caller observes unknown after a lost commit reply and explicit replay confirms it`, async t => {
  const namespace = randomUUID().replaceAll("-", "");
  const proxy = await createCommitProxy(encrypted ? encryptedRuntime.href : runtimeURL, encrypted ? { tls: tlsIdentity } : {});
  const ledger = await openPostgresLedger({ connectionString: proxy.connectionString, namespace });
  const provider = createPostgresProvider({ ledger, holdFirstKey: "" });
  const providerURL = await listen(provider);
  const caller = createWriteCaller({ dependencyURL: providerURL });
  const appURL = await listen(caller);
  t.after(async () => { await close(caller); await close(provider); await ledger.close(); await proxy.close(); await cleanupNamespace(runtimeURL, namespace); });
  proxy.arm();
  const first = await write(appURL, "caller-ambiguous");
  assert.equal(first.status, 503); assert.equal(first.body.payment_outcome, "unknown"); assert.equal(first.body.attempts, 1);
  assert.equal(proxy.evidence().acknowledgements_dropped, 1);
  assert.equal((await ledger.get("caller-ambiguous")).state, "confirmed");
  assert.equal((await write(appURL, "caller-ambiguous")).status, 200);
  assert.equal((await ledger.stats()).charges, 1);
});

async function eventually(check, timeoutMS = 7000) {
  const end = Date.now() + timeoutMS;
  while (Date.now() < end) {
    const value = await check();
    if (value) return value;
    await delay(10);
  }
  throw new Error("dependency recovery condition timed out");
}

for (const scenario of ["late-ack", "bounded-stall", "database-client-deadline", "request-delay", "request-stall"]) {
  test(`TLS ${scenario}: unknown write reconciles once and joins held work`, { timeout: 15000 }, async t => {
    const namespace = randomUUID().replaceAll("-", "");
    const proxy = await createCommitProxy(encryptedRuntime.href, { tls: tlsIdentity });
    const ledger = await openPostgresLedger({ connectionString: proxy.connectionString, namespace });
    const outcomes = [];
    const observedLedger = { ...ledger, async bind(...args) {
      try {
        const result = await ledger.bind(...args);
        outcomes.push("acknowledged");
        return result;
      } catch (error) { outcomes.push(error.outcomeUnknown ? "unknown" : "rejected"); throw error; }
    } };
    const provider = createPostgresProvider({ ledger: observedLedger, holdFirstKey: "" });
    const providerURL = await listen(provider);
    const caller = createWriteCaller({ dependencyURL: providerURL, deadlineMS: 400 });
    const appURL = await listen(caller);
    t.after(async () => { await close(caller); await close(provider); await proxy.close(); await ledger.close(); await cleanupNamespace(runtimeURL, namespace); });
    const key = "slow-commit";
    const requestFault = scenario.startsWith("request-");
    if (requestFault) await ledger.bind(key, "pending", order);
    const previousCommits = proxy.evidence().commits_forwarded;
    const durationMS = scenario === "database-client-deadline" ? 5000 : 1200;
    proxy.arm({ mode: scenario.endsWith("stall") ? "stall" : "delay", direction: requestFault ? "request" : "response", durationMS });
    const original = scenario === "database-client-deadline" ? fetch(providerURL + "/charge", {
      method: "POST", headers: { "content-type": "application/json", "idempotency-key": key },
      body: JSON.stringify(order), signal: AbortSignal.timeout(10000),
    }).then(async response => ({ status: response.status, body: await response.json() })) : write(appURL, key);
    await eventually(() => proxy.evidence().active_faults === 1);
    const before = proxy.evidence();
    assert.equal(before.faults_fired, 1); assert.equal(before.commits_forwarded, previousCommits + (requestFault ? 0 : 1));
    assert.equal(before.faults[0].outcome, "holding");
    // This query uses an independent connection while the acknowledgement is held.
    const witness = await admin.query("SELECT state,payload FROM public.gregale_write_charges WHERE namespace=$1 AND operation_key=$2", [namespace, key]);
    assert.deepEqual(witness.rows, [{ state: requestFault ? "pending" : "confirmed", payload: order }]);
    const first = await original;
    assert.equal(first.status, 503); assert.equal(first.body.payment_outcome, "unknown");
    if (scenario === "database-client-deadline") assert.equal(first.body.error, "commit_outcome_unknown");
    else {
      assert.equal(first.body.error, "payment_unavailable");
      assert.equal(proxy.evidence().faults[0].outcome, "holding");
      assert.equal((await get(providerURL, "/stats")).active_writes, 1);
    }
    assert.equal((await get(appURL, "/in-flight")).active, 0);
    const recovered = await eventually(async () => {
      const stats = await get(providerURL, "/stats");
      return proxy.evidence().active_faults === 0 && stats.active_writes === 0 && stats.queued_writes === 0 && stats;
    });
    assert.equal(recovered.attempts, 1); // No hidden retry after either deadline.
    assert.deepEqual(outcomes, [scenario === "late-ack" || scenario === "request-delay" ? "acknowledged" : "unknown"]);
    assert.equal(recovered.charges, scenario === "request-stall" ? 0 : 1);
    assert.equal(recovered.pending, scenario === "request-stall" ? 1 : 0);
    assert.equal(recovered.pool_waiting, 0); assert.equal(recovered.pool_total, recovered.pool_idle);
    const fault = proxy.evidence().faults[0];
    assert.equal(fault.configured_ms, durationMS);
    assert.equal(fault.outcome, scenario === "late-ack" ? "released" : scenario.endsWith("stall") ? "timed_out" : scenario === "request-delay" ? "released" : "canceled");
    if (scenario === "database-client-deadline") {
      assert.ok(fault.elapsed_ms >= 3000 && fault.elapsed_ms < durationMS);
    } else assert.ok(fault.elapsed_ms >= durationMS);
    assert.equal(proxy.evidence().acknowledgements_dropped, scenario === "bounded-stall" ? 1 : 0);
    const transactions = await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE application_name=$1 AND state LIKE 'idle in transaction%'", ["gregale-write-ledger-" + namespace]);
    assert.equal(transactions.rows[0].count, 0);
    assert.equal((await write(appURL, key, { ...order, quantity: 2 })).status, 409);
    const replays = await Promise.all(Array.from({ length: 4 }, () => write(appURL, key)));
    for (const replay of replays) {
      assert.equal(replay.status, 200); assert.equal(replay.body.status, "confirmed"); assert.equal(replay.body.attempts, 1);
    }
    const after = await get(providerURL, "/stats");
    assert.equal(after.charges, 1); assert.equal(after.pending, 0); assert.equal(after.attempts, 6);
    assert.equal(after.active_writes, 0); assert.equal(after.queued_writes, 0);
    assert.equal(after.pool_waiting, 0); assert.equal(after.pool_total, after.pool_idle);
    const sessions = await admin.query("SELECT state,ssl,version FROM pg_stat_activity JOIN pg_stat_ssl USING(pid) WHERE application_name=$1", ["gregale-write-ledger-" + namespace]);
    assert.ok(sessions.rows.length > 0);
    for (const session of sessions.rows) {
      assert.equal(session.state, "idle"); assert.equal(session.ssl, true); assert.match(session.version, /^TLSv1\.[23]$/);
    }
    assert.equal(proxy.evidence().faults_fired, 1); assert.equal(proxy.evidence().fault_timers, 0);
    assert.equal(proxy.evidence().invalid_frames, 0); assert.equal(proxy.evidence().tls_failures, 0);
    await ledger.close(); await proxy.close();
    assert.equal(proxy.evidence().connections, 0); assert.equal(proxy.evidence().active_faults, 0);
  });
}

for (const mode of ["complete", "clear-during-read", "deadline", "proxy-close"]) {
  test(`TLS bandwidth-limited large read ${mode} verifies transfer and cleanup`, { timeout: 10000 }, async t => {
    const proxy = await createCommitProxy(encryptedRuntime.href, { tls: tlsIdentity });
    const applicationName = "gregale-bandwidth-" + randomUUID().replaceAll("-", "");
    const clients = [];
    async function connect() {
      const client = new pg.Client({ ...poolConfig(proxy.connectionString), application_name: applicationName,
        query_timeout: mode === "deadline" ? 350 : 4000 });
      client.on("error", () => {}); clients.push(client); await client.connect();
      await client.query("SELECT 1");
      return client;
    }
    t.after(async () => { await proxy.close(); await Promise.all(clients.map(client => client.end())); });
    const client = await connect();
    const bytes = mode === "complete" ? 32768 : 65536;
    const rate = mode === "complete" ? 131072 : 8192;
    proxy.limitBandwidth({ direction: "response", bytesPerSecond: rate });
    // Handle failure immediately, including shutdown while a partial frame is read.
    const reading = client.query("SELECT repeat('x',$1)::text AS payload", [bytes]).then(
      value => ({ value }), error => ({ error }));
    await eventually(() => proxy.evidence().bandwidth[0].bytes_forwarded >= 100);
    if (mode === "clear-during-read") proxy.clearBandwidth("response");
    if (mode === "proxy-close") await proxy.close();
    const result = await reading;
    const record = proxy.evidence().bandwidth[0];
    assert.ok(record.bytes_forwarded > 0);
    if (mode === "complete" || mode === "clear-during-read") {
      assert.equal(result.error, undefined); assert.equal(result.value.rows[0].payload, "x".repeat(bytes));
      if (mode === "complete") {
        assert.ok(record.bytes_forwarded > bytes);
        assert.ok(record.elapsed_ms >= record.bytes_forwarded / rate * 1000);
      } else assert.ok(record.bytes_forwarded < bytes);
    } else {
      assert.ok(result.error); assert.ok(record.bytes_forwarded < bytes);
      if (mode === "deadline") assert.match(result.error.message, /Query read timeout/);
      // A timed-out read leaves a partial protocol frame. Retire that connection.
      await client.end();
      await eventually(() => proxy.evidence().bandwidth_pending_transfers === 0 && proxy.evidence().connections === 0);
      assert.ok(proxy.evidence().bandwidth[0].canceled_transfers >= 1);
    }
    if (mode !== "proxy-close") {
      proxy.clearBandwidth("response");
      const recovered = mode === "deadline" ? await connect() : client;
      const before = proxy.evidence().bandwidth[0].bytes_forwarded;
      const fresh = await recovered.query("SELECT repeat('x',$1)::text AS payload", [bytes]);
      assert.equal(fresh.rows[0].payload, "x".repeat(bytes));
      assert.equal(proxy.evidence().bandwidth[0].bytes_forwarded, before);
      const session = await admin.query("SELECT state,ssl,version FROM pg_stat_activity JOIN pg_stat_ssl USING(pid) WHERE application_name=$1", [applicationName]);
      assert.equal(session.rows.length, 1); assert.equal(session.rows[0].state, "idle");
      assert.equal(session.rows[0].ssl, true); assert.match(session.rows[0].version, /^TLSv1\.[23]$/);
    }
    await Promise.all(clients.map(client => client.end())); await proxy.close();
    const evidence = proxy.evidence();
    assert.equal(evidence.bandwidth_active_limits, 0); assert.equal(evidence.bandwidth_pending_transfers, 0);
    assert.equal(evidence.bandwidth_buffered_bytes, 0); assert.equal(evidence.bandwidth_timers, 0);
    assert.equal(evidence.connections, 0); assert.equal(evidence.invalid_frames, 0); assert.equal(evidence.tls_failures, 0);
    await eventually(async () => {
      const remaining = await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE application_name=$1", [applicationName]);
      return remaining.rows[0].count === 0;
    });
  });
}

for (const mode of ["cut", "paced-cut", "clear-before-read", "unreached"]) {
  test(`TLS row interruption ${mode} rejects partial results and recovers on a fresh stream`, { timeout: 10000 }, async t => {
    const proxy = await createCommitProxy(encryptedRuntime.href, { tls: tlsIdentity });
    const applicationName = "gregale-read-cut-" + randomUUID().replaceAll("-", "");
    const clients = [];
    async function connect() {
      const client = new pg.Client({ ...poolConfig(proxy.connectionString), application_name: applicationName });
      client.on("error", () => {}); clients.push(client); await client.connect(); await client.query("SELECT 1"); return client;
    }
    t.after(async () => { await proxy.close(); await Promise.all(clients.map(client => client.end())); });
    const client = await connect();
    const originalPID = (await client.query("SELECT pg_backend_pid() AS pid")).rows[0].pid;
    proxy.armReadCut({ afterBytes: 32768 });
    if (mode === "paced-cut") proxy.limitBandwidth({ direction: "response", bytesPerSecond: 262144 });
    if (mode === "clear-before-read") proxy.clearReadCut();
    const bytes = mode === "unreached" ? 1024 : 65536;
    let rowEvents = 0;
    const rows = [];
    // Row callbacks must not expose a successfully parsed partial large row.
    const query = new pg.Query({ text: "SELECT repeat('x',$1)::text AS payload", values: [bytes] });
    query.on("row", row => { rowEvents++; rows.push(row); });
    const result = new Promise(resolve => client.query(query, (error, value) => resolve(error ? { error } : { value })));
    const first = await result;
    if (mode === "cut" || mode === "paced-cut") {
      assert.ok(first.error); assert.equal(first.value, undefined); assert.equal(rowEvents, 0);
      assert.match(first.error.message, /connection|Connection|terminated/);
      const evidence = proxy.evidence();
      assert.equal(evidence.read_cuts_fired, 1); assert.equal(evidence.read_cut_armed, false);
      assert.equal(evidence.read_cuts[0].after_bytes, 32768); assert.equal(evidence.read_cuts[0].prefix_flushed_bytes, 32768);
      // A one-column text DataRow is 5 header + 2 column count + 4 length + data.
      assert.equal(evidence.read_cuts[0].frame_bytes, bytes + 11); assert.equal(evidence.read_cuts[0].outcome, "cut");
      await client.end();
      await eventually(() => proxy.evidence().connections === 0 && proxy.evidence().active_read_cuts === 0);
      if (mode === "paced-cut") {
        const transfer = proxy.evidence().bandwidth[0];
        assert.ok(transfer.bytes_forwarded >= 32768);
        assert.ok(transfer.elapsed_ms >= transfer.bytes_forwarded / transfer.bytes_per_second * 1000);
        proxy.clearBandwidth("response");
      }
    } else {
      assert.equal(first.error, undefined); assert.equal(rowEvents, 1);
      assert.deepEqual(rows, [{ payload: "x".repeat(bytes) }]);
      assert.equal(first.value.rowCount, 1); assert.equal(proxy.evidence().read_cuts_fired, 0);
      assert.equal(proxy.evidence().read_cut_armed, mode === "unreached");
      proxy.clearReadCut(); await client.end();
    }
    await eventually(async () => {
      const retired = await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE pid=$1", [originalPID]);
      return retired.rows[0].count === 0;
    });
    const fresh = await connect();
    const freshPID = (await fresh.query("SELECT pg_backend_pid() AS pid")).rows[0].pid;
    assert.notEqual(freshPID, originalPID);
    const recovered = await fresh.query("SELECT repeat('x',$1)::text AS payload", [65536]);
    assert.equal(recovered.rows[0].payload, "x".repeat(65536));
    assert.equal(proxy.evidence().read_cuts_fired, mode === "cut" || mode === "paced-cut" ? 1 : 0);
    const session = await admin.query("SELECT state,ssl,version FROM pg_stat_activity JOIN pg_stat_ssl USING(pid) WHERE application_name=$1", [applicationName]);
    assert.equal(session.rows.length, 1); assert.equal(session.rows[0].state, "idle");
    assert.equal(session.rows[0].ssl, true); assert.match(session.rows[0].version, /^TLSv1\.[23]$/);
    await fresh.end(); await proxy.close();
    const after = proxy.evidence();
    assert.equal(after.active_read_cuts, 0); assert.equal(after.read_cut_armed, false);
    assert.equal(after.bandwidth_buffered_bytes, 0); assert.equal(after.bandwidth_timers, 0);
    assert.equal(after.bandwidth_pending_transfers, 0); assert.equal(after.connections, 0);
    assert.equal(after.invalid_frames, 0); assert.equal(after.tls_failures, 0);
    await eventually(async () => {
      const remaining = await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE application_name=$1", [applicationName]);
      return remaining.rows[0].count === 0;
    });
  });
}

for (const phase of ["ssl_request", "authentication"]) for (const mode of ["bounded-retries", "queued-acquisitions", "release", "expire", "shutdown"]) {
  test(`${phase} connection stall ${mode} bounds attempts and recovers a verified pool`, { timeout: 10000 }, async t => {
    const proxy = await createCommitProxy(encryptedRuntime.href, { tls: tlsIdentity });
    const applicationName = "gregale-connect-stall-" + randomUUID().replaceAll("-", "");
    const baseline = await admin.query("SELECT pid FROM pg_stat_activity WHERE backend_type='client backend'");
    const baselinePIDs = baseline.rows.map(row => row.pid);
    const pool = new pg.Pool({ ...poolConfig(proxy.connectionString), application_name: applicationName, max: 1,
      connectionTimeoutMillis: mode === "bounded-retries" ? 150 : mode === "queued-acquisitions" ? 300 : 2000 });
    pool.on("error", () => {});
    let ending;
    const endPool = () => ending ||= pool.end();
    t.after(async () => { await proxy.close(); await endPool(); });
    proxy.stallConnections({ phase, durationMS: mode === "expire" ? 120 : 1500 });
    async function attempt() {
      try { return { client: await pool.connect() }; } catch (error) { return { error }; }
    }
    if (mode === "bounded-retries") {
      for (let attemptNumber = 0; attemptNumber < 3; attemptNumber++) {
        const result = await attempt(); assert.ok(result.error); assert.equal(result.client, undefined);
        assert.match(result.error.message, /timeout|terminated/i);
        await eventually(() => pool.totalCount === 0 && pool.waitingCount === 0 && proxy.evidence().pending_connections === 0 && proxy.evidence().pending_authentication === 0 && proxy.evidence().connections === 0);
      }
      assert.equal(proxy.evidence().connection_attempts, 3); assert.equal(proxy.evidence().connection_stalls_fired, 3);
      for (const record of proxy.evidence().connection_stalls) assert.equal(record.outcome, "canceled");
    } else if (mode === "queued-acquisitions") {
      const acquisitions = [attempt(), attempt(), attempt()];
      await eventually(() => proxy.evidence().active_connection_stalls === 1);
      assert.equal(pool.waitingCount, 2); assert.equal(pool.idleCount, 0);
      for (const result of await Promise.all(acquisitions)) { assert.ok(result.error); assert.equal(result.client, undefined); }
      await eventually(() => pool.totalCount === 0 && pool.waitingCount === 0 && proxy.evidence().pending_connections === 0 && proxy.evidence().pending_authentication === 0 && proxy.evidence().connections === 0);
      assert.ok(proxy.evidence().connection_attempts >= 1 && proxy.evidence().connection_attempts <= 3);
      assert.ok(proxy.evidence().connection_stalls_fired >= 1 && proxy.evidence().connection_stalls_fired <= 3);
    } else {
      const pending = attempt();
      await eventually(() => proxy.evidence().active_connection_stalls === 1);
      assert.equal(proxy.evidence().tls_connections_verified, phase === "authentication" ? proxy.evidence().connection_stalls_fired : 0); assert.equal(proxy.evidence().pending_connections, phase === "ssl_request" ? 1 : 0);
      assert.equal(proxy.evidence().pending_authentication, phase === "authentication" ? 1 : 0);
      const untouched = await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE application_name=$1", [applicationName]);
      assert.equal(untouched.rows[0].count, 0);
      if (mode === "release") proxy.clearConnectionStall();
      if (mode === "shutdown") await proxy.close();
      const result = await pending;
      if (mode === "release") {
        assert.equal(result.error, undefined);
        assert.equal((await result.client.query("SELECT 42 AS answer")).rows[0].answer, 42);
        result.client.release();
        assert.equal(proxy.evidence().connection_attempts, 1);
        assert.equal(proxy.evidence().connection_stalls[0].outcome, "released");
      } else {
        assert.ok(result.error); assert.equal(result.client, undefined);
        await eventually(() => pool.totalCount === 0 && proxy.evidence().pending_connections === 0 && proxy.evidence().pending_authentication === 0 && proxy.evidence().connections === 0);
        const record = proxy.evidence().connection_stalls[0];
        assert.equal(record.outcome, mode === "expire" ? "timed_out" : "canceled");
        if (mode === "expire") assert.ok(record.elapsed_ms >= 120);
      }
    }
    if (mode !== "release") {
      if (phase === "ssl_request") assert.equal(proxy.evidence().tls_connections_verified, 0);
      else assert.ok(proxy.evidence().tls_connections_verified >= 1 && proxy.evidence().tls_connections_verified <= proxy.evidence().connection_attempts);
    }
    for (const record of proxy.evidence().connection_stalls) assert.equal(record.phase, phase);
    assert.equal(proxy.evidence().pending_authentication, 0);
    assert.equal(pool.waitingCount, 0); assert.equal(proxy.evidence().active_connection_stalls, 0);
    assert.equal(proxy.evidence().connection_stall_timers, 0);
    if (mode !== "shutdown") {
      const beforeRecoveryTLS = proxy.evidence().tls_connections_verified;
      proxy.clearConnectionStall();
      const recovered = await pool.query("SELECT 42 AS answer");
      assert.equal(recovered.rows[0].answer, 42);
      assert.equal(pool.totalCount, 1); assert.equal(pool.idleCount, 1); assert.equal(pool.waitingCount, 0);
      const session = await admin.query("SELECT state,ssl,version FROM pg_stat_activity JOIN pg_stat_ssl USING(pid) WHERE application_name=$1", [applicationName]);
      assert.equal(session.rows.length, 1); assert.equal(session.rows[0].state, "idle");
      assert.equal(session.rows[0].ssl, true); assert.match(session.rows[0].version, /^TLSv1\.[23]$/);
      assert.equal(proxy.evidence().tls_connections_verified, beforeRecoveryTLS + (mode === "release" ? 0 : 1));
      assert.ok(proxy.evidence().connection_attempts <= 4); // Only caller-selected acquisitions; no retry loop in proxy.
    }
    await endPool(); await proxy.close();
    const after = proxy.evidence();
    assert.equal(after.connection_stall_enabled, false); assert.equal(after.active_connection_stalls, 0);
    assert.equal(after.connection_stall_timers, 0); assert.equal(after.pending_connections, 0); assert.equal(after.pending_authentication, 0); assert.equal(after.connections, 0);
    assert.equal(pool.totalCount, 0); assert.equal(pool.waitingCount, 0); assert.equal(pool.idleCount, 0);
    await eventually(async () => {
      const remaining = await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE application_name=$1", [applicationName]);
      const unretired = await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE backend_type='client backend' AND NOT (pid=ANY($1::int[]))", [baselinePIDs]);
      return remaining.rows[0].count === 0 && unretired.rows[0].count === 0;
    });
  });
}

test("released TLS authentication stall still rejects invalid credentials and retires the backend", { timeout: 10000 }, async t => {
  const proxy = await createCommitProxy(encryptedRuntime.href, { tls: tlsIdentity });
  const baseline = await admin.query("SELECT pid FROM pg_stat_activity WHERE backend_type='client backend'");
  const baselinePIDs = baseline.rows.map(row => row.pid);
  const invalid = new URL(proxy.connectionString); invalid.password = "fixture-invalid-password";
  const failed = new pg.Client(poolConfig(invalid.href));
  const healthy = new pg.Client(poolConfig(proxy.connectionString));
  for (const client of [failed, healthy]) client.on("error", () => {});
  t.after(async () => { await proxy.close(); await failed.end(); await healthy.end(); });
  proxy.stallConnections({ phase: "authentication", durationMS: 1500 });
  const connecting = failed.connect().then(() => ({ connected: true }), error => ({ error }));
  await eventually(() => proxy.evidence().active_connection_stalls === 1);
  assert.equal(proxy.evidence().tls_connections_verified, 1); assert.equal(proxy.evidence().pending_authentication, 1);
  proxy.clearConnectionStall();
  const rejected = await connecting;
  assert.equal(rejected.connected, undefined); assert.equal(rejected.error.code, "28P01");
  await failed.end();
  await eventually(() => proxy.evidence().connections === 0 && proxy.evidence().pending_authentication === 0);
  await healthy.connect(); assert.equal((await healthy.query("SELECT 42 AS answer")).rows[0].answer, 42);
  assert.equal(proxy.evidence().connection_stalls_fired, 1); assert.equal(proxy.evidence().connection_stalls[0].outcome, "released");
  await healthy.end(); await proxy.close();
  assert.equal(proxy.evidence().active_connection_stalls, 0); assert.equal(proxy.evidence().connection_stall_timers, 0);
  await eventually(async () => {
    const unretired = await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE backend_type='client backend' AND NOT (pid=ANY($1::int[]))", [baselinePIDs]);
    return unretired.rows[0].count === 0;
  });
});

test("native authentication fixture routes caller through loopback TLS proxy and verifies complete lifecycle", { timeout: 20000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const { createNativeAuthCaller } = await import("../native-auth-app/server.js");
  const { verifyNativeAuth } = await import("./native-auth-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-auth-proof-");
  const namespace = randomUUID().replaceAll("-", "");
  const token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server);
  const caller = createNativeAuthCaller({ dependencyURL: providerURL });
  const appURL = await listen(caller);
  t.after(async () => { await fixture.stop(); await close(caller); await close(fixture.server); await rm(directory, { recursive: true, force: true }); });
  const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/auth-proof.json" };
  assert.equal((await fetch(providerURL + "/native-auth-control", { method: "POST", body: JSON.stringify({ namespace, action: "arm" }) })).status, 401);
  assert.equal(fixture.status().connection_stall_enabled, false);
  const baseline = await fetch(appURL + "/query"); assert.equal(baseline.status, 200); assert.deepEqual(await baseline.json(), { ready: true, value: 42 });
  await verifyNativeAuth("all", options);
  const proof = await verifyNativeAuth("cleanup", options);
  assert.equal(proof.outage.responses.length, 3); assert.equal(proof.cleanup.status.stopped, true);
  assert.equal(proof.cleanup.status.pending_authentication, 0); assert.equal(proof.cleanup.status.connections, 0);
  const observer = spawnSync(process.execPath, [new URL("./native-auth-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.deepEqual(JSON.parse(observer.stdout), { namespace, passed: true, backend_sessions: 0, unretired_backends: 0 });
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_auth_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0);
  const text = await readFile(options.proofPath, "utf8");
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!text.includes(secret));
});

test("native COMMIT acknowledgement loss proves committed write, safe replay, conflict and independent cleanup", { timeout: 20000 }, async t => {
  const { createNativeCommitProvider } = await import("../payment-postgres/native-fixture/commit-provider.mjs");
  const { verifyNativeCommit } = await import("./native-commit-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-commit-proof-");
  const namespace = randomUUID().replaceAll("-", "");
  const token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeCommitProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server);
  const caller = createWriteCaller({ dependencyURL: providerURL }); const appURL = await listen(caller);
  t.after(async () => { await fixture.stop(); await close(caller); await close(fixture.server); await cleanupNamespace(runtimeURL, namespace); await rm(directory, { recursive: true, force: true }); });
  const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/commit-proof.json" };
  const control = body => fetch(providerURL + "/native-commit-control", { method: "POST", headers: { authorization: "Bearer " + token }, body: JSON.stringify(body) });
  assert.equal((await fetch(providerURL + "/native-commit-control", { method: "POST", body: JSON.stringify({ namespace, action: "arm" }) })).status, 401);
  assert.equal((await control({ namespace: randomUUID().replaceAll("-", ""), action: "arm" })).status, 400);
  assert.equal(fixture.status().armed, false);
  await verifyNativeCommit("all", options);
  const proof = await verifyNativeCommit("cleanup", options);
  assert.equal(proof.unknown.database.charges, 1); assert.equal(proof.replay.database.charges, 1); assert.equal(proof.cleanup.status.stopped, true);
  const observer = spawnSync(process.execPath, [new URL("./native-commit-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.deepEqual(JSON.parse(observer.stdout), { namespace, passed: true, remaining: 0, backend_sessions: 0, unretired_backends: 0 });
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_commit_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0, python.stderr);
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!(await readFile(options.proofPath, "utf8")).includes(secret));
  await fixture.stop(); // Shutdown must be idempotent after successful cleanup.
});

for (const direction of ["request", "response"]) {
  test(`native held COMMIT ${direction} exceeds caller deadline, cancels and safely replays`, { timeout: 20000 }, async t => {
    const { createNativeCommitProvider } = await import("../payment-postgres/native-fixture/commit-provider.mjs");
    const { verifyNativeCommit } = await import("./native-commit-acceptance.mjs");
    const { mkdtemp, rm } = await import("node:fs/promises");
    const directory = await mkdtemp("/tmp/gregale-native-commit-deadline-");
    const namespace = randomUUID().replaceAll("-", ""), faultMode = direction + "-deadline";
    const token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
    const fixture = await createNativeCommitProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
      controlToken: token, disposable: "1", faultMode, upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
    const providerURL = await listen(fixture.server);
    const caller = createWriteCaller({ dependencyURL: providerURL, deadlineMS: 1500 }); const appURL = await listen(caller);
    t.after(async () => { await fixture.stop(); await close(caller); await close(fixture.server); await cleanupNamespace(runtimeURL, namespace); await rm(directory, { recursive: true, force: true }); });
    const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/commit-proof.json", faultMode };
    await verifyNativeCommit("all", options);
    const proof = await verifyNativeCommit("cleanup", options);
    assert.equal(proof.unknown.database.charges, direction === "request" ? 0 : 1);
    assert.equal(proof.replay.database.charges, 1); assert.equal(proof.cleanup.status.fault_timers, 0);
    const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_commit_proof'](p,{'run_id':p['namespace']},p['fault_mode'])",
      new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
    assert.equal(python.status, 0, python.stderr);
    const observer = spawnSync(process.execPath, [new URL("./native-commit-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
      { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
    assert.equal(observer.status, 0); assert.equal(JSON.parse(observer.stdout).unretired_backends, 0);
  });
}

test("native bandwidth fixture completes large reads, rejects partial timeout, clears and retires resources", { timeout: 15000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const { createNativeAuthCaller } = await import("../native-auth-app/server.js");
  const { verifyNativeBandwidth } = await import("./native-bandwidth-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-bandwidth-proof-");
  const namespace = randomUUID().replaceAll("-", ""), token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", fixtureMode: "bandwidth", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server);
  const caller = createNativeAuthCaller({ dependencyURL: providerURL }), appURL = await listen(caller);
  t.after(async () => { await fixture.stop(); await close(caller); await close(fixture.server); await rm(directory, { recursive: true, force: true }); });
  assert.equal((await fetch(providerURL + "/native-bandwidth-control", { method: "POST", body: JSON.stringify({ namespace, action: "slow" }) })).status, 401);
  const baseline = await fetch(appURL + "/query"); assert.equal(baseline.status, 200); assert.equal((await baseline.json()).bytes, 65536);
  const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/bandwidth-proof.json" };
  await verifyNativeBandwidth("all", options); const proof = await verifyNativeBandwidth("cleanup", options);
  assert.equal(proof.cleanup.status.bandwidth_active_limits, 0); assert.equal(proof.cleanup.status.bandwidth_buffered_bytes, 0);
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_bandwidth_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0, python.stderr);
  const observer = spawnSync(process.execPath, [new URL("./native-bandwidth-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.equal(JSON.parse(observer.stdout).unretired_backends, 0);
  const text = await readFile(options.proofPath, "utf8");
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!text.includes(secret));
});

test("native read interruption rejects a partial row, recovers and isolates concurrent clients", { timeout: 15000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const { createNativeAuthCaller } = await import("../native-auth-app/server.js");
  const { verifyNativeReadCut } = await import("./native-read-cut-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-read-cut-proof-");
  const namespace = randomUUID().replaceAll("-", ""), token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", fixtureMode: "read-cut", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server);
  const caller = createNativeAuthCaller({ dependencyURL: providerURL }), appURL = await listen(caller);
  t.after(async () => { await fixture.stop(); await close(caller); await close(fixture.server); await rm(directory, { recursive: true, force: true }); });
  assert.equal((await fetch(providerURL + "/native-read-cut-control", { method: "POST", body: JSON.stringify({ namespace, action: "arm" }) })).status, 401);
  const baseline = await fetch(appURL + "/query"); assert.equal(baseline.status, 200); assert.equal((await baseline.json()).bytes, 65536);
  const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/read-cut-proof.json" };
  await verifyNativeReadCut("all", options); const proof = await verifyNativeReadCut("cleanup", options);
  assert.equal(proof.concurrent.status.concurrent_read_pairs, 1); assert.equal(proof.cleanup.status.read_pair_timers, 0);
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_read_cut_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0, python.stderr);
  const observer = spawnSync(process.execPath, [new URL("./native-read-cut-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.equal(JSON.parse(observer.stdout).unretired_backends, 0);
  const text = await readFile(options.proofPath, "utf8");
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!text.includes(secret));
});

test("native read rendezvous fails closed and joins a missing concurrent participant", { timeout: 10000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const namespace = randomUUID().replaceAll("-", ""), token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", fixtureMode: "read-cut", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server);
  t.after(async () => { await fixture.stop(); await close(fixture.server); });
  const response = await fetch(providerURL + "/native-read-cut-control", { method: "POST", headers: { authorization: "Bearer " + token }, body: JSON.stringify({ namespace, action: "arm-concurrent" }) });
  assert.equal(response.status, 200);
  const lone = await fetch(providerURL + "/query"); assert.equal(lone.status, 503);
  assert.deepEqual(await lone.json(), { error: "dependency_unavailable", retryable: true });
  await fixture.stop();
  assert.equal(fixture.status().concurrent_read_pairs, 0); assert.equal(fixture.status().read_cuts_fired, 0);
  for (const field of ["read_pair_waiters", "read_pair_timers", "pool_total", "active_queries", "connections"]) assert.equal(fixture.status()[field], 0);
});

test("native SSLRequest stall bounds retries, finishes queued acquisitions and restores verified TLS", { timeout: 25000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const { createNativeAuthCaller } = await import("../native-auth-app/server.js");
  const { verifyNativeSSLRequest } = await import("./native-ssl-request-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-ssl-request-proof-");
  const namespace = randomUUID().replaceAll("-", ""), token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", fixtureMode: "ssl-request", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server), caller = createNativeAuthCaller({ dependencyURL: providerURL }), appURL = await listen(caller);
  t.after(async () => { await fixture.stop(); await close(caller); await close(fixture.server); await rm(directory, { recursive: true, force: true }); });
  assert.equal((await fetch(providerURL + "/native-ssl-request-control", { method: "POST", body: JSON.stringify({ namespace, action: "arm" }) })).status, 401);
  const baseline = await fetch(appURL + "/query"); assert.equal(baseline.status, 200); assert.deepEqual(await baseline.json(), { ready: true, value: 42 });
  const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/ssl-request-proof.json" };
  await verifyNativeSSLRequest("all", options); const proof = await verifyNativeSSLRequest("cleanup", options);
  assert.equal(proof.queued.status.peak_pool_waiting, 2); assert.equal(proof.cleanup.status.connection_stall_timers, 0);
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_ssl_request_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0, python.stderr);
  const observer = spawnSync(process.execPath, [new URL("./native-ssl-request-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.equal(JSON.parse(observer.stdout).unretired_backends, 0);
  const text = await readFile(options.proofPath, "utf8");
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!text.includes(secret));
});

test("native shared retry budget caps six concurrent callers, cancels and recovers without amplification", { timeout: 20000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const { createNativeRetryCaller } = await import("../native-retry-app/server.js");
  const { verifyNativeRetryBudget } = await import("./native-retry-budget-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-retry-budget-proof-");
  const namespace = randomUUID().replaceAll("-", ""), token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", fixtureMode: "ssl-request", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server), caller = createNativeRetryCaller({ dependencyURL: providerURL, namespace, token }), appURL = await listen(caller.server);
  t.after(async () => { await caller.stop(); await fixture.stop(); await close(caller.server); await close(fixture.server); await rm(directory, { recursive: true, force: true }); });
  assert.equal((await fetch(appURL + "/retry-budget-control", { method: "POST" })).status, 401);
  const baseline = await fetch(appURL + "/query"); assert.equal(baseline.status, 200); assert.equal((await baseline.json()).attempts, 1);
  const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/retry-budget-proof.json" };
  await verifyNativeRetryBudget("all", options); const proof = await verifyNativeRetryBudget("cleanup", options);
  assert.equal(proof.outage.status.caller.retries_admitted, 2); assert.equal(proof.cleanup.status.caller.deadline_timers, 0);
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_retry_budget_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0, python.stderr);
  const observer = spawnSync(process.execPath, [new URL("./native-retry-budget-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.equal(JSON.parse(observer.stdout).unretired_backends, 0);
  const text = await readFile(options.proofPath, "utf8");
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!text.includes(secret));
});

test("native circuit breaker opens, limits half-open probes, reopens on failure/cancellation and recovers", { timeout: 30000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const { createNativeBreakerCaller } = await import("../native-breaker-app/server.js");
  const { verifyNativeCircuitBreaker } = await import("./native-circuit-breaker-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-circuit-breaker-proof-");
  const namespace = randomUUID().replaceAll("-", ""), token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", fixtureMode: "ssl-request", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server), caller = createNativeBreakerCaller({ dependencyURL: providerURL, namespace, token }), appURL = await listen(caller.server);
  t.after(async () => { await caller.stop(); await fixture.stop(); await close(caller.server); await close(fixture.server); await rm(directory, { recursive: true, force: true }); });
  assert.equal((await fetch(appURL + "/breaker-control", { method: "POST" })).status, 401);
  const baseline = await fetch(appURL + "/query"); assert.equal(baseline.status, 200); assert.equal((await baseline.json()).attempts, 1);
  const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/circuit-breaker-proof.json" };
  await verifyNativeCircuitBreaker("all", options); const proof = await verifyNativeCircuitBreaker("cleanup", options);
  assert.equal(proof.recovery.status.caller.state, "closed"); assert.equal(proof.cleanup.status.caller.deadline_timers, 0);
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_circuit_breaker_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0, python.stderr);
  const observer = spawnSync(process.execPath, [new URL("./native-circuit-breaker-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.equal(JSON.parse(observer.stdout).unretired_backends, 0);
  const text = await readFile(options.proofPath, "utf8");
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!text.includes(secret));
});

test("native bulkhead bounds dependency calls and queue deadlines while healthy traffic stays responsive", { timeout: 20000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const { createNativeBulkheadCaller } = await import("../native-bulkhead-app/server.js");
  const { verifyNativeBulkhead } = await import("./native-bulkhead-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-bulkhead-proof-");
  const namespace = randomUUID().replaceAll("-", ""), token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", fixtureMode: "ssl-request", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server), caller = createNativeBulkheadCaller({ dependencyURL: providerURL, namespace, token }), appURL = await listen(caller.server);
  t.after(async () => { await caller.stop(); await fixture.stop(); await close(caller.server); await close(fixture.server); await rm(directory, { recursive: true, force: true }); });
  assert.equal((await fetch(appURL + "/bulkhead-control", { method: "POST" })).status, 401);
  const baseline = await fetch(appURL + "/query"); assert.equal(baseline.status, 200); assert.equal((await baseline.json()).attempts, 1);
  const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/bulkhead-proof.json" };
  await verifyNativeBulkhead("all", options); const proof = await verifyNativeBulkhead("cleanup", options);
  assert.equal(proof.cleanup.status.caller.permits_granted, proof.cleanup.status.caller.permits_released);
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_bulkhead_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0, python.stderr);
  const observer = spawnSync(process.execPath, [new URL("./native-bulkhead-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.equal(JSON.parse(observer.stdout).unretired_backends, 0);
  const text = await readFile(options.proofPath, "utf8");
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!text.includes(secret));
});

test("native combined policies protect every retry and probe, isolate health and recover without leaks", { timeout: 35000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const { createNativeCombinedCaller } = await import("../native-bulkhead-app/combined.js");
  const { verifyNativeCombined } = await import("./native-combined-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-combined-proof-");
  const namespace = randomUUID().replaceAll("-", ""), token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", fixtureMode: "ssl-request", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server), caller = createNativeCombinedCaller({ dependencyURL: providerURL, namespace, token }), appURL = await listen(caller.server);
  t.after(async () => { await caller.stop(); await fixture.stop(); await close(caller.server); await close(fixture.server); await rm(directory, { recursive: true, force: true }); });
  assert.equal((await fetch(appURL + "/combined-control", { method: "POST" })).status, 401);
  const baseline = await fetch(appURL + "/query"); assert.equal(baseline.status, 200); assert.equal((await baseline.json()).attempts, 1);
  const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/combined-proof.json" };
  await verifyNativeCombined("all", options); const proof = await verifyNativeCombined("cleanup", options);
  assert.equal(proof.outage.status.caller.retries_admitted, 2); assert.equal(proof.cleanup.status.caller.permits_granted, proof.cleanup.status.caller.permits_released);
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_combined_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0, python.stderr);
  const observer = spawnSync(process.execPath, [new URL("./native-combined-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.equal(JSON.parse(observer.stdout).unretired_backends, 0);
  const text = await readFile(options.proofPath, "utf8");
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!text.includes(secret));
});

test("native repeated outages renew real retry windows and recover without resource accumulation", { timeout: 120000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const { createNativeCombinedCaller } = await import("../native-bulkhead-app/combined.js");
  const { verifyNativeRepeated } = await import("./native-repeated-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-repeated-proof-");
  const namespace = randomUUID().replaceAll("-", ""), token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixture = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
    controlToken: token, disposable: "1", fixtureMode: "ssl-request", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
  const providerURL = await listen(fixture.server), caller = createNativeCombinedCaller({ dependencyURL: providerURL, namespace, token }), appURL = await listen(caller.server);
  t.after(async () => { await caller.stop(); await fixture.stop(); await close(caller.server); await close(fixture.server); await rm(directory, { recursive: true, force: true }); });
  assert.equal((await fetch(appURL + "/combined-control", { method: "POST" })).status, 401);
  const baseline = await fetch(appURL + "/query"); assert.equal(baseline.status, 200); assert.equal((await baseline.json()).attempts, 1);
  const options = { appURL, providerURL, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/repeated-proof.json" };
  await verifyNativeRepeated("all", options); const proof = await verifyNativeRepeated("cleanup", options);
  assert.equal(proof.cycles.length, 3); assert.equal(proof.cleanup.status.caller.permits_granted, proof.cleanup.status.caller.permits_released);
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_repeated_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0, python.stderr);
  const observer = spawnSync(process.execPath, [new URL("./native-repeated-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.equal(JSON.parse(observer.stdout).unretired_backends, 0);
  const text = await readFile(options.proofPath, "utf8");
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!text.includes(secret));
});

test("native dependency isolation keeps either healthy route responsive through the other's outage", { timeout: 30000 }, async t => {
  const { createNativeAuthProvider } = await import("../payment-postgres/native-fixture/provider.mjs");
  const { createNativeIsolationCaller } = await import("../native-bulkhead-app/isolation.js");
  const { verifyNativeIsolation } = await import("./native-isolation-acceptance.mjs");
  const { mkdtemp, rm } = await import("node:fs/promises");
  const directory = await mkdtemp("/tmp/gregale-native-isolation-proof-");
  const namespace = randomUUID().replaceAll("-", ""), token = randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");
  const fixtures = {}, providerURLs = {};
  let caller;
  t.after(async () => {
    await caller?.stop();
    for (const fixture of Object.values(fixtures)) { await fixture.stop(); await close(fixture.server); }
    if (caller) await close(caller.server);
    await rm(directory, { recursive: true, force: true });
  });
  for (const name of ["a", "b"]) {
    fixtures[name] = await createNativeAuthProvider({ databaseURL: encryptedRuntime.href, namespace, cert: tlsIdentity.cert, key: tlsIdentity.key,
      controlToken: token, disposable: "1", fixtureMode: "ssl-request", upstreamCA: tlsIdentity.ca, upstreamServerName: "localhost" });
    providerURLs[name] = await listen(fixtures[name].server);
  }
  caller = createNativeIsolationCaller({ dependencyURLs: providerURLs, namespace, token });
  const appURL = await listen(caller.server);
  assert.equal((await fetch(appURL + "/isolation-control", { method: "POST" })).status, 401);
  for (const name of ["a", "b"]) {
    const response = await fetch(appURL + "/" + name + "/query"); assert.equal(response.status, 200); assert.equal((await response.json()).attempts, 1);
  }
  const options = { appURL, providerURLs, namespace, token, databaseURL: runtimeURL, proofPath: directory + "/isolation-proof.json" };
  await verifyNativeIsolation("all", options); const proof = await verifyNativeIsolation("cleanup", options);
  assert.equal(proof.phases.length, 2);
  const python = spawnSync("python3", ["-c", "import json,runpy,sys; m=runpy.run_path(sys.argv[1]); p=json.load(open(sys.argv[2])); m['verify_isolation_proof'](p,{'run_id':p['namespace']})",
    new URL("../../../scripts/ci/scenario-restart-native.py", import.meta.url).pathname, options.proofPath], { encoding: "utf8", timeout: 10000 });
  assert.equal(python.status, 0, python.stderr);
  const observer = spawnSync(process.execPath, [new URL("./native-isolation-acceptance.mjs", import.meta.url).pathname, "check", namespace, options.proofPath],
    { env: { ...process.env, GREGALE_DEPENDENCY_DATABASE_URL: runtimeURL }, encoding: "utf8", timeout: 10000 });
  assert.equal(observer.status, 0); assert.equal(JSON.parse(observer.stdout).unretired_backends, 0);
  const text = await readFile(options.proofPath, "utf8"); assert.ok(text.length <= 65536);
  for (const secret of [token, encryptedRuntime.href, tlsIdentity.key.toString()]) assert.ok(!text.includes(secret));
});
