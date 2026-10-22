import assert from "node:assert/strict";
import { readFile, writeFile, rename } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";
import pg from "../payment-postgres/client.js";
import { poolConfig, namespaceValue } from "../payment-postgres/database.js";

export async function backendRetirement(databaseURL, namespace, baselinePIDs, applicationPrefix = "gregale-native-auth-") {
  namespaceValue(namespace);
  assert.ok(Array.isArray(baselinePIDs) && baselinePIDs.length <= 128 && baselinePIDs.every(pid => Number.isInteger(pid) && pid > 0));
  const pool = new pg.Pool(poolConfig(databaseURL)); pool.on("error", () => {});
  try {
    const end = Date.now() + 5000;
    while (Date.now() < end) {
      const value = await pool.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE application_name=$1", [applicationPrefix + namespace]);
      const unretired = await pool.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE backend_type='client backend' AND pid<>pg_backend_pid() AND NOT (pid=ANY($1::int[]))", [baselinePIDs]);
      if (value.rows[0].count === 0 && unretired.rows[0].count === 0) return { namespace, passed: true, backend_sessions: 0, unretired_backends: 0 };
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error("native_auth_backends_remain");
  } finally { await pool.end(); }
}

export async function verifyNativeAuth(stage, { appURL, providerURL, namespace, token, databaseURL, proofPath }) {
  if (stage === "all") {
    await verifyNativeAuth("outage", { appURL, providerURL, namespace, token, databaseURL, proofPath });
    return verifyNativeAuth("recovery", { appURL, providerURL, namespace, token, databaseURL, proofPath });
  }
  namespaceValue(namespace);
  assert.ok(isAbsolute(proofPath)); assert.match(token, /^[0-9a-f]{64}$/);
  const request = async (url, options) => {
    const result = await fetch(url, { ...options, redirect: "error", signal: AbortSignal.timeout(8000) });
    const body = await result.text(); assert.ok(body.length <= 65536);
    return { status: result.status, body: JSON.parse(body) };
  };
  const control = async action => {
    const result = await request(providerURL + "/native-auth-control", { method: "POST", headers: { authorization: "Bearer " + token, "content-type": "application/json" }, body: JSON.stringify({ namespace, action }) });
    assert.equal(result.status, 200); assert.equal(result.body.namespace, namespace); return result.body;
  };
  const status = async () => {
    const end = Date.now() + 5000;
    while (Date.now() < end) {
      const result = await request(providerURL + "/native-auth-status"); assert.equal(result.status, 200);
      const value = result.body; assert.equal(value.namespace, namespace);
      if (!value.active_queries && !value.pool_total && !value.pool_waiting && !value.connections && !value.pending_connections && !value.pending_authentication && !value.active_connection_stalls) return value;
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error("native_auth_work_not_joined");
  };
  let proof;
  try { const data = await readFile(proofPath, "utf8"); assert.ok(data.length <= 65536); proof = JSON.parse(data); }
  catch (error) { if (error.code !== "ENOENT") throw error; proof = { namespace }; }
  assert.equal(proof.namespace, namespace);
  if (stage === "outage") {
    const observer = new pg.Pool(poolConfig(databaseURL)); observer.on("error", () => {});
    try {
      const before = await observer.query("SELECT pid FROM pg_stat_activity WHERE backend_type='client backend'");
      proof.backend_baseline_pids = before.rows.map(row => row.pid); assert.ok(proof.backend_baseline_pids.length <= 128);
    } finally { await observer.end(); }
    proof.baseline = await status(); assert.equal(proof.baseline.attempts, 1); assert.equal(proof.baseline.tls_connections_verified, 1);
    assert.equal(proof.baseline.authentication_challenges_seen, 1); assert.equal(proof.baseline.connection_stalls_fired, 0);
    proof.instance_id = proof.baseline.instance_id;
    await control("arm");
    const responses = [];
    for (let i = 0; i < 3; i++) {
      const result = await request(appURL + "/query");
      assert.equal(result.status, 503); assert.deepEqual(result.body, { error: "dependency_unavailable", retryable: true }); responses.push(result.status);
    }
    const after = await status(); assert.equal(after.instance_id, proof.instance_id);
    assert.equal(after.attempts, 4); assert.equal(after.tls_connections_verified, 4); assert.equal(after.authentication_challenges_seen, 4);
    assert.equal(after.connection_stalls_fired, 3); assert.equal(after.connection_stall_enabled, true);
    for (const record of after.connection_stalls) {
      assert.equal(record.phase, "authentication"); assert.equal(record.outcome, "canceled");
      assert.equal(record.configured_ms, 5000); assert.ok(record.elapsed_ms >= 1000 && record.elapsed_ms < 5000);
    }
    proof.outage = { responses, status: after, database: await backendRetirement(databaseURL, namespace, proof.backend_baseline_pids) };
  } else if (stage === "recovery") {
    assert.ok(proof.outage); await control("clear");
    for (let i = 0; i < 2; i++) {
      const result = await request(appURL + "/query"); assert.equal(result.status, 200); assert.deepEqual(result.body, { ready: true, value: 42 });
    }
    const after = await status(); assert.equal(after.instance_id, proof.instance_id);
    assert.equal(after.attempts, 6); assert.equal(after.tls_connections_verified, 6); assert.equal(after.connection_stalls_fired, 3);
    assert.equal(after.connection_stall_enabled, false); assert.equal(after.connection_stall_timers, 0);
    proof.recovery = { responses: [200, 200], value: 42, status: after, database: await backendRetirement(databaseURL, namespace, proof.backend_baseline_pids) };
  } else if (stage === "cleanup") {
    await control("stop"); const after = await status(); assert.equal(after.stopped, true);
    assert.equal(after.connection_stall_timers, 0); proof.cleanup = { status: after, database: await backendRetirement(databaseURL, namespace, proof.backend_baseline_pids) };
  } else throw new Error("invalid_native_auth_stage");
  const temporary = proofPath + ".tmp";
  await writeFile(temporary, JSON.stringify(proof) + "\n", { mode: 0o600 }); await rename(temporary, proofPath);
  return proof;
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.env.GREGALE_DEPENDENCY_DATABASE_DISPOSABLE !== "1") throw new Error("disposable_database_required");
    if (process.argv[2] === "check") {
      const text = await readFile(process.argv[4], "utf8"); assert.ok(text.length <= 65536);
      const proof = JSON.parse(text); assert.equal(proof.namespace, process.argv[3]);
      console.log(JSON.stringify(await backendRetirement(process.env.GREGALE_DEPENDENCY_DATABASE_URL, process.argv[3], proof.backend_baseline_pids)));
    } else {
      await verifyNativeAuth(process.argv[2], { appURL: process.env.GREGALE_TEST_URL, providerURL: process.env.GREGALE_TEST_SERVICE_PAYMENT_URL,
        namespace: process.env.GREGALE_TEST_RUN_ID, token: process.env.GREGALE_NATIVE_AUTH_CONTROL_TOKEN,
        databaseURL: process.env.GREGALE_DEPENDENCY_DATABASE_URL, proofPath: process.env.GREGALE_NATIVE_AUTH_PROOF });
      console.log(JSON.stringify({ stage: process.argv[2], passed: true }));
    }
  } catch { console.error("Native TLS authentication acceptance failed"); process.exitCode = 1; }
}
