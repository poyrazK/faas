import assert from "node:assert/strict";
import { readFile, writeFile, rename } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";
import pg from "../payment-postgres/client.js";
import { namespaceValue, poolConfig } from "../payment-postgres/database.js";
import { defaultOrder, fingerprint } from "../payment-postgres/contract.js";
import { backendRetirement } from "./native-auth-acceptance.mjs";
import { cleanupNamespace } from "./postgres-cleanup.mjs";

async function observe(databaseURL, namespace) {
  const pool = new pg.Pool(poolConfig(databaseURL)); pool.on("error", () => {});
  try {
    const result = await pool.query(`SELECT l.id AS ledger_id,
      count(c.operation_key)::int AS charges, count(*) FILTER(WHERE c.state='pending')::int AS pending,
      count(*) FILTER(WHERE c.operation_key='ack-loss' AND c.state='confirmed' AND c.fingerprint=$2)::int AS matching
      FROM public.gregale_write_ledgers l LEFT JOIN public.gregale_write_charges c USING(namespace)
      WHERE l.namespace=$1 GROUP BY l.id`, [namespace, fingerprint(defaultOrder)]);
    assert.equal(result.rows.length, 1); return result.rows[0];
  } finally { await pool.end(); }
}

export async function verifyNativeCommit(stage, { appURL, providerURL, namespace, token, databaseURL, proofPath, faultMode = "drop" }) {
  assert.ok(["drop", "request-deadline", "response-deadline"].includes(faultMode));
  const deadline = faultMode !== "drop", requestHeld = faultMode === "request-deadline";
  namespaceValue(namespace); assert.ok(isAbsolute(proofPath)); assert.match(token, /^[0-9a-f]{64}$/);
  const request = async (url, options) => {
    const result = await fetch(url, { ...options, redirect: "error", signal: AbortSignal.timeout(8000) });
    const body = await result.text(); assert.ok(body.length <= 65536); return { status: result.status, body: JSON.parse(body) };
  };
  const control = async action => {
    const result = await request(providerURL + "/native-commit-control", { method: "POST", headers: { authorization: "Bearer " + token, "content-type": "application/json" }, body: JSON.stringify({ namespace, action }) });
    assert.equal(result.status, 200); return result.body;
  };
  const status = async () => {
    const end = Date.now() + 5000;
    while (Date.now() < end) {
      const result = await request(providerURL + "/native-commit-status"); assert.equal(result.status, 200);
      const value = result.body; assert.equal(value.namespace, namespace);
      if (!value.active_writes && !value.queued_writes && !value.pool_total && !value.pool_waiting && !value.connections && !value.pending_connections && !value.pending_authentication && !value.active_faults) return value;
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error("native_commit_work_not_joined");
  };
  const write = payload => request(appURL + "/checkout", { method: "POST", headers: { "content-type": "application/json", "idempotency-key": "ack-loss" }, body: JSON.stringify(payload) });
  let proof;
  try { const text = await readFile(proofPath, "utf8"); assert.ok(text.length <= 65536); proof = JSON.parse(text); }
  catch (error) { if (error.code !== "ENOENT") throw error; proof = { namespace, fault_mode: faultMode }; }
  assert.equal(proof.namespace, namespace); assert.equal(proof.fault_mode, faultMode);
  const save = async () => { await writeFile(proofPath + ".tmp", JSON.stringify(proof) + "\n", { mode: 0o600 }); await rename(proofPath + ".tmp", proofPath); };
  const retirement = () => backendRetirement(databaseURL, namespace, proof.backend_baseline_pids, "gregale-write-ledger-");
  if (stage === "all") {
    proof.baseline = await status(); assert.equal(proof.baseline.attempts, 0); assert.equal(proof.baseline.commits_forwarded, 0);
    proof.instance_id = proof.baseline.instance_id; proof.ledger_id = proof.baseline.ledger_id;
    const observer = new pg.Pool(poolConfig(databaseURL)); observer.on("error", () => {});
    try { proof.backend_baseline_pids = (await observer.query("SELECT pid FROM pg_stat_activity WHERE backend_type='client backend'")).rows.map(row => row.pid); }
    finally { await observer.end(); }
    await save(); // Preserve cleanup identity even when subsequent assertions fail.
    await control("arm");
    const started = performance.now();
    const unknown = await write(defaultOrder);
    const elapsed = performance.now() - started;
    if (deadline) assert.ok(elapsed >= 1000 && elapsed < 3500);
    assert.equal(unknown.status, 503); assert.deepEqual(unknown.body, { ...(deadline ? {} : { attempts: 1, statuses: [503], body_errors: [] }), error: "payment_unavailable", payment_outcome: "unknown", retryable: true });
    const lost = await status(); // Join the database acquisition/query and canceled hold before observing state.
    const committed = await observe(databaseURL, namespace);
    assert.deepEqual(committed, { ledger_id: proof.ledger_id, charges: requestHeld ? 0 : 1, pending: 0, matching: requestHeld ? 0 : 1 });
    assert.equal(lost.attempts, 1); assert.equal(lost.acknowledgements_dropped, deadline ? 0 : 1); assert.equal(lost.commits_forwarded, requestHeld ? 0 : 1);
    assert.equal(lost.fault_mode, faultMode);
    if (deadline) {
      assert.equal(lost.faults.length, 1); const record = lost.faults[0];
      assert.equal(record.mode, "stall"); assert.equal(record.direction, requestHeld ? "request" : "response");
      assert.equal(record.configured_ms, 5000); assert.equal(record.outcome, "canceled");
      assert.ok(record.elapsed_ms >= 3000 && record.elapsed_ms < 5000);
    }
    proof.unknown = { elapsed_ms: elapsed, deadline_ms: deadline ? 1500 : null, response: unknown, database: committed, status: lost, retirement: await retirement() }; await save();
    const replay = await write(defaultOrder);
    assert.equal(replay.status, 200); assert.deepEqual(replay.body, { attempts: 1, statuses: [201], body_errors: [], id: "ack-loss", status: "confirmed" });
    const conflict = await write({ ...defaultOrder, quantity: 2 });
    assert.equal(conflict.status, 409); assert.deepEqual(conflict.body, { attempts: 1, statuses: [409], body_errors: [], error: "idempotency_conflict" });
    const recovered = await observe(databaseURL, namespace); assert.deepEqual(recovered, { ledger_id: proof.ledger_id, charges: 1, pending: 0, matching: 1 });
    const after = await status(); assert.equal(after.attempts, 3); assert.equal(after.commits_forwarded, requestHeld ? 1 : 2); assert.equal(after.acknowledgements_dropped, deadline ? 0 : 1);
    assert.equal(after.instance_id, proof.instance_id); assert.equal(after.ledger_id, proof.ledger_id);
    proof.replay = { response: replay, conflict, database: recovered, status: after, retirement: await retirement() };
  } else if (stage === "cleanup") {
    await control("stop");
    const after = await status(); assert.equal(after.stopped, true);
    const database = await cleanupNamespace(databaseURL, namespace);
    proof.cleanup = { status: after, database, retirement: await retirement() };
  } else throw new Error("invalid_native_commit_stage");
  await save(); return proof;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.env.GREGALE_DEPENDENCY_DATABASE_DISPOSABLE !== "1") throw new Error("disposable_database_required");
    const databaseURL = process.env.GREGALE_DEPENDENCY_DATABASE_URL;
    if (process.argv[2] === "check") {
      const namespace = process.argv[3], text = await readFile(process.argv[4], "utf8"); assert.ok(text.length <= 65536);
      const proof = JSON.parse(text); assert.equal(proof.namespace, namespace);
      console.log(JSON.stringify({ ...await cleanupNamespace(databaseURL, namespace, true), ...await backendRetirement(databaseURL, namespace, proof.backend_baseline_pids, "gregale-write-ledger-") }));
    } else {
      await verifyNativeCommit(process.argv[2], { appURL: process.env.GREGALE_TEST_URL, providerURL: process.env.GREGALE_TEST_SERVICE_PAYMENT_URL,
        namespace: process.env.GREGALE_TEST_RUN_ID, token: process.env.GREGALE_NATIVE_AUTH_CONTROL_TOKEN, databaseURL,
        proofPath: process.env.GREGALE_NATIVE_COMMIT_PROOF, faultMode: process.argv[3] || "drop" });
      console.log(JSON.stringify({ stage: process.argv[2], passed: true }));
    }
  } catch { console.error("Native COMMIT acknowledgement acceptance failed"); process.exitCode = 1; }
}
