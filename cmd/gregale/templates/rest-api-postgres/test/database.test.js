import test from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { createDatabase, poolConfig } from "../database.js";

const deferred = () => { let resolve; const promise = new Promise((done) => { resolve = done; }); return { promise, resolve }; };
const tick = () => new Promise((resolve) => setImmediate(resolve));
async function fixture(options = {}) {
  const pools = [], logs = [];
  const db = createDatabase(config => {
    const pool = new EventEmitter();
    pool.dsn = config.connectionString;
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
    pools.push(pool); options.configurePool?.(pool, pools.length); return pool;
  }, { env: {DATABASE_URL: "postgres://worker:fixture@db:5432/app?sslmode=disable"}, log: (code) => logs.push(code), ...options });
  await db.start();
  for (const pool of pools) pool.clients = [];
  return { db, pools, logs };
}

test("idle SQL errors are handled without printing provider details", async () => {
  const { db, pools, logs } = await fixture();
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
  const { db, pools, logs } = await fixture();
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
  const { db, pools } = await fixture();
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
  const { db, pools } = await fixture(); const blocked = deferred(); const controller = new AbortController();
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
  const { db, pools } = await fixture(); const pending = deferred(); pools[0].pending = pending;
  const controller = new AbortController(); const result = db.query("SELECT 1", [], { signal: controller.signal });
  await tick(); // The driver has admitted connect before cancellation.
  controller.abort(); await assert.rejects(result, /database_cancelled/);
  const closing = db.close(); await tick();
  assert.equal(pools[0].ended, 0); // A pending driver connection still owns the lease.
  const releases = []; pending.resolve(Object.assign(new EventEmitter(), { release: (destroy) => releases.push(destroy) }));
  await closing; assert.deepEqual(releases, [true]); assert.equal(pools[0].ended, 1);
});

test("request deadlines and admission bound an unavailable database", async () => {
  const { db, pools } = await fixture({ timeoutMS: 30 }); pools[0].execute = () => new Promise(() => {});
  const requests = Array.from({ length: 16 }, () => assert.rejects(db.query("SELECT 1"), /database_cancelled/));
  await assert.rejects(db.query("SELECT 1"), /database_busy/);
  await Promise.all(requests);
  assert.ok(pools[0].clients.every((client) => client.releases.length === 1 && client.releases[0]));
  await db.close();
});

test("restore cancels captured work and a failed readiness probe stays closed", async () => {
  const { db, pools } = await fixture({configurePool: (pool, index) => {
    if (index === 3) pool.execute = () => {throw new Error("secret");};
  }}); const blocked = deferred();
  pools[0].execute = () => blocked.promise;
  const work = assert.rejects(db.query("SELECT 1"), /database_cancelled/);
  const restore = db.restore(); await tick();
  // Run another restore with a missing-schema error on the fresh pool.
  await restore; await work; blocked.resolve({ rows: [] });
  pools[1].execute = () => { throw new Error("secret provider detail"); };
  const next = db.restore();
  await assert.rejects(next, /database_unavailable/);
  assert.equal(db.generation, 1);
  await assert.rejects(db.query("SELECT 1"), /database_unavailable/);
  await db.restore(); assert.equal(db.generation, 2); await db.close();
});

test("close during restore prevents late readiness publication", async () => {
  const { db } = await fixture(); const restore = db.restore();
  const failure = assert.rejects(restore, /database_unavailable/);
  await db.close(); await failure;
  assert.equal(db.generation, 0);
  await assert.rejects(db.query("SELECT 1"), /database_unavailable/);
  await assert.rejects(db.restore(), /database_unavailable/);
});

test("shutdown aborts an already running readiness probe", async () => {
  const entered = deferred();
  const { db, pools } = await fixture({configurePool: (pool, index) => {
    if (index === 2) pool.execute = () => {entered.resolve(); return new Promise(() => {});};
  }});
  const restore = db.restore();
  const failure = assert.rejects(restore, /database_unavailable/);
  await entered.promise;
  assert.equal(pools[1].clients.length, 1);
  await db.close(); await failure;
  assert.equal(db.generation, 0);
  assert.deepEqual(pools[1].clients[0].releases, [true]);
});

const managedURI = password => `postgres://worker:${password}@db:5432/app?sslmode=require`;
async function managedFixture(t, options = {}) {
  let raw = JSON.stringify({revision:'a'.repeat(64),secrets:{DATABASE_URL:managedURI('private-first')}});
  const receipts = [];
  const env = {DATABASE_URL: managedURI('private-stale-environment'), FAAS_SECRETS_SNAPSHOT_FILE:'/snapshot',
    FAAS_SECRETS_RELOAD_READY_FILE:'/ready', FAAS_SECRETS_RELOAD_ACK_ENDPOINT:'http://metadata.test/ack',
    FAAS_SECRETS_RELOAD_GENERATION:'e'.repeat(32)};
  const f = await fixture({...options, env, io:{read:async()=>raw, ready:async()=>{}, fetch:async(_url, init)=>{
    receipts.push(JSON.parse(init.body)); return new Response(null,{status:202});
  }}});
  t.after(()=>f.db.close());
  return {...f, receipts, publish:(version, secrets={DATABASE_URL:managedURI('private-second')})=>{
    raw=JSON.stringify({revision:version.repeat(64),secrets});
  }, malformed:()=>{raw='private-invalid-json';}};
}

