// Explicit opt-in only: this creates roles and the public.notes workload in a
// disposable database. Run npm run test:postgres; missing inputs fail, not skip.
import test from "node:test";
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { once } from "node:events";
import { setTimeout as delay } from "node:timers/promises";
import {mkdtemp, rename, rm, writeFile} from "node:fs/promises";
import {createServer} from "node:http";
import {tmpdir} from "node:os";
import {join} from "node:path";
import express from "express";
import pg from "pg";
import { createApp } from "../app.js";
import { createDatabase, poolConfig } from "../database.js";
import { migrate } from "../migrate.js";

test("generated PostgreSQL starter survives real TLS/SCRAM connection failures", { timeout: 60_000 }, async (t) => {
  assert.equal(process.env.GREGALE_POSTGRES_STARTER_TEST_DISPOSABLE, "1", "explicit disposable database opt-in required");
  const url = process.env.GREGALE_POSTGRES_STARTER_TEST_URL;
  assert.ok(url, "GREGALE_POSTGRES_STARTER_TEST_URL required");
  const admin = new pg.Client(poolConfig(url));
  await admin.connect();
  const suffix = randomBytes(8).toString("hex"), password = randomBytes(24).toString("hex");
  const role = `starter_${suffix}`, nextRole = `starter_next_${suffix}`, rotationRole = `starter_rotation_${suffix}`;
  const databases = [], servers = [], idleErrors = [];
  const ownedRoles = [];
  const terminate = (name) => admin.query("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1", [name]);
  assert.equal((await admin.query("SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()")).rows[0].ssl, true);
  assert.equal((await admin.query("SELECT to_regclass('public.notes') IS NULL AS empty")).rows[0].empty, true, "fixture must have no existing notes table");
  t.after(async () => {
    for (const server of servers) { server.closeAllConnections(); await new Promise((resolve) => server.close(resolve)); }
    for (const db of databases) await db.close();
    await admin.query("ROLLBACK");
    for (const name of ownedRoles) await terminate(name);
    await admin.query("DROP TABLE IF EXISTS notes, notes_saved");
    for (const name of ownedRoles) { await admin.query(`DROP OWNED BY "${name}"`); await admin.query(`DROP ROLE "${name}"`); }
    await admin.end();
  });
  // Migration is the actual starter release entrypoint. The runtime receives
  // only a restricted login; owner credentials remain in this test process.
  await migrate(url);
  for (const name of [role, nextRole, rotationRole]) {
    await admin.query(`CREATE ROLE "${name}" LOGIN PASSWORD '${password}'`);
    ownedRoles.push(name);
    await admin.query(`GRANT USAGE ON SCHEMA public TO "${name}"`);
    await admin.query(`GRANT SELECT, INSERT, UPDATE, DELETE ON notes TO "${name}"`);
    await admin.query(`GRANT USAGE, SELECT ON SEQUENCE notes_id_seq TO "${name}"`);
  }
  async function runtime(name) {
    const uri = new URL(url); uri.username = name; uri.password = password;
    const config = poolConfig(uri.href);
    const db = createDatabase(() => new pg.Pool(config), { env: {DATABASE_URL: uri.href}, log: (code) => idleErrors.push(code) });
    databases.push(db); await db.start(); return db;
  }
  async function listen(db) {
    const server = createApp(express, db).listen(0, "127.0.0.1"); servers.push(server); await once(server, "listening");
    return `http://127.0.0.1:${server.address().port}`;
  }
  const db = await runtime(role), base = await listen(db);
  const hook = () => fetch(`${base}/internal/after-restore`, { method: "POST", headers: { "X-Faas-After-Restore": "1" } });
  async function note(body, signal) {
    return fetch(`${base}/notes`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ body }), signal });
  }
  async function waitFor(check) {
    const end = Date.now() + 3_000;
    while (!await check()) { assert.ok(Date.now() < end, "fixture observation deadline exceeded"); await delay(10); }
  }
  await t.test("serving rejects migration credentials without exposing either URL", () => {
    const result = spawnSync(process.execPath, [fileURLToPath(new URL("../handler.js", import.meta.url))], {
      encoding: "utf8", env: { ...process.env, DATABASE_URL: url, MIGRATION_DATABASE_URL: url },
    });
    assert.equal(result.status, 1);
    assert.match(result.stderr, /must not receive MIGRATION_DATABASE_URL/);
    assert.ok(!result.stderr.includes(url) && !result.stderr.includes(password));
  });
  await t.test("release session termination fails safely and a subsequent migration succeeds", async () => {
    await admin.query("SELECT pg_advisory_lock(734928146)");
    const uri = new URL(url), label = `starter_release_${suffix}`;
    uri.searchParams.set("application_name", label);
    const failed = assert.rejects(migrate(uri.href));
    try {
      await waitFor(async () => (await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock'", [label])).rows[0].count > 0);
      await admin.query("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name=$1", [label]);
      await failed;
    } finally { await admin.query("SELECT pg_advisory_unlock(734928146)"); }
    await migrate(url);
  });
  await t.test("runtime HTTP writes use the release-created schema and cannot perform DDL", async () => {
    assert.equal((await note("preserved")).status, 201);
    await assert.rejects(db.query("CREATE TABLE forbidden(id int)", [], { write: true }), /database_unavailable/);
    assert.equal((await fetch(`${base}/healthz`)).status, 200);
  });
  await t.test("idle connection termination is handled and restore replaces sessions without writes", async () => {
    const oldPID = (await db.query("SELECT pg_backend_pid() AS pid")).rows[0].pid;
    await terminate(role); await waitFor(() => idleErrors.length > 0);
    const before = (await admin.query("SELECT count(*)::int AS count FROM notes")).rows[0].count;
    assert.equal((await hook()).status, 204); assert.equal(db.generation, 1);
    const newPID = (await db.query("SELECT pg_backend_pid() AS pid")).rows[0].pid;
    assert.notEqual(newPID, oldPID);
    assert.equal((await admin.query("SELECT count(*)::int AS count FROM notes")).rows[0].count, before);
    const result = await (await fetch(`${base}/notes`)).json(); assert.equal(result.notes[0].body, "preserved");
  });
  await t.test("HTTP cancellation before commit cannot leave a delayed write", async () => {
    await admin.query("BEGIN"); await admin.query("LOCK TABLE notes IN ACCESS EXCLUSIVE MODE");
    const controller = new AbortController();
    const pending = assert.rejects(note("cancelled", controller.signal), /abort/i);
    async function blocked() {
      // Statistics are otherwise cached for admin's lock-holding transaction.
      await admin.query("SELECT pg_stat_clear_snapshot()");
      return (await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [role])).rows[0].count;
    }
    try {
      await waitFor(async () => await blocked() > 0);
      controller.abort(); await pending;
      await waitFor(() => idleErrors.includes("database_request_cancelled"));
    } finally { controller.abort(); await pending; await admin.query("ROLLBACK"); }
    // A server blocked on a lock may observe a disconnected socket only after
    // the lock is released. The important guarantee is no COMMIT or replay.
    await waitFor(async () => (await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [role])).rows[0].count === 0);
    assert.equal((await admin.query("SELECT count(*)::int AS count FROM notes WHERE body='cancelled'")).rows[0].count, 0);
    assert.equal((await fetch(`${base}/healthz`)).status, 200);
  });
  await t.test("active session termination rejects once and subsequent requests recover", async () => {
    await admin.query("BEGIN"); await admin.query("LOCK TABLE notes IN ACCESS EXCLUSIVE MODE");
    const pending = note("terminated");
    try {
      await waitFor(async () => {
        await admin.query("SELECT pg_stat_clear_snapshot()");
        return (await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [role])).rows[0].count > 0;
      });
      await terminate(role);
      const response = await pending;
      assert.equal(response.status, 503);
      assert.deepEqual(await response.json(), { ok: false, error: "database_unavailable" });
    } finally { await admin.query("ROLLBACK"); }
    assert.equal((await admin.query("SELECT count(*)::int AS count FROM notes WHERE body='terminated'")).rows[0].count, 0);
    assert.equal((await fetch(`${base}/healthz`)).status, 200);
  });
  await t.test("a missing workload fails restore and keeps readiness closed", async () => {
    await admin.query("ALTER TABLE notes RENAME TO notes_saved");
    assert.equal((await hook()).status, 503); assert.equal(db.generation, 1);
    assert.equal((await fetch(`${base}/healthz`)).status, 503);
    await admin.query("ALTER TABLE notes_saved RENAME TO notes");
    assert.equal((await hook()).status, 204); assert.equal(db.generation, 2);
  });
  await t.test("before_checkpoint closes every server session; restore reconnects", async () => {
    const sessions = async () => (await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE usename=$1", [role])).rows[0].count;
    const checkpoint = () => fetch(`${base}/internal/before-checkpoint`, { method: "POST", headers: { "X-Faas-Before-Checkpoint": "1" } });
    assert.equal((await fetch(`${base}/healthz`)).status, 200);
    assert.ok(await sessions() > 0, "the pool keeps an idle session after a request");
    assert.equal((await checkpoint()).status, 204);
    await waitFor(async () => await sessions() === 0);
    assert.equal((await fetch(`${base}/healthz`)).status, 503);
    assert.equal((await checkpoint()).status, 204); // A retried capture finds it drained.
    assert.equal(await sessions(), 0);
    assert.equal((await hook()).status, 204); assert.equal(db.generation, 3);
    assert.equal((await fetch(`${base}/healthz`)).status, 200);
    const result = await (await fetch(`${base}/notes`)).json(); assert.ok(result.notes.some(note => note.body === "preserved"));
  });
  await t.test("atomic rotation drains an open write, rejects stale SCRAM credentials, and fences restore races", async () => {
    const directory = await mkdtemp(join(tmpdir(), 'gregale-postgres-rotation-'));
    const receipts = [], pools = [];
    const generation = 'e'.repeat(32), nextPassword = randomBytes(24).toString('hex');
    let current = 'a'.repeat(64);
    const snapshot = join(directory, 'snapshot');
    const uri = credential => {const value = new URL(url); value.username = rotationRole; value.password = credential; return value.href;};
    const publish = async (revision, secrets = {DATABASE_URL: uri(nextPassword)}) => {
      current = revision.repeat(64);
      const pending = join(directory, 'pending');
      await writeFile(pending, JSON.stringify({revision: current, secrets}), {mode: 0o600});
      await rename(pending, snapshot);
    };
    await publish('a', {DATABASE_URL: uri(password)});
    const ack = createServer((request, response) => {
      void (async () => {
        let body = ''; for await (const chunk of request) body += chunk;
        const receipt = JSON.parse(body); receipts.push(receipt);
        response.writeHead(receipt.revision === current ? 202 : 409).end();
      })().catch(() => response.writeHead(500).end());
    });
    ack.listen(0, '127.0.0.1'); await once(ack, 'listening');
    const managed = createDatabase(config => {
      const pool = new pg.Pool(poolConfig(config.connectionString)), entry = {pool, ends: 0};
      const end = pool.end.bind(pool); pool.end = () => {entry.ends++; return end();};
      pools.push(entry); return pool;
    }, {env: {DATABASE_URL: uri(password), FAAS_SECRETS_SNAPSHOT_FILE: snapshot,
      FAAS_SECRETS_RELOAD_READY_FILE: join(directory, 'ready'), FAAS_SECRETS_RELOAD_GENERATION: generation,
      FAAS_SECRETS_RELOAD_ACK_ENDPOINT: `http://127.0.0.1:${ack.address().port}/ack`},
      log: code => idleErrors.push(code),
    });
    databases.push(managed);
    const blocker = new pg.Client(poolConfig(url)); await blocker.connect();
    let heldWrite;
    try {
      await managed.start(); const managedBase = await listen(managed);
      const health = () => fetch(`${managedBase}/healthz`);
      const restore = () => fetch(`${managedBase}/internal/after-restore`, {method:'POST', headers:{'X-Faas-After-Restore':'1'}});
      await blocker.query('BEGIN'); await blocker.query('LOCK TABLE notes IN SHARE MODE');
      let finished = false;
      heldWrite = fetch(`${managedBase}/notes`, {method:'POST',headers:{'content-type':'application/json'},
        body:JSON.stringify({body:'rotated-once'})}).then(response => {finished = true; return response;});
      await waitFor(async () => (await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'",[rotationRole])).rows[0].count > 0);
      await admin.query(`ALTER ROLE "${rotationRole}" PASSWORD '${nextPassword}'`);
      await publish('b'); process.kill(process.pid, 'SIGHUP');
      await waitFor(() => receipts.some(r => r.revision === 'b'.repeat(64) && r.status === 'applied'));
      assert.equal(finished, false); assert.equal(pools[0].ends, 0);
      await publish('c', {DATABASE_URL: uri(password)});
      await assert.rejects(managed.reload(), /database_unavailable/);
      assert.equal((await health()).status, 503);
      await publish('d', {}); await assert.rejects(managed.reload());
      assert.equal(pools[0].ends, 0); assert.equal(finished, false);
      await blocker.query('COMMIT');
      assert.equal((await heldWrite).status, 201);
      await waitFor(() => pools[0].ends === 1);
      assert.equal((await admin.query("SELECT count(*)::int AS count FROM notes WHERE body='rotated-once'")).rows[0].count, 1);
      await publish('e'); assert.equal((await restore()).status, 204);
      assert.equal(managed.generation, 1); assert.equal((await health()).status, 200);
      // Hold the real schema validation during SIGHUP, then time out a restore
      // queued behind it. Neither that candidate nor the queued reload may ACK applied.
      await blocker.query('BEGIN'); await blocker.query('LOCK TABLE notes IN ACCESS EXCLUSIVE MODE');
      await publish('f'); process.kill(process.pid, 'SIGHUP');
      await waitFor(async () => (await admin.query("SELECT count(*)::int AS count FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'",[rotationRole])).rows[0].count > 0);
      assert.equal((await restore()).status, 503); assert.equal(managed.generation, 1);
      await blocker.query('ROLLBACK');
      await waitFor(() => receipts.some(r => r.revision === 'f'.repeat(64)));
      assert.ok(!receipts.some(r => r.revision === 'f'.repeat(64) && r.status === 'applied'));
      assert.equal((await health()).status, 503);
      await publish('1'); assert.equal((await restore()).status, 204, JSON.stringify({logs:idleErrors,receipts,pools:pools.map(p=>p.ends)}));
      assert.equal(managed.generation, 2); assert.equal((await health()).status, 200);
      assert.deepEqual(receipts.map(r => r.status), ['applied','applied','failed','failed','applied','failed','applied']);
      assert.ok(receipts.every(r => r.generation === generation && Object.keys(r).sort().join(',') === 'generation,revision,status'));
      assert.ok(!JSON.stringify(receipts).includes(password));
    } finally {
      await blocker.query('ROLLBACK'); await blocker.end();
      await heldWrite?.catch(() => {});
      await managed.close();
      ack.closeAllConnections(); await new Promise(resolve => ack.close(resolve));
      await rm(directory, {recursive:true,force:true});
    }
    assert.ok(pools.every(entry => entry.ends === 1));
  });
  await t.test("retired held and fresh credentials fail; replacement preserves data", async () => {
    await admin.query(`ALTER ROLE "${role}" NOLOGIN`); await terminate(role);
    assert.equal((await fetch(`${base}/healthz`)).status, 503);
    assert.equal((await hook()).status, 503);
    const replacement = await runtime(nextRole), nextBase = await listen(replacement);
    assert.equal((await fetch(`${nextBase}/healthz`)).status, 200);
    assert.ok((await (await fetch(`${nextBase}/notes`)).json()).notes.some(note => note.body === "preserved"));
  });
  await t.test("public lifecycle attempts cannot reset or drain a pool", async () => {
    for (const path of ["after-restore", "before-checkpoint"]) {
      const result = await fetch(`${base}/internal/${path}`, { method: "POST" });
      assert.equal(result.status, 403);
    }
  });
});
