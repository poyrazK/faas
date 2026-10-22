import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile, writeFile, rename } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";
import pg from "../payment-postgres/client.js";
import { poolConfig, namespaceValue } from "../payment-postgres/database.js";
import { backendRetirement } from "./native-auth-acceptance.mjs";

const complete = { ready: true, bytes: 65536, sha256: createHash("sha256").update("x".repeat(65536)).digest("hex") };
export async function verifyNativeReadCut(stage, { appURL, providerURL, namespace, token, databaseURL, proofPath }) {
  namespaceValue(namespace); assert.ok(isAbsolute(proofPath)); assert.match(token, /^[0-9a-f]{64}$/);
  const request = async (url, options) => {
    const result = await fetch(url, { ...options, redirect: "error", signal: AbortSignal.timeout(8000) });
    const text = await result.text(); assert.ok(text.length <= 65536); return { status: result.status, body: JSON.parse(text) };
  };
  const control = async action => {
    const result = await request(providerURL + "/native-read-cut-control", { method: "POST", headers: { authorization: "Bearer " + token, "content-type": "application/json" }, body: JSON.stringify({ namespace, action }) });
    assert.equal(result.status, 200); assert.equal(result.body.namespace, namespace);
  };
  const status = async () => {
    const end = Date.now() + 5000;
    while (Date.now() < end) {
      const result = await request(providerURL + "/native-read-cut-status"); assert.equal(result.status, 200);
      const value = result.body; assert.equal(value.namespace, namespace); assert.equal(value.fixture_mode, "read-cut");
      if (["active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "bandwidth_pending_transfers", "bandwidth_buffered_bytes", "bandwidth_timers", "active_read_cuts", "read_pair_waiters", "read_pair_timers"].every(key => value[key] === 0)) return value;
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error("native_read_cut_work_not_joined");
  };
  let proof;
  try { const text = await readFile(proofPath, "utf8"); assert.ok(text.length <= 65536); proof = JSON.parse(text); }
  catch (error) { if (error.code !== "ENOENT") throw error; proof = { namespace }; }
  assert.equal(proof.namespace, namespace);
  const save = async () => { await writeFile(proofPath + ".tmp", JSON.stringify(proof) + "\n", { mode: 0o600 }); await rename(proofPath + ".tmp", proofPath); };
  const retirement = () => backendRetirement(databaseURL, namespace, proof.backend_baseline_pids, "gregale-native-read-cut-");
  if (stage === "all") {
    proof.baseline = await status(); assert.equal(proof.baseline.attempts, 1); assert.equal(proof.baseline.tls_connections_verified, 1);
    assert.equal(proof.baseline.read_cuts.length, 0); proof.instance_id = proof.baseline.instance_id;
    const observer = new pg.Pool(poolConfig(databaseURL)); observer.on("error", () => {});
    try { proof.backend_baseline_pids = (await observer.query("SELECT pid FROM pg_stat_activity WHERE backend_type='client backend'")).rows.map(row => row.pid); }
    finally { await observer.end(); }
    await save();
    for (const [phase, action, attempts] of [["interrupted", "arm", 2], ["recovery", null, 3], ["concurrent", "arm-concurrent", 5], ["final", null, 6]]) {
      if (action) await control(action);
      const started = performance.now();
      const responses = await Promise.all(Array.from({ length: phase === "concurrent" ? 2 : 1 }, () => request(appURL + "/query")));
      const elapsed = performance.now() - started;
      responses.sort((a, b) => a.status - b.status);
      assert.deepEqual(responses.map(value => value.status), phase === "interrupted" ? [503] : phase === "concurrent" ? [200, 503] : [200]);
      for (const response of responses) assert.deepEqual(response.body, response.status === 200 ? complete : { error: "dependency_unavailable", retryable: true });
      assert.ok(elapsed < 3500);
      const after = await status(); assert.equal(after.instance_id, proof.instance_id);
      assert.equal(after.attempts, attempts); assert.equal(after.tls_connections_verified, attempts);
      assert.equal(after.read_cuts_fired, phase === "interrupted" || phase === "recovery" ? 1 : 2);
      assert.equal(after.concurrent_read_pairs, phase === "interrupted" || phase === "recovery" ? 0 : 1);
      assert.equal(after.read_cut_armed, false); assert.equal(after.read_pair_armed, false);
      proof[phase] = { responses, elapsed_ms: elapsed, status: after, database: await retirement() }; await save();
    }
  } else if (stage === "cleanup") {
    await control("stop"); const after = await status(); assert.equal(after.stopped, true);
    proof.cleanup = { status: after, database: await retirement() };
  } else throw new Error("invalid_native_read_cut_stage");
  await save(); return proof;
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.env.GREGALE_DEPENDENCY_DATABASE_DISPOSABLE !== "1") throw new Error("disposable_database_required");
    if (process.argv[2] === "check") {
      const text = await readFile(process.argv[4], "utf8"); assert.ok(text.length <= 65536); const proof = JSON.parse(text);
      assert.equal(proof.namespace, process.argv[3]);
      console.log(JSON.stringify(await backendRetirement(process.env.GREGALE_DEPENDENCY_DATABASE_URL, process.argv[3], proof.backend_baseline_pids, "gregale-native-read-cut-")));
    } else {
      await verifyNativeReadCut(process.argv[2], { appURL: process.env.GREGALE_TEST_URL, providerURL: process.env.GREGALE_TEST_SERVICE_PAYMENT_URL,
        namespace: process.env.GREGALE_TEST_RUN_ID, token: process.env.GREGALE_NATIVE_AUTH_CONTROL_TOKEN,
        databaseURL: process.env.GREGALE_DEPENDENCY_DATABASE_URL, proofPath: process.env.GREGALE_NATIVE_READ_CUT_PROOF });
      console.log(JSON.stringify({ stage: process.argv[2], passed: true }));
    }
  } catch { console.error("Native PostgreSQL read interruption acceptance failed"); process.exitCode = 1; }
}
