import assert from "node:assert/strict";
import { readFile, writeFile, rename } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";
import pg from "../payment-postgres/client.js";
import { poolConfig, namespaceValue } from "../payment-postgres/database.js";
import { backendRetirement } from "./native-auth-acceptance.mjs";

export async function verifyNativeCombined(stage, { appURL, providerURL, namespace, token, databaseURL, proofPath }) {
  namespaceValue(namespace); assert.ok(isAbsolute(proofPath)); assert.match(token, /^[0-9a-f]{64}$/);
  const request = async (url, options) => {
    const result = await fetch(url, { ...options, redirect: "error", signal: AbortSignal.timeout(10000) });
    const text = await result.text(); assert.ok(text.length <= 65536); return { status: result.status, body: JSON.parse(text) };
  };
  const control = async (target, action) => {
    const caller = target === appURL;
    const result = await request(target + (caller ? "/combined-control" : "/native-ssl-request-control"), { method: "POST",
      headers: { authorization: "Bearer " + token, "x-test-namespace": namespace, "content-type": "application/json" }, body: JSON.stringify({ namespace, action }) });
    assert.equal(result.status, 200); assert.equal(result.body.namespace, namespace);
  };
  const callerStatus = async () => {
    const result = await request(appURL + "/combined-status"); assert.equal(result.status, 200); assert.equal(result.body.namespace, namespace); return result.body;
  };
  const joined = async () => {
    const end = Date.now() + 7000;
    while (Date.now() < end) {
      const caller = await callerStatus(), result = await request(providerURL + "/native-ssl-request-status"); assert.equal(result.status, 200);
      const provider = result.body; assert.equal(provider.namespace, namespace);
      if (["active_requests", "active_probes", "active_dependencies", "queued_requests", "queue_timers", "retry_timers", "deadline_timers"].every(key => caller[key] === 0) && ["active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers"].every(key => provider[key] === 0)) return { caller, provider };
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
  const observeCapacity = async predicate => {
    const end = Date.now() + 800;
    while (Date.now() < end) {
      const value = await callerStatus(); if (predicate(value)) return value;
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    throw new Error("combined_capacity_not_observed");
  };
  if (stage === "all") {
    proof.baseline = await joined(); assert.equal(proof.baseline.caller.dependency_attempts, 1); assert.equal(proof.baseline.provider.attempts, 1);
    proof.caller_id = proof.baseline.caller.instance_id; proof.provider_id = proof.baseline.provider.instance_id;
    const observer = new pg.Pool(poolConfig(databaseURL)); observer.on("error", () => {});
    try { proof.backend_baseline_pids = (await observer.query("SELECT pid FROM pg_stat_activity WHERE backend_type='client backend'")).rows.map(row => row.pid); }
    finally { await observer.end(); }
    await save(); await control(providerURL, "arm");
    const controllers = Array.from({ length: 3 }, () => new AbortController());
    const startCanceled = index => fetch(appURL + "/query", { signal: controllers[index].signal, redirect: "error" }).then(result => ({ status: result.status }), error => ({ aborted: error.name === "AbortError" }));
    const cancelRequests = [startCanceled(0), startCanceled(1)];
    await observeCapacity(value => value.active_dependencies === 2);
    cancelRequests.push(startCanceled(2));
    let cancelObserved;
    try { cancelObserved = await observeCapacity(value => value.queued_requests === 1); } finally { for (const controller of controllers) controller.abort(); }
    assert.deepEqual(await Promise.all(cancelRequests), [{ aborted: true }, { aborted: true }, { aborted: true }]);
    proof.initial_cancellation = { joined: true, client_aborted: true, observed: cancelObserved, status: await joined(), database: await retirement() }; await save();
    const wave = Promise.all(Array.from({ length: 6 }, async () => { const started = performance.now(), response = await request(appURL + "/query"); return { ...response, elapsed_ms: performance.now() - started }; }));
    const observed = await observeCapacity(value => value.active_dependencies === 2 && value.queued_requests === 2);
    const health = await Promise.all(Array.from({ length: 3 }, async () => { const started = performance.now(), response = await request(appURL + "/health"); return { ...response, elapsed_ms: performance.now() - started }; }));
    for (const value of health) { assert.equal(value.status, 200); assert.deepEqual(value.body, { ready: true, active_dependencies: 2 }); assert.ok(value.elapsed_ms < 750); }
    const responses = await wave;
    assert.deepEqual(responses.map(value => value.body.error).sort(), ["bulkhead_rejected", "bulkhead_rejected", "dependency_unavailable", "dependency_unavailable", "queue_timeout", "queue_timeout"]);
    assert.equal(responses.reduce((sum, value) => sum + value.body.attempts, 0), 4);
    proof.outage = { responses, observed, health, status: await joined(), database: await retirement() }; await save();
    const started = performance.now(), rejected = await Promise.all(Array.from({ length: 6 }, () => request(appURL + "/query")));
    assert.deepEqual(rejected, Array.from({ length: 6 }, () => blocked)); const elapsed = performance.now() - started; assert.ok(elapsed < 1200);
    proof.open = { responses: rejected, elapsed_ms: elapsed, status: await joined(), database: await retirement() }; await save();
    await waitCooldown();
    const probe = request(appURL + "/query"), probeObserved = await observeProbe();
    const concurrent = await Promise.all(Array.from({ length: 4 }, () => request(appURL + "/query")));
    assert.deepEqual(concurrent, Array.from({ length: 4 }, () => blocked)); assert.deepEqual(await probe, failed);
    proof.failed_probe = { probe: failed, responses: concurrent, observed: probeObserved, status: await joined(), database: await retirement() }; await save();
    await waitCooldown();
    const controller = new AbortController();
    const canceled = fetch(appURL + "/query", { signal: controller.signal, redirect: "error" }).then(result => ({ status: result.status }), error => ({ aborted: error.name === "AbortError" }));
    let canceledProbe;
    try { canceledProbe = await observeProbe(); } finally { controller.abort(); }
    assert.deepEqual(await canceled, { aborted: true });
    proof.cancellation = { joined: true, client_aborted: true, observed: canceledProbe, status: await joined(), database: await retirement() }; await save();
    await control(providerURL, "clear"); await waitCooldown();
    const recovered = [];
    for (let i = 0; i < 3; i++) { const response = await request(appURL + "/query"); assert.deepEqual(response, { status: 200, body: { ready: true, value: 42, attempts: 1 } }); recovered.push(response); }
    proof.recovery = { responses: recovered, status: await joined(), database: await retirement() };
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
      await verifyNativeCombined(process.argv[2], { appURL: process.env.GREGALE_TEST_URL, providerURL: process.env.GREGALE_TEST_SERVICE_PAYMENT_URL,
        namespace: process.env.GREGALE_TEST_RUN_ID, token: process.env.GREGALE_NATIVE_AUTH_CONTROL_TOKEN,
        databaseURL: process.env.GREGALE_DEPENDENCY_DATABASE_URL, proofPath: process.env.GREGALE_NATIVE_COMBINED_PROOF });
      console.log(JSON.stringify({ stage: process.argv[2], passed: true }));
    }
  } catch { console.error("Native combined resilience acceptance failed"); process.exitCode = 1; }
}
