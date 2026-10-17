import assert from 'node:assert/strict';
import {test} from 'node:test';
import {inspect} from 'node:util';
import {PostgresPoolBinding, PostgresPoolBindingError, type PostgresPoolConfig} from '../src/postgres-pool-runtime.js';
import {SecretReloadCoordinator} from '../src/secret-reload-runtime.js';

const uri = (password = 'private-first') => `postgresql://worker:${password}@db.example:6432/app?sslmode=require`;
const values = (password?: string) => ({DATABASE_URL: uri(password)});
const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};
class Pool {closed = 0; constructor(readonly dsn: string) {} close(): void {this.closed++;}}
const binding = (validate: (pool: Pool, signal?: AbortSignal) => void | Promise<void> = () => {}) =>
  new PostgresPoolBinding('DATABASE_URL', config => new Pool(config.connectionString), {validate, retire: pool => pool.close()});

test('validation gates publication and transactions retain old pools until their final lease', async () => {
  const entered = deferred(), finish = deferred();
  const b = binding(async pool => {if (pool.dsn.includes('private-second')) {entered.resolve(); await finish.promise;}});
  await b.reload(values());
  const first = b.acquire(), second = b.acquire();
  const old = first.current();
  const reload = b.reload(values('private-second'));
  await entered.promise;
  assert.throws(() => b.acquire(), /not ready/);
  assert.equal(first.current(), old); assert.equal(old.closed, 0);
  finish.resolve(); await reload;
  const next = b.acquire(), current = next.current();
  assert.notEqual(current, old);
  const copied = {...first}; first.release(); copied.release();
  assert.throws(() => copied.current(), /released/);
  assert.equal(old.closed, 0);
  b.invalidate(); assert.throws(() => b.acquire());
  await assert.rejects(b.waitForRetirement(AbortSignal.abort()), /cancelled/);
  assert.equal(current.closed, 0);
  second.release(); next.release();
  await b.waitForRetirement();
  assert.equal(old.closed, 1); assert.equal(current.closed, 1);
});

