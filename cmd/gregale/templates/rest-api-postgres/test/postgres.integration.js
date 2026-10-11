// Explicit opt-in only: this creates roles and the public.notes workload in a
// disposable database. Run npm run test:postgres; missing inputs fail, not skip.
import test from "node:test";
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { once } from "node:events";
import { setTimeout as delay } from "node:timers/promises";
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
  const role = `starter_${suffix}`, nextRole = `starter_next_${suffix}`;
  const databases = [], servers = [], idleErrors = [];
  const ownedRoles = [];
  const terminate = (name) => admin.query("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1", [name]);
  assert.equal((await admin.query("SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()")).rows[0].ssl, true);
  assert.equal((await admin.query("SELECT to_regclass('public.notes') IS NULL AS empty")).rows[0].empty, true, "fixture must have no existing notes table");
  t.after(async () => {
    for (const server of servers) { server.closeAllConnections(); await new Promise((resolve) => server.close(resolve)); }
    for (const db of databases) await db.close();
    await admin.query("ROLLBACK");
    await terminate(role); await terminate(nextRole);
    await admin.query("DROP TABLE IF EXISTS notes, notes_saved");
    for (const name of ownedRoles) { await admin.query(`DROP OWNED BY "${name}"`); await admin.query(`DROP ROLE "${name}"`); }
    await admin.end();
  });
  // Migration is the actual starter release entrypoint. The runtime receives
  // only a restricted login; owner credentials remain in this test process.
  await migrate(url);
  for (const name of [role, nextRole]) {
    await admin.query(`CREATE ROLE "${name}" LOGIN PASSWORD '${password}'`);
    ownedRoles.push(name);
    await admin.query(`GRANT USAGE ON SCHEMA public TO "${name}"`);
    await admin.query(`GRANT SELECT, INSERT, UPDATE, DELETE ON notes TO "${name}"`);
    await admin.query(`GRANT USAGE, SELECT ON SEQUENCE notes_id_seq TO "${name}"`);
  }
  function runtime(name) {
    const uri = new URL(url); uri.username = name; uri.password = password;
    const config = poolConfig(uri.href);
    const db = createDatabase(() => new pg.Pool(config), { log: (code) => idleErrors.push(code) });
    databases.push(db); return db;
  }
  async function listen(db) {
    const server = createApp(express, db).listen(0, "127.0.0.1"); servers.push(server); await once(server, "listening");
    return `http://127.0.0.1:${server.address().port}`;
  }
  const db = runtime(role), base = await listen(db);
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
  await t.test("retired held and fresh credentials fail; replacement preserves data", async () => {
    await admin.query(`ALTER ROLE "${role}" NOLOGIN`); await terminate(role);
    assert.equal((await fetch(`${base}/healthz`)).status, 503);
    assert.equal((await hook()).status, 503);
    const replacement = runtime(nextRole), nextBase = await listen(replacement);
    assert.equal((await fetch(`${nextBase}/healthz`)).status, 200);
    assert.equal((await (await fetch(`${nextBase}/notes`)).json()).notes[0].body, "preserved");
  });
  await t.test("public restore attempts cannot reset a pool", async () => {
    const result = await fetch(`${base}/internal/after-restore`, { method: "POST" });
    assert.equal(result.status, 403);
  });
});
