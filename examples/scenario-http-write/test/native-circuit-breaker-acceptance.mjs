import assert from "node:assert/strict";
import { readFile, writeFile, rename } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";
import pg from "../payment-postgres/client.js";
import { poolConfig, namespaceValue } from "../payment-postgres/database.js";
import { backendRetirement } from "./native-auth-acceptance.mjs";

export async function verifyNativeCircuitBreaker(stage, { appURL, providerURL, namespace, token, databaseURL, proofPath }) {
  namespaceValue(namespace); assert.ok(isAbsolute(proofPath)); assert.match(token, /^[0-9a-f]{64}$/);
  const request = async (url, options) => {
    const result = await fetch(url, { ...options, redirect: "error", signal: AbortSignal.timeout(10000) });
    const text = await result.text(); assert.ok(text.length <= 65536); return { status: result.status, body: JSON.parse(text) };
  };
  const control = async (target, action) => {
    const caller = target === appURL;
    const result = await request(target + (caller ? "/breaker-control" : "/native-ssl-request-control"), { method: "POST",
      headers: { authorization: "Bearer " + token, "x-test-namespace": namespace, "content-type": "application/json" }, body: JSON.stringify({ namespace, action }) });
    assert.equal(result.status, 200); assert.equal(result.body.namespace, namespace);
  };
  const callerStatus = async () => {
    const result = await request(appURL + "/breaker-status"); assert.equal(result.status, 200); assert.equal(result.body.namespace, namespace); return result.body;
  };
  const joined = async () => {
    const end = Date.now() + 7000;
    while (Date.now() < end) {
      const caller = await callerStatus(), result = await request(providerURL + "/native-ssl-request-status"); assert.equal(result.status, 200);
      const provider = result.body; assert.equal(provider.namespace, namespace);
      if (["active_requests", "active_probes", "deadline_timers"].every(key => caller[key] === 0) && ["active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers"].every(key => provider[key] === 0)) return { caller, provider };
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error("retry_work_not_joined");
  };
  let proof;
  try { const text = await readFile(proofPath, "utf8"); assert.ok(text.length <= 65536); proof = JSON.parse(text); }
  catch (error) { if (error.code !== "ENOENT") throw error; proof = { namespace }; }
  assert.equal(proof.namespace, namespace);
  const save = async () => { await writeFile(proofPath + ".tmp", JSON.stringify(proof) + "\n", { mode: 0o600 }); await rename(proofPath + ".tmp", proofPath); };
  const retirement = () => backendRetirement(databaseURL, namespace, proof.backend_baseline_pids, "gregale-native-ssl-request-");
  const blocked = { status: 503, body: { error: "circuit_open", retryable: true, attempts: 0 } };
  const failed = { status: 503, body: { error: "dependency_unavailable", retryable: true, attempts: 1 } };
  const waitCooldown = async () => {
    const end = Date.now() + 4500;
    while (Date.now() < end) {
      const value = await callerStatus(); assert.equal(value.state, "open");
      if (value.cooldown_remaining_ms === 0) return;
      await new Promise(resolve => setTimeout(resolve, Math.min(50, value.cooldown_remaining_ms)));
    }
    throw new Error("breaker_cooldown_unbounded");
  };
  const observeProbe = async () => {
    const end = Date.now() + 1000;
    while (Date.now() < end) {
      const value = await callerStatus();
      if (value.state === "half-open" && value.active_probes === 1) return value;
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    throw new Error("breaker_probe_not_observed");
  };
  if (stage === "all") {
    proof.baseline = await joined(); assert.equal(proof.baseline.caller.dependency_attempts, 1); assert.equal(proof.baseline.provider.attempts, 1);
    proof.caller_id = proof.baseline.caller.instance_id; proof.provider_id = proof.baseline.provider.instance_id;
    const observer = new pg.Pool(poolConfig(databaseURL)); observer.on("error", () => {});
    try { proof.backend_baseline_pids = (await observer.query("SELECT pid FROM pg_stat_activity WHERE backend_type='client backend'")).rows.map(row => row.pid); }
    finally { await observer.end(); }
    await save(); await control(providerURL, "arm");
    const failures = [];
    for (let i = 0; i < 2; i++) { const result = await request(appURL + "/query"); assert.deepEqual(result, failed); failures.push(result); await joined(); }
    proof.trip = { responses: failures, status: await joined(), database: await retirement() }; await save();
    const started = performance.now(), rejected = await Promise.all(Array.from({ length: 6 }, () => request(appURL + "/query")));
    assert.deepEqual(rejected, Array.from({ length: 6 }, () => blocked));
    const elapsed = performance.now() - started; assert.ok(elapsed < 1200);
    proof.open = { responses: rejected, elapsed_ms: elapsed, status: await joined(), database: await retirement() }; await save();
    await waitCooldown();
    const probe = request(appURL + "/query");
    const observed = await observeProbe();
    const concurrent = await Promise.all(Array.from({ length: 4 }, () => request(appURL + "/query")));
    assert.deepEqual(concurrent, Array.from({ length: 4 }, () => blocked)); assert.deepEqual(await probe, failed);
    proof.failed_probe = { probe: failed, responses: concurrent, observed, status: await joined(), database: await retirement() }; await save();
    await waitCooldown();
    const controller = new AbortController();
    const canceled = fetch(appURL + "/query", { signal: controller.signal, redirect: "error" }).then(result => ({ status: result.status }), error => ({ aborted: error.name === "AbortError" }));
    let cancelObserved;
    try { cancelObserved = await observeProbe(); } finally { controller.abort(); }
    assert.deepEqual(await canceled, { aborted: true });
    proof.cancellation = { joined: true, client_aborted: true, observed: cancelObserved, status: await joined(), database: await retirement() }; await save();
    await control(providerURL, "clear"); await waitCooldown();
    const responses = [];
    for (let i = 0; i < 3; i++) { const response = await request(appURL + "/query"); assert.deepEqual(response, { status: 200, body: { ready: true, value: 42, attempts: 1 } }); responses.push(response); }
    proof.recovery = { responses, status: await joined(), database: await retirement() };
  } else if (stage === "cleanup") {
    await control(appURL, "stop"); await control(providerURL, "stop");
    proof.cleanup = { status: await joined(), database: await retirement() };
  } else throw new Error("invalid_circuit_breaker_stage");
  await save(); return proof;
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.env.GREGALE_DEPENDENCY_DATABASE_DISPOSABLE !== "1") throw new Error("disposable_database_required");
    if (process.argv[2] === "check") {
      const text = await readFile(process.argv[4], "utf8"); assert.ok(text.length <= 65536); const proof = JSON.parse(text); assert.equal(proof.namespace, process.argv[3]);
      console.log(JSON.stringify(await backendRetirement(process.env.GREGALE_DEPENDENCY_DATABASE_URL, process.argv[3], proof.backend_baseline_pids, "gregale-native-ssl-request-")));
    } else {
      await verifyNativeCircuitBreaker(process.argv[2], { appURL: process.env.GREGALE_TEST_URL, providerURL: process.env.GREGALE_TEST_SERVICE_PAYMENT_URL,
        namespace: process.env.GREGALE_TEST_RUN_ID, token: process.env.GREGALE_NATIVE_AUTH_CONTROL_TOKEN,
        databaseURL: process.env.GREGALE_DEPENDENCY_DATABASE_URL, proofPath: process.env.GREGALE_NATIVE_CIRCUIT_BREAKER_PROOF });
      console.log(JSON.stringify({ stage: process.argv[2], passed: true }));
    }
  } catch { console.error("Native circuit breaker acceptance failed"); process.exitCode = 1; }
}
