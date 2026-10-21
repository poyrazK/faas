import assert from 'node:assert/strict';
import {EventEmitter} from 'node:events';
import {test} from 'node:test';
import {inspect} from 'node:util';
import {createPostgresPool, postgresPoolConfig, PostgresPoolError,
  type RestoreAwarePostgresPoolOptions, type PostgresQueryResult} from '../src/postgres-pool-runtime.js';

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: Error) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return {promise, resolve, reject};
}
const tick = () => new Promise<void>(resolve => setImmediate(resolve));

test('generated function registration precedes pool creation and survives a timed-out close until actual drainage', async t => {
  const key = Symbol.for('gregale.function.postgres.lifecycle.v1');
  const held = new Set<unknown>();
  Object.defineProperty(globalThis, key, {configurable: true, value: {version: 1, register(resource: unknown) {
    held.add(resource); return () => held.delete(resource);
  }}});
  t.after(() => { Reflect.deleteProperty(globalThis, key); });
  const drained = deferred<void>();
  const db = createPostgresPool(() => {
    assert.equal(held.size, 1);
    const pool = new Pool(); pool.finish = () => drained.promise; return pool;
  }, {lifecycleTimeoutMs: 25});
  assert.ok(held.has(db));
  await assert.rejects(db.close(), /database_unavailable/);
  assert.ok(held.has(db));
  drained.resolve(); await tick();
  assert.equal(held.size, 0);
  await assert.rejects(db.query('SELECT 1'), /database_unavailable/);
});

test('invalid or full function registries refuse before driver side effects and sanitize bridge failures', async t => {
  const key = Symbol.for('gregale.function.postgres.lifecycle.v1');
  t.after(() => { Reflect.deleteProperty(globalThis, key); });
  let opened = 0;
  for (const bridge of [{version: 9}, {version: 1, register() { throw new Error('private-fixture-value'); }}]) {
    Object.defineProperty(globalThis, key, {configurable: true, value: bridge});
    assert.throws(() => createPostgresPool(() => { opened++; return new Pool(); }), error =>
      error instanceof PostgresPoolError && error.message === 'database_unavailable');
  }
  assert.equal(opened, 0);
});
class Client extends EventEmitter {
  readonly queries: {text: string; values: unknown[]}[] = [];
  readonly releases: boolean[] = [];
  constructor(readonly execute: (text: string) => Promise<PostgresQueryResult> = async () => ({rows: []})) { super(); }
  async query(text: string, values: unknown[] = []): Promise<PostgresQueryResult> {
    this.queries.push({text, values}); return this.execute(text);
  }
  release(destroy = false): void { this.releases.push(destroy); }
}
class Pool extends EventEmitter {
  readonly clients: Client[] = [];
  ended = 0;
  acquire: (() => Promise<Client>) | undefined;
  finish: (() => Promise<void>) | undefined;
  execute: ((text: string) => Promise<PostgresQueryResult>) | undefined;
  async connect(): Promise<Client> {
    if (this.acquire) return this.acquire();
    const client = new Client(this.execute); this.clients.push(client); return client;
  }
  async end(): Promise<void> { this.ended += 1; await this.finish?.(); }
}
function fixture(options: RestoreAwarePostgresPoolOptions = {}, configure: (pool: Pool, index: number) => void = () => {}) {
  const pools: Pool[] = [];
  const db = createPostgresPool(() => {
    const pool = new Pool(); configure(pool, pools.length); pools.push(pool); return pool;
  }, options);
  return {db, pools};
}

test('checkpoint cancels unfinished writes, drains once and gates until a fresh read probe succeeds', async () => {
  const blocked = deferred<PostgresQueryResult>();
  const {db, pools} = fixture({readinessQuery: {text: 'SELECT id FROM existing_table LIMIT 1'}});
  pools[0]!.execute = async text => text === 'INSERT fixture' ? blocked.promise : {rows: []};
  const work = db.query('INSERT fixture', [], {write: true});
  const cancelled = assert.rejects(work, /database_cancelled/);
  await tick();
  const checkpoint = db.beforeCheckpoint();
  assert.equal(db.beforeCheckpoint(), checkpoint);
  await assert.rejects(db.query('SELECT 1'), /database_unavailable/);
  await checkpoint; await cancelled;
  assert.equal(db.diagnostics.state, 'checkpointed');
  assert.equal(db.diagnostics.openPools, 0);
  assert.deepEqual(pools[0]!.clients[0]!.releases, [true]);
  blocked.resolve({rows: []}); await tick();
  assert.ok(!pools[0]!.clients[0]!.queries.some(q => q.text === 'COMMIT'));
  await db.beforeCheckpoint(); assert.equal(pools.length, 1);
  await assert.rejects(db.query('SELECT 1'), /database_unavailable/);
  const restore = db.afterRestore(); assert.equal(db.afterRestore(), restore); await restore;
  assert.equal(db.generation, 1);
  assert.deepEqual(pools[1]!.clients[0]!.queries.map(q => q.text), [
    'BEGIN READ ONLY', "SELECT set_config('statement_timeout', $1, true)", 'SELECT id FROM existing_table LIMIT 1', 'COMMIT',
  ]);
  await db.close(); assert.ok(pools.every(pool => pool.ended === 1));
});