test('failed validation retires candidates, hides driver errors and permits later recovery', async () => {
  const created: Pool[] = [];
  let config!: PostgresPoolConfig;
  const b = new PostgresPoolBinding('DATABASE_URL', input => {config = input; const pool = new Pool(input.connectionString); created.push(pool); return pool;}, {
    validate: pool => {if (pool.dsn.includes('private-bad')) throw new Error(pool.dsn);}, retire: pool => pool.close(),
  });
  await b.reload(values());
  const old = b.acquire();
  await assert.rejects(b.reload(values('private-bad')), error => error instanceof PostgresPoolBindingError && !String(error).includes('private-'));
  assert.throws(() => b.acquire());
  assert.ok(!inspect(config).includes('private-')); assert.ok(!JSON.stringify(config).includes('private-'));
  assert.equal({...config}.connectionString, uri('private-bad'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(created[0]!.closed, 0); assert.equal(created[1]!.closed, 1);
  old.release(); await b.waitForRetirement();
  await b.reload(values('private-recovered'));
  const recovered = b.acquire(); assert.equal(recovered.current().dsn, uri('private-recovered'));
  assert.ok(!JSON.stringify(recovered).includes('private-')); assert.ok(!inspect(recovered).includes('private-'));
  recovered.release(); b.invalidate(); await b.waitForRetirement();
});

test('invalidation and cancellation fence late validation and queued reloads', async () => {
  for (const mode of ['invalidate', 'cancel']) {
    const entered = deferred(), finish = deferred(), cancel = new AbortController();
    let calls = 0; const pools: Pool[] = [];
    const b = new PostgresPoolBinding('DATABASE_URL', config => {calls++; const p = new Pool(config.connectionString); pools.push(p); return p;}, {
      validate: async () => {entered.resolve(); await finish.promise;}, retire: pool => pool.close(),
    });
    const reload = b.reload(values(), cancel.signal); await entered.promise;
    const queued = mode === 'invalidate' ? b.reload(values()) : undefined;
    if (mode === 'invalidate') b.invalidate(); else cancel.abort();
    assert.throws(() => b.acquire());
    finish.resolve(); await assert.rejects(reload, /cancelled/);
    if (queued) await assert.rejects(queued, /cancelled/);
    await b.waitForRetirement(); assert.equal(calls, 1); assert.equal(pools[0]!.closed, 1);
  }
});

test('a tracked alias is rejected before validation without retiring its borrower', async () => {
  const pool = new Pool(uri()); let checks = 0;
  const b = new PostgresPoolBinding('DATABASE_URL', () => pool, {validate: () => {checks++;}, retire: p => p.close()});
  await b.reload(values()); const lease = b.acquire();
  await assert.rejects(b.reload(values()), /reused an owned pool/);
  assert.equal(checks, 1); assert.equal(pool.closed, 0); assert.equal(lease.current(), pool);
  lease.release(); await b.waitForRetirement(); assert.equal(pool.closed, 1);
});

test('URI identity is explicit; invalid snapshots never invoke the factory', async () => {
  const invalid = ['\v' + uri(), 'postgres://worker:%1f@db:5432/app', 'postgres://worker:secret@db/app', 'postgres://worker:secret@%2Ftmp:5432/app', uri() + '&database=other', '', 'host=db user=worker', 'https://worker:secret@db/app', 'postgres://db/app', 'postgres://worker@db/app',
    'postgres://worker:secret@db', 'postgres://worker:secret@db/', 'postgres://worker:secret@db:0/app',
    'postgres://worker:secret@db:65536/app', uri() + '#fragment', uri() + '#', uri() + '&UsEr=other', uri() + '&host=other',
    'postgres://worker:%00@db/app', 'postgres://worker:%ZZ@db/app', uri() + '\n'];
  let calls = 0;
  const b = new PostgresPoolBinding('DATABASE_URL', () => {calls++; return new Pool('unused');}, {validate: () => {}, retire: p => p.close()});
  for (const value of invalid) {await assert.rejects(b.reload({DATABASE_URL: value}), PostgresPoolBindingError); assert.throws(() => b.acquire());}
  assert.equal(calls, 0);
  for (const value of [uri(), 'postgres://work%20er:p%40ss@[::1]:5432/app%2Fdb?sslmode=disable']) {
    await b.reload({DATABASE_URL: value}); b.invalidate(); await b.waitForRetirement();
  }
  assert.throws(() => new PostgresPoolBinding('invalid-key', () => new Pool('unused'), {validate: () => {}, retire: () => {}}));
  assert.throws(() => new PostgresPoolBinding('DATABASE_URL', () => new Pool('unused'), {} as never));
});

test('retirement failures stay sanitized and are never retried', async () => {
  let calls = 0; const observed = deferred();
  const b = new PostgresPoolBinding('DATABASE_URL', () => new Pool(uri()), {validate: () => {},
    retire: () => {calls++; throw new Error('private-cleanup');},
    onRetireError: error => {assert.ok(error instanceof PostgresPoolBindingError); assert.ok(!String(error).includes('private-')); observed.resolve(); throw new Error('private-observer');},
  });
  await b.reload(values()); b.invalidate();
  await assert.rejects(b.waitForRetirement(), /pool retirement failed/); await observed.promise;
  await assert.rejects(b.waitForRetirement(), /pool retirement failed/); assert.equal(calls, 1);
});

test('a boolean validation result cannot publish a pool', async () => {
  const pool = new Pool(uri());
  const b = new PostgresPoolBinding('DATABASE_URL', () => pool, {
    validate: (() => false) as unknown as () => void, retire: p => p.close(),
  });
  await assert.rejects(b.reload(values()), /validation failed/);
  assert.throws(() => b.acquire());
  await b.waitForRetirement(); assert.equal(pool.closed, 1);
});

test('coordinated applied ACK follows validation; a later consumer failure invalidates the adopted pool', async t => {
  const entered = deferred(), finish = deferred(); let fail = false, checks = 0;
  const b = binding(async () => {checks++; if (checks === 1) {entered.resolve(); await finish.promise;}});
  let raw = JSON.stringify({revision: 'a'.repeat(64), secrets: values()}); const receipts: string[] = [];
  const coordinator = new SecretReloadCoordinator({consumers: [b.secretConsumer(), {
    apply: () => {if (fail) throw new Error('private-other-consumer');}, invalidate: () => {},
  }], env: {FAAS_SECRETS_SNAPSHOT_FILE: '/snapshot', FAAS_SECRETS_RELOAD_READY_FILE: '/ready',
    FAAS_SECRETS_RELOAD_ACK_ENDPOINT: 'http://metadata.test/ack', FAAS_SECRETS_RELOAD_GENERATION: 'e'.repeat(32)},
    io: {read: async () => raw, ready: async () => {}, fetch: async (_input, init) => {
      receipts.push(JSON.parse(String(init?.body)).status); return new Response(null, {status: 202});
    }},
  });
  t.after(() => coordinator.stop());
  const start = coordinator.start(); await entered.promise;
  assert.deepEqual(receipts, []); assert.throws(() => b.acquire()); finish.resolve(); await start;
  assert.deepEqual(receipts, ['applied']); const old = b.acquire(), pool = old.current();
  fail = true; raw = JSON.stringify({revision: 'b'.repeat(64), secrets: values('private-second')});
  await assert.rejects(coordinator.reload()); assert.throws(() => b.acquire());
  assert.equal(pool.closed, 0); assert.deepEqual(receipts, ['applied', 'failed']);
  old.release(); coordinator.stop(); await b.waitForRetirement(); assert.equal(pool.closed, 1);
});