test('managed adoption validates schema before ACK and uses the snapshot instead of stale environment', async t => {
  const entered=deferred(), finish=deferred();
  const f=await managedFixture(t,{configurePool:(pool,index)=>{
    if(index===2)pool.execute=async sql=>{
      if(sql==='SELECT id FROM notes LIMIT 1'){entered.resolve();await finish.promise;}
      return {rows:[]};
    };
  }});
  assert.equal(f.pools[0].dsn,managedURI('private-first'));
  f.publish('b');const reload=f.db.reload();
  try{
    await entered.promise;
    assert.equal(f.receipts.length,1);
    await assert.rejects(f.db.query('SELECT 1'),/database_unavailable/);
  }finally{finish.resolve();}
  await reload;
  assert.equal(f.pools[1].dsn,managedURI('private-second'));
  assert.deepEqual(f.receipts.map(r=>r.status),['applied','applied']);
  assert.ok(f.receipts.every(r=>Object.keys(r).sort().join(',')==='generation,revision,status'));
});

test('rotation and revocation retain an admitted write until COMMIT and connection release', async t => {
  const f=await managedFixture(t), entered=deferred(), finish=deferred();
  f.pools[0].execute=async sql=>{
    if(sql==='INSERT fixture'){entered.resolve();return finish.promise;}
    return {rows:[]};
  };
  const write=f.db.query('INSERT fixture',[],{write:true});await entered.promise;
  try{
    f.publish('b');await f.db.reload();
    assert.equal(f.pools[0].ended,0);
    f.publish('c',{});await assert.rejects(f.db.reload(),/database_unavailable/);
    await assert.rejects(f.db.query('SELECT 1'),/database_unavailable/);
    assert.equal(f.pools[0].ended,0);
  }finally{finish.resolve({rows:[{id:42}]});}
  assert.equal((await write).rows[0].id,42);await tick();
  assert.equal(f.pools[0].ended,1);
  const client=f.pools[0].clients[0];
  assert.equal(client.queries.filter(q=>q.sql==='INSERT fixture').length,1);
  assert.equal(client.queries.at(-1).sql,'COMMIT');assert.deepEqual(client.releases,[false]);
  assert.deepEqual(f.receipts.map(r=>r.status),['applied','applied','failed']);
});

test('failed or malformed adoption disables acquisitions, retires candidates, and can recover', async t => {
  const f=await managedFixture(t,{configurePool:(pool,index)=>{
    if(index===2)pool.execute=()=>{throw new Error(pool.dsn);};
  }});
  f.publish('b');await assert.rejects(f.db.reload(),error=>!error.message.includes('private-'));
  await tick();assert.equal(f.pools[1].ended,1);
  await assert.rejects(f.db.query('SELECT 1'),/database_unavailable/);
  f.malformed();await assert.rejects(f.db.reload());assert.equal(f.pools.length,2);
  assert.equal(f.receipts.length,2); // Malformed envelopes cannot identify a revision to ACK.
  f.publish('c');await f.db.reload();await f.db.query('SELECT 1');
  const built=f.pools.length;
  f.publish('d',{DATABASE_URL:managedURI('private-second'),MIGRATION_DATABASE_URL:'private-migration'});
  await assert.rejects(f.db.reload());assert.equal(f.pools.length,built);
  await assert.rejects(f.db.query('SELECT 1'),/database_unavailable/);
  assert.ok(!JSON.stringify(f.logs).includes('private-'));
});

test('restore cancellation fences a refresh queued behind rotation and rereads the latest snapshot on recovery', {timeout:3000}, async t => {
  const entered=deferred(), finish=deferred();
  const f=await managedFixture(t,{restoreTimeoutMS:30,configurePool:(pool,index)=>{
    if(index===2)pool.execute=async sql=>{
      if(sql==='SELECT id FROM notes LIMIT 1'){entered.resolve();await finish.promise;}
      return {rows:[]};
    };
  }});
  f.publish('b');const rotation=assert.rejects(f.db.reload());await entered.promise;
  try{
    await assert.rejects(f.db.restore(),/database_unavailable/);
    assert.equal(f.db.generation,0);assert.equal(f.pools.length,2);
  }finally{finish.resolve();}
  await rotation;await tick();
  assert.ok(!f.receipts.some(r=>r.revision==='b'.repeat(64)&&r.status==='applied'));
  await assert.rejects(f.db.query('SELECT 1'),/database_unavailable/);
  f.publish('c',{DATABASE_URL:managedURI('private-current')});await f.db.restore();
  assert.equal(f.db.generation,1);assert.equal(f.pools.at(-1).dsn,managedURI('private-current'));
  await f.db.query('SELECT 1');
});

test('restore timeout cancels active validation without a late publication or ACK', {timeout:3000}, async t => {
  const entered=deferred(), finish=deferred();
  const f=await managedFixture(t,{restoreTimeoutMS:30,configurePool:(pool,index)=>{
    if(index===2)pool.execute=async sql=>{
      if(sql==='SELECT id FROM notes LIMIT 1'){entered.resolve();await finish.promise;}
      return {rows:[]};
    };
  }});
  const restore=assert.rejects(f.db.restore(),/database_unavailable/);await entered.promise;
  await restore;finish.resolve();await tick();
  assert.equal(f.db.generation,0);assert.equal(f.pools[1].ended,1);assert.equal(f.receipts.length,1);
  await assert.rejects(f.db.query('SELECT 1'),/database_unavailable/);
  await f.db.restore();assert.equal(f.db.generation,1);
});

test('partial guest contracts and serving migration credentials never downgrade to environment loading', async () => {
  for(const extras of [{FAAS_SECRETS_SNAPSHOT_FILE:'/snapshot'},{FAAS_SECRETS_FILE:'/legacy'},{MIGRATION_DATABASE_URL:'private-migration'}]){
    assert.throws(()=>createDatabase(()=>assert.fail('must not construct a pool'),{
      env:{DATABASE_URL:managedURI('private-first'),...extras},
    }));
  }
});