test('thaw replaces retained sessions without a checkpoint and re-evaluates the pool factory', async () => {
  let credential = 'old'; const observed: string[] = [];
  const db = createPostgresPool(() => { observed.push(credential); return new Pool(); });
  await db.query('SELECT 1'); credential = 'rotated';
  await db.afterRestore(); await db.afterRestore();
  assert.deepEqual(observed, ['old', 'rotated', 'rotated']);
  assert.equal(db.generation, 2); assert.equal(db.diagnostics.openPools, 1);
  await db.close();
});

test('a client acquired after cancellation is destroyed and never executes SQL', async () => {
  const acquisition = deferred<Client>(); const {db, pools} = fixture();
  pools[0]!.acquire = () => acquisition.promise;
  const controller = new AbortController();
  const cancelled = assert.rejects(db.query('INSERT fixture', [], {write: true, signal: controller.signal}), /database_cancelled/);
  controller.abort(); await cancelled;
  const late = new Client(); acquisition.resolve(late); await tick();
  assert.deepEqual(late.releases, [true]); assert.deepEqual(late.queries, []);
  assert.equal(late.listenerCount('error'), 0); await db.close();
});

test('slow draining fails checkpoint within its budget and cannot accumulate replacement pools', async () => {
  const drained = deferred<void>(); const {db, pools} = fixture({lifecycleTimeoutMs: 25});
  pools[0]!.finish = () => drained.promise;
  await assert.rejects(db.beforeCheckpoint(), /database_unavailable/);
  assert.equal(db.diagnostics.state, 'unavailable');
  await assert.rejects(db.query('SELECT 1'), /database_unavailable/);
  await assert.rejects(db.afterRestore(), /database_unavailable/);
  assert.equal(pools.length, 1); assert.equal(pools[0]!.ended, 1);
  drained.resolve(); await db.afterRestore(); assert.equal(db.generation, 1); await db.close();
});

test('a late readiness result never publishes a timed-out pool; a subsequent explicit restore can recover', async () => {
  const probe = deferred<PostgresQueryResult>();
  const {db, pools} = fixture({lifecycleTimeoutMs: 25}, (pool, index) => {
    if (index === 1) pool.execute = async text => text === 'SELECT 1' ? probe.promise : {rows: []};
  });
  await assert.rejects(db.afterRestore(), /database_unavailable/);
  assert.equal(db.diagnostics.state, 'unavailable'); assert.equal(db.generation, 0);
  probe.resolve({rows: []}); await tick();
  assert.deepEqual(pools[1]!.clients[0]!.releases, [true]);
  assert.ok(!pools[1]!.clients[0]!.queries.some(q => q.text === 'COMMIT'));
  await assert.rejects(db.query('SELECT 1'), /database_unavailable/);
  await db.afterRestore(); assert.equal(db.generation, 1); await db.close();
});

test('shutdown cancels a readiness probe and stays closed after late completion', async () => {
  const probe = deferred<PostgresQueryResult>(), entered = deferred<void>();
  const {db} = fixture({}, (pool, index) => {
    if (index === 1) pool.execute = async () => { entered.resolve(); return probe.promise; };
  });
  const failed = assert.rejects(db.afterRestore(), /database_unavailable/); await entered.promise;
  const closing = db.close(); assert.equal(db.close(), closing); await closing; await failed;
  probe.resolve({rows: []}); await tick();
  assert.equal(db.diagnostics.state, 'closed'); assert.equal(db.generation, 0);
  await assert.rejects(db.afterRestore(), /database_unavailable/);
  await assert.rejects(db.beforeCheckpoint(), /database_unavailable/);
  await assert.rejects(db.query('SELECT 1'), /database_unavailable/);
});

