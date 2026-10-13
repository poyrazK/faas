import assert from "node:assert/strict";
import { readFile, writeFile, rename } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";
import pg from "../payment-postgres/client.js";
import { poolConfig, namespaceValue } from "../payment-postgres/database.js";
import { backendRetirement } from "./native-auth-acceptance.mjs";

export async function verifyNativeIsolation(stage, { appURL, providerURLs, namespace, token, databaseURL, proofPath }) {
  namespaceValue(namespace); assert.ok(isAbsolute(proofPath)); assert.match(token, /^[0-9a-f]{64}$/);
  assert.ok(providerURLs.a && providerURLs.b && providerURLs.a !== providerURLs.b);
  const request = async (url, options) => {
    const result = await fetch(url, { ...options, redirect: "error", signal: AbortSignal.timeout(10000) });
    const text = await result.text(); assert.ok(text.length <= 65536);
    return { status: result.status, body: JSON.parse(text) };
  };
  const control = async (target, action) => {
    const result = await request(target + (target === appURL ? "/isolation-control" : "/native-ssl-request-control"), { method: "POST",
      headers: { authorization: "Bearer " + token, "x-test-namespace": namespace, "content-type": "application/json" }, body: JSON.stringify({ namespace, action }) });
    assert.equal(result.status, 200); assert.equal(result.body.namespace, namespace);
  };
  const callerStatus = async () => (await request(appURL + "/isolation-status")).body;
  const joined = async () => {
    const end = Date.now() + 7000;
    while (Date.now() < end) {
      const caller = await callerStatus(), providers = {};
      for (const name of ["a", "b"]) providers[name] = (await request(providerURLs[name] + "/native-ssl-request-status")).body;
      if (["a", "b"].every(name => ["active_requests", "active_probes", "active_dependencies", "queued_requests", "queue_timers", "retry_timers", "deadline_timers"].every(key => caller.dependencies[name][key] === 0)
        && ["active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers"].every(key => providers[name][key] === 0))) return { caller, providers };
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error("isolation_work_not_joined");
  };
  let proof;
  try { const text = await readFile(proofPath, "utf8"); assert.ok(text.length <= 65536); proof = JSON.parse(text); }
  catch (error) { if (error.code !== "ENOENT") throw error; proof = { namespace }; }
  assert.equal(proof.namespace, namespace);
  const save = async () => { await writeFile(proofPath + ".tmp", JSON.stringify(proof) + "\n", { mode: 0o600 }); await rename(proofPath + ".tmp", proofPath); };
  const retirement = () => backendRetirement(databaseURL, namespace, proof.backend_baseline_pids, "gregale-native-ssl-request-");
  const complete = { status: 200, body: { ready: true, value: 42, attempts: 1 } };
  const healthyReads = async (name, count) => {
    const values = [];
    for (let index = 0; index < count; index++) {
      const started = performance.now(), response = await request(appURL + "/" + name + "/query");
      assert.deepEqual(response, complete);
      const elapsed_ms = performance.now() - started; assert.ok(elapsed_ms < 750);
      values.push({ ...response, elapsed_ms });
    }
    return values;
  };
  if (stage === "all") {
    proof.baseline = await joined(); proof.caller_id = proof.baseline.caller.instance_id;
    proof.provider_ids = Object.fromEntries(["a", "b"].map(name => [name, proof.baseline.providers[name].instance_id]));
    proof.dependency_ids = Object.fromEntries(["a", "b"].map(name => [name, proof.baseline.caller.dependencies[name].instance_id]));
    assert.notEqual(proof.provider_ids.a, proof.provider_ids.b); assert.notEqual(proof.dependency_ids.a, proof.dependency_ids.b);
    for (const name of ["a", "b"]) { assert.equal(proof.baseline.caller.dependencies[name].dependency_attempts, 1); assert.equal(proof.baseline.providers[name].attempts, 1); }
    const observer = new pg.Pool(poolConfig(databaseURL)); observer.on("error", () => {});
    try { proof.backend_baseline_pids = (await observer.query("SELECT pid FROM pg_stat_activity WHERE backend_type='client backend'")).rows.map(row => row.pid); }
    finally { await observer.end(); }
    proof.phases = []; await save();
    for (const faulted of ["a", "b"]) {
      const healthy = faulted === "a" ? "b" : "a", block = { faulted, healthy };
      proof.phases.push(block); await save(); await control(providerURLs[faulted], "arm");
      const wave = Promise.all(Array.from({ length: 6 }, async () => {
        const started = performance.now(), response = await request(appURL + "/" + faulted + "/query");
        return { ...response, elapsed_ms: performance.now() - started };
      }));
      const end = Date.now() + 800;
      do {
        block.observed = await callerStatus();
        if (block.observed.dependencies[faulted].active_dependencies === 2 && block.observed.dependencies[faulted].queued_requests === 2) break;
        await new Promise(resolve => setTimeout(resolve, 10));
      } while (Date.now() < end);
      assert.equal(block.observed.dependencies[faulted].active_dependencies, 2); assert.equal(block.observed.dependencies[faulted].queued_requests, 2);
      const healthyResponses = await healthyReads(healthy, 6);
      block.after_healthy = await callerStatus(); assert.equal(block.after_healthy.dependencies[faulted].active_dependencies, 2);
      const health = await Promise.all(Array.from({ length: 3 }, async () => {
        const started = performance.now(), response = await request(appURL + "/health");
        assert.deepEqual(response, { status: 200, body: { ready: true, active_dependencies: { [faulted]: 2, [healthy]: 0 } } });
        const elapsed_ms = performance.now() - started; assert.ok(elapsed_ms < 750); return { ...response, elapsed_ms };
      }));
      const responses = await wave;
      assert.deepEqual(responses.map(value => value.body.error).sort(), ["bulkhead_rejected", "bulkhead_rejected", "dependency_unavailable", "dependency_unavailable", "queue_timeout", "queue_timeout"]);
      block.outage = { responses, healthy_responses: healthyResponses, health, status: await joined(), database: await retirement() }; await save();
      const started = performance.now(), rejected = await Promise.all(Array.from({ length: 6 }, () => request(appURL + "/" + faulted + "/query")));
      assert.deepEqual(rejected, Array.from({ length: 6 }, () => ({ status: 503, body: { error: "circuit_open", retryable: true, attempts: 0 } })));
      const elapsed_ms = performance.now() - started; assert.ok(elapsed_ms < 1200);
      block.open = { responses: rejected, elapsed_ms, healthy_responses: await healthyReads(healthy, 3), status: await joined(), database: await retirement() }; await save();
      await control(providerURLs[faulted], "clear");
      const cooldownEnd = Date.now() + 4500;
      while (Date.now() < cooldownEnd && (await callerStatus()).dependencies[faulted].cooldown_remaining_ms > 0) await new Promise(resolve => setTimeout(resolve, 25));
      assert.equal((await callerStatus()).dependencies[faulted].cooldown_remaining_ms, 0);
      const recovered = [];
      for (let index = 0; index < 3; index++) { const response = await request(appURL + "/" + faulted + "/query"); assert.deepEqual(response, complete); recovered.push(response); }
      block.recovery = { responses: recovered, status: await joined(), database: await retirement() }; await save();
      console.log(JSON.stringify({ faulted, healthy, joined: true }));
    }
  } else if (stage === "cleanup") {
    await control(appURL, "stop");
    await Promise.all(Object.values(providerURLs).map(target => control(target, "stop")));
    proof.cleanup = { status: await joined(), database: await retirement() };
  } else throw new Error("invalid_isolation_stage");
  await save(); return proof;
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.env.GREGALE_DEPENDENCY_DATABASE_DISPOSABLE !== "1") throw new Error("disposable_database_required");
    if (process.argv[2] === "check") {
      const text = await readFile(process.argv[4], "utf8"); assert.ok(text.length <= 65536); const proof = JSON.parse(text); assert.equal(proof.namespace, process.argv[3]);
      console.log(JSON.stringify(await backendRetirement(process.env.GREGALE_DEPENDENCY_DATABASE_URL, process.argv[3], proof.backend_baseline_pids, "gregale-native-ssl-request-")));
    } else {
      await verifyNativeIsolation(process.argv[2], { appURL: process.env.GREGALE_TEST_URL,
        providerURLs: { a: process.env.GREGALE_TEST_SERVICE_PAYMENT_URL, b: process.env.GREGALE_TEST_SERVICE_INVENTORY_URL },
        namespace: process.env.GREGALE_TEST_RUN_ID, token: process.env.GREGALE_NATIVE_AUTH_CONTROL_TOKEN,
        databaseURL: process.env.GREGALE_DEPENDENCY_DATABASE_URL, proofPath: process.env.GREGALE_NATIVE_ISOLATION_PROOF });
      console.log(JSON.stringify({ stage: process.argv[2], passed: true }));
    }
  } catch { console.error("Native dependency isolation acceptance failed"); process.exitCode = 1; }
}
