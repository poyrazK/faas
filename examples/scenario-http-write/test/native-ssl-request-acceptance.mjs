import assert from "node:assert/strict";
import { readFile, writeFile, rename } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";
import pg from "../payment-postgres/client.js";
import { poolConfig, namespaceValue } from "../payment-postgres/database.js";
import { backendRetirement } from "./native-auth-acceptance.mjs";

export async function verifyNativeSSLRequest(stage, { appURL, providerURL, namespace, token, databaseURL, proofPath }) {
  namespaceValue(namespace);
  assert.ok(isAbsolute(proofPath)); assert.match(token, /^[0-9a-f]{64}$/);
  const request = async (url, options) => {
    const result = await fetch(url, { ...options, redirect: "error", signal: AbortSignal.timeout(8000) });
    const body = await result.text(); assert.ok(body.length <= 65536);
    return { status: result.status, body: JSON.parse(body) };
  };
  const control = async action => {
    const result = await request(providerURL + "/native-ssl-request-control", { method: "POST", headers: { authorization: "Bearer " + token, "content-type": "application/json" }, body: JSON.stringify({ namespace, action }) });
    assert.equal(result.status, 200); assert.equal(result.body.namespace, namespace); return result.body;
  };
  const status = async () => {
    const end = Date.now() + 5000;
    while (Date.now() < end) {
      const result = await request(providerURL + "/native-ssl-request-status"); assert.equal(result.status, 200);
      const value = result.body; assert.equal(value.namespace, namespace);
      if (!value.active_queries && !value.pool_total && !value.pool_waiting && !value.connections && !value.pending_connections && !value.pending_authentication && !value.active_connection_stalls) return value;
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error("native_ssl_request_work_not_joined");
  };
  let proof;
  try { const data = await readFile(proofPath, "utf8"); assert.ok(data.length <= 65536); proof = JSON.parse(data); }
  catch (error) { if (error.code !== "ENOENT") throw error; proof = { namespace }; }
  assert.equal(proof.namespace, namespace);
  const retirement = () => backendRetirement(databaseURL, namespace, proof.backend_baseline_pids, "gregale-native-ssl-request-");
  const save = async () => { await writeFile(proofPath + ".tmp", JSON.stringify(proof) + "\n", { mode: 0o600 }); await rename(proofPath + ".tmp", proofPath); };
  if (stage === "all") {
    proof.baseline = await status(); assert.equal(proof.baseline.attempts, 1); assert.equal(proof.baseline.tls_connections_verified, 1);
    assert.equal(proof.baseline.connection_stalls_fired, 0); proof.instance_id = proof.baseline.instance_id;
    const observer = new pg.Pool(poolConfig(databaseURL)); observer.on("error", () => {});
    try { proof.backend_baseline_pids = (await observer.query("SELECT pid FROM pg_stat_activity WHERE backend_type='client backend'")).rows.map(row => row.pid); }
    finally { await observer.end(); }
    await save(); await control("arm");
    for (const phase of ["retries", "queued"]) {
      const started = performance.now(), responses = [];
      if (phase === "retries") {
        for (let i = 0; i < 3; i++) responses.push(await request(appURL + "/query"));
      } else responses.push(...await Promise.all(Array.from({ length: 3 }, () => request(appURL + "/query"))));
      const elapsed = performance.now() - started;
      for (const response of responses) { assert.equal(response.status, 503); assert.deepEqual(response.body, { error: "dependency_unavailable", retryable: true }); }
      const after = await status(); assert.equal(after.instance_id, proof.instance_id);
      assert.equal(after.attempts, phase === "retries" ? 4 : 7); assert.equal(after.tls_connections_verified, 1);
      assert.equal(after.authentication_challenges_seen, 1); assert.equal(after.connection_stall_enabled, true);
      if (phase === "queued") assert.ok(after.peak_pool_waiting >= 2);
      proof[phase] = { responses, elapsed_ms: elapsed, status: after, database: await retirement() }; await save();
    }
    await control("clear");
    const responses = [];
    for (let i = 0; i < 2; i++) {
      const result = await request(appURL + "/query"); assert.equal(result.status, 200); assert.deepEqual(result.body, { ready: true, value: 42 }); responses.push(result);
    }
    const after = await status(); assert.equal(after.attempts, 9); assert.equal(after.tls_connections_verified, 3);
    assert.equal(after.connection_stall_enabled, false); assert.equal(after.instance_id, proof.instance_id);
    proof.recovery = { responses, status: after, database: await retirement() };
  } else if (stage === "cleanup") {
    await control("stop"); const after = await status(); assert.equal(after.stopped, true);
    proof.cleanup = { status: after, database: await retirement() };
  } else throw new Error("invalid_native_ssl_request_stage");
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
      console.log(JSON.stringify(await backendRetirement(process.env.GREGALE_DEPENDENCY_DATABASE_URL, process.argv[3], proof.backend_baseline_pids, "gregale-native-ssl-request-")));
    } else {
      await verifyNativeSSLRequest(process.argv[2], { appURL: process.env.GREGALE_TEST_URL, providerURL: process.env.GREGALE_TEST_SERVICE_PAYMENT_URL,
        namespace: process.env.GREGALE_TEST_RUN_ID, token: process.env.GREGALE_NATIVE_AUTH_CONTROL_TOKEN,
        databaseURL: process.env.GREGALE_DEPENDENCY_DATABASE_URL, proofPath: process.env.GREGALE_NATIVE_SSL_REQUEST_PROOF });
      console.log(JSON.stringify({ stage: process.argv[2], passed: true }));
    }
  } catch { console.error("Native SSLRequest acceptance failed"); process.exitCode = 1; }
}