test('concurrent hook kinds cannot overlap; cancelling a duplicate waiter leaves its owner running', async () => {
  const drained = deferred<void>(); const {db, pools} = fixture(); pools[0]!.finish = () => drained.promise;
  const owner = db.afterRestore(), waiter = new AbortController();
  const cancelled = assert.rejects(db.afterRestore({signal: waiter.signal}), /database_cancelled/);
  waiter.abort(); await cancelled;
  await assert.rejects(db.beforeCheckpoint(), /database_busy/);
  drained.resolve(); await owner; assert.equal(db.generation, 1); await db.close();
});

test('pool creation and disposal failures are sanitized, close is bounded, and pools cannot be reused', async () => {
  assert.throws(() => createPostgresPool(() => { throw new Error('postgres://private:password@provider'); }), PostgresPoolError);
  const shared = new Pool(), reused = createPostgresPool(() => shared);
  await assert.rejects(reused.afterRestore(), /database_unavailable/);
  assert.equal(shared.ended, 1); await reused.close();
  const events: unknown[] = [];
  const {db, pools} = fixture({onEvent: event => { events.push(event); }});
  pools[0]!.finish = async () => { throw new Error('private-provider-close-error'); };
  await assert.rejects(db.beforeCheckpoint(), /database_unavailable/);
  await assert.rejects(db.close(), /database_unavailable/);
  assert.equal(pools[0]!.ended, 1); assert.ok(!JSON.stringify(events).includes('private'));
  const hung = fixture({lifecycleTimeoutMs: 25}); hung.pools[0]!.finish = () => new Promise(() => {});
  const closing = hung.db.close(); assert.equal(hung.db.close(), closing);
  await assert.rejects(closing, /database_unavailable/);
});

test('a lost COMMIT response is reported once and never replays the mutation', async () => {
  let writes = 0, commits = 0; const {db, pools} = fixture();
  pools[0]!.execute = async text => {
    if (text === 'INSERT fixture') writes += 1;
    if (text === 'COMMIT') { commits += 1; throw new Error('private uncertain commit'); }
    return {rows: []};
  };
  await assert.rejects(db.query('INSERT fixture', [], {write: true}), /database_unavailable/);
  assert.equal(writes, 1); assert.equal(commits, 1);
  assert.deepEqual(pools[0]!.clients[0]!.releases, [true]);
  await db.afterRestore(); assert.equal(writes, 1); assert.equal(commits, 1); await db.close();
});

test('admission and query deadlines bound unavailable SQL, and observers cannot break cleanup', async () => {
  const events: unknown[] = [];
  const {db, pools} = fixture({timeoutMs: 25, maxInFlight: 2, onEvent: async event => {
    events.push(event); throw new Error('private observer failure');
  }});
  pools[0]!.emit('error', new Error('private idle password'));
  pools[0]!.execute = () => new Promise(() => {});
  const requests = Array.from({length: 2}, () => assert.rejects(db.query('SELECT 1'), /database_cancelled/));
  await assert.rejects(db.query('SELECT 1'), /database_busy/);
  await Promise.all(requests); assert.equal(db.diagnostics.activeRequests, 0);
  assert.ok(pools[0]!.clients.every(client => client.releases.length === 1 && client.releases[0]));
  assert.ok(!JSON.stringify(events).includes('private')); assert.ok(!inspect(db).includes('private'));
  await db.close();
});

test('TLS verification cannot be overridden by URL options; invalid budgets fail before creating resources', () => {
  for (const mode of ['require', 'verify-ca', 'verify-full']) {
    const config = postgresPoolConfig(`postgres://user:private@db.example/db?sslmode=${mode}`);
    assert.deepEqual(config.ssl, {rejectUnauthorized: true});
    assert.equal(new URL(config.connectionString).searchParams.has('sslmode'), false);
  }
  assert.equal(postgresPoolConfig('postgres://localhost/test?sslmode=disable').ssl, false);
  for (const url of ['private-invalid-url', 'https://private@db.example',
    'postgres://db/db?sslmode=prefer', 'postgres://db/db?ssl=0', 'postgres://db/db?sslrootcert=/private/key']) {
    assert.throws(() => postgresPoolConfig(url), error => error instanceof PostgresPoolError && !String(error).includes('private'));
  }
  let calls = 0;
  for (const options of [{timeoutMs: NaN}, {timeoutMs: 0}, {timeoutMs: 2 ** 31}, {lifecycleTimeoutMs: 1501},
    {lifecycleTimeoutMs: -1}, {maxInFlight: 1.5}, {readinessQuery: {text: ''}}]) {
    assert.throws(() => createPostgresPool(() => { calls += 1; return new Pool(); }, options), PostgresPoolError);
  }
  assert.equal(calls, 0);
});
