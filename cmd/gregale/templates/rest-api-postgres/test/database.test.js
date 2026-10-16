import test from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { createDatabase, poolConfig } from "../database.js";

const deferred = () => { let resolve; const promise = new Promise((done) => { resolve = done; }); return { promise, resolve }; };
const tick = () => new Promise((resolve) => setImmediate(resolve));
function fixture(options = {}) {
  const pools = [], logs = [], next = {};
  const db = createDatabase(() => {
    const pool = new EventEmitter();
    pool.execute = next.execute; delete next.execute;
    pool.clients = []; pool.ended = 0;
    pool.connect = async () => {
      if (pool.pending) return pool.pending.promise;
      const client = Object.assign(new EventEmitter(), { queries: [], releases: [], async query(sql, params) {
        this.queries.push({ sql, params });
        if (pool.execute) return pool.execute(sql, params);
        return { rows: [{ id: 1 }] };
      }, release(destroy = false) { this.releases.push(destroy); } });
      pool.clients.push(client); return client;
    };
    pool.end = async () => { pool.ended += 1; };
    pools.push(pool); return pool;
  }, { log: (code) => logs.push(code), ...options });
  return { db, pools, logs, next };
}

test("idle SQL errors are handled without printing provider details", async () => {
  const { db, pools, logs } = fixture();
  pools[0].emit("error", new Error("password=secret provider.example"));
  assert.deepEqual(logs, ["database_idle_connection_lost"]);
  await db.query("SELECT 1"); await db.close();
});

test("managed SSL URLs preserve certificate verification instead of overriding it", () => {
  for (const mode of ["require", "verify-ca", "verify-full"]) {
    const config = poolConfig(`postgres://user:secret@db.example/db?sslmode=${mode}`);
    assert.deepEqual(config.ssl, { rejectUnauthorized: true });
    assert.equal(new URL(config.connectionString).searchParams.has("sslmode"), false);
  }
  assert.equal(poolConfig("postgres://user@localhost/db?sslmode=disable").ssl, false);
  assert.throws(() => poolConfig("postgres://user@db/db?sslmode=prefer"));
  assert.throws(() => poolConfig("postgres://user@db/db?sslrootcert=/private/key"));
  assert.throws(() => poolConfig("postgres://user@db/db?ssl=0&sslmode=require"));
  assert.throws(() => poolConfig("postgres://user@db/db?uselibpqcompat=true&sslmode=require"));
});

test("active session errors are handled without committing or replaying a write", async () => {
  const { db, pools, logs } = fixture();
  pools[0].execute = async (sql) => {
    if (sql === "INSERT fixture") pools[0].clients[0].emit("error", new Error("secret SQL detail"));
    return { rows: [] };
  };
  await assert.rejects(db.query("INSERT fixture", [], { write: true }), /database_unavailable/);
  const client = pools[0].clients[0];
  assert.deepEqual(logs, ["database_active_connection_lost"]);
  assert.equal(client.queries.filter((q) => q.sql === "INSERT fixture").length, 1);
  assert.ok(!client.queries.some((q) => q.sql === "COMMIT"));
  assert.deepEqual(client.releases, [true]); assert.equal(client.listenerCount("error"), 0);
  await db.close();
});

test("restore coalesces concurrent callbacks, replaces the pool and only reads", async () => {
  const { db, pools } = fixture();
  await db.query("SELECT 1");
  const first = db.restore(), second = db.restore();
  assert.equal(first, second);
  await assert.rejects(db.query("SELECT 1"), /database_unavailable/);
  await first;
  assert.equal(pools.length, 2); assert.equal(pools[0].ended, 1); assert.equal(db.generation, 1);
  assert.deepEqual(pools[1].clients[0].queries.map((q) => q.sql), ["BEGIN READ ONLY", "SELECT set_config('statement_timeout', $1, true)", "SELECT id FROM notes LIMIT 1", "COMMIT"]);
  await db.restore(); assert.equal(db.generation, 2);
  await db.close(); await db.close();
  assert.ok(pools.every((pool) => pool.ended === 1));
});

test("cancellation before commit destroys the session and never replays a write", async () => {
  const { db, pools } = fixture(); const blocked = deferred(); const controller = new AbortController();
  pools[0].execute = async (sql) => sql === "INSERT fixture" ? blocked.promise : { rows: [] };
  const result = db.query("INSERT fixture", [], { write: true, signal: controller.signal });
  await tick(); controller.abort();
  await assert.rejects(result, /database_cancelled/);
  blocked.resolve({ rows: [] }); await tick();
  const client = pools[0].clients[0];
  assert.equal(client.queries.filter((q) => q.sql === "INSERT fixture").length, 1);
  assert.ok(!client.queries.some((q) => q.sql === "COMMIT"));
  assert.deepEqual(client.releases, [true]); await db.close();
});

test("a client acquired after cancellation is destroyed rather than leaked", async () => {
  const { db, pools } = fixture(); const pending = deferred(); pools[0].pending = pending;
  const controller = new AbortController(); const result = db.query("SELECT 1", [], { signal: controller.signal });
  controller.abort(); await assert.rejects(result, /database_cancelled/);
  const releases = []; pending.resolve(Object.assign(new EventEmitter(), { release: (destroy) => releases.push(destroy) }));
  await tick(); assert.deepEqual(releases, [true]); await db.close();
});

test("request deadlines and admission bound an unavailable database", async () => {
  const { db, pools } = fixture({ timeoutMS: 30 }); pools[0].execute = () => new Promise(() => {});
  const requests = Array.from({ length: 16 }, () => assert.rejects(db.query("SELECT 1"), /database_cancelled/));
  await assert.rejects(db.query("SELECT 1"), /database_busy/);
  await Promise.all(requests);
  assert.ok(pools[0].clients.every((client) => client.releases.length === 1 && client.releases[0]));
  await db.close();
});

test("restore cancels captured work and a failed readiness probe stays closed", async () => {
  const { db, pools, next } = fixture(); const blocked = deferred();
  pools[0].execute = () => blocked.promise;
  const work = assert.rejects(db.query("SELECT 1"), /database_cancelled/);
  const restore = db.restore(); await tick();
  // Run another restore with a missing-schema error on the fresh pool.
  await restore; await work; blocked.resolve({ rows: [] });
  next.execute = () => { throw new Error("secret provider detail"); };
  await assert.rejects(db.restore(), /database_unavailable/);
  assert.equal(db.generation, 1);
  await assert.rejects(db.query("SELECT 1"), /database_unavailable/);
  await db.restore(); assert.equal(db.generation, 2); await db.close();
});

test("close during restore prevents late readiness publication", async () => {
  const { db } = fixture(); const restore = db.restore();
  const failure = assert.rejects(restore, /database_unavailable/);
  await db.close(); await failure;
  assert.equal(db.generation, 0);
  await assert.rejects(db.query("SELECT 1"), /database_unavailable/);
  await assert.rejects(db.restore(), /database_unavailable/);
});

test("shutdown aborts an already running readiness probe", async () => {
  const { db, pools, next } = fixture();
  next.execute = () => new Promise(() => {});
  const restore = db.restore();
  const failure = assert.rejects(restore, /database_unavailable/);
  await tick();
  assert.equal(pools[1].clients.length, 1);
  await db.close(); await failure;
  assert.equal(db.generation, 0);
  assert.deepEqual(pools[1].clients[0].releases, [true]);
});
