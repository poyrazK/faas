import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import pg from 'pg';
import {
  operationReceiptSchema, operationRequestFromHeaders, operationRequestDigest,
  withOperationTransaction, OperationConflictError, OperationCommitUnknownError,
} from '../dist/index.js';

const dsn = process.env.DATABASE_URL;
const fixture = JSON.parse(await readFile(new URL('../../operation-tests/request-fixture.json', import.meta.url), 'utf8'));
const request = operationRequestFromHeaders(fixture.headers, fixture.method, fixture.path, Buffer.from(fixture.body_base64, 'base64'));

test('Operation request contract rejects forged/ambiguous context and preserves generation identity', () => {
  assert.equal(Buffer.from(operationRequestDigest(request)).toString('hex'), fixture.digest);
  assert.deepEqual(operationRequestDigest({ ...request, generation: '2' }), operationRequestDigest(request));
  for (const change of [
    { 'x-gregale-operation-result-version': '2' },
    { 'x-gregale-operation-generation': '9223372036854775808' },
    { 'x-gregale-operation-generation': '01' },
    { 'x-gregale-operation-id': [request.operationId, request.operationId] },
    { 'x-faas-app-id': '00000000-0000-0000-0000-000000000000' },
  ]) {
    assert.throws(() => operationRequestFromHeaders({ ...fixture.headers, ...change }, fixture.method, fixture.path, request.body), TypeError);
  }
  assert.throws(() => operationRequestFromHeaders({}, 'POST', '/', Buffer.alloc(0)), TypeError);
});

async function databaseFixture() {
  const admin = new pg.Client({ connectionString: dsn });
  await admin.connect();
  const name = `operation_node_${randomUUID().replaceAll('-', '')}`;
  await admin.query(`CREATE DATABASE "${name}" TEMPLATE template0 ENCODING 'UTF8'`);
  const url = new URL(dsn); url.pathname = `/${name}`;
  const pool = new pg.Pool({ connectionString: url.toString(), max: 12 });
  const close = async () => {
    await pool.end();
    try {
      const deadline = Date.now() + 10000;
      while (true) {
        const active = Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE datname=$1', [name])).rows[0].count);
        if (active === 0) break;
        if (Date.now() >= deadline) throw new Error(`database ${name} still has ${active} active sessions`);
        await new Promise(resolve => setTimeout(resolve, 25));
      }
      await admin.query(`DROP DATABASE "${name}"`);
    }
    finally { await admin.end(); }
  };
  await pool.query(operationReceiptSchema);
  await pool.query(operationReceiptSchema); // Explicit installation is replay safe.
  await pool.query('CREATE SCHEMA business; CREATE TABLE business.counter(id integer PRIMARY KEY,total integer NOT NULL); INSERT INTO business.counter VALUES(1,0); CREATE TABLE business.gregale_operation_inbox(LIKE public.gregale_operation_inbox INCLUDING ALL)');
  const counts = async () => (await pool.query('SELECT (SELECT total FROM business.counter WHERE id=1) AS total,(SELECT count(*)::int FROM public.gregale_operation_inbox) AS receipts')).rows[0];
  return { pool, counts, url: url.toString(), close };
}

test('Operation transaction rolls back, suppresses concurrent duplicates, and replays immutable effects', { skip: !dsn, timeout: 30000 }, async t => {
  const { pool, counts, close } = await databaseFixture();
  t.after(close);
  const outcome = { result: { value: 'π <>&' }, effects: [{ name: 'notify', webhook_id: randomUUID(), type: 'order.fulfilled', payload: { order_id: 123 } }] };
  const callback = async tx => { await tx.query('UPDATE business.counter SET total=total+1 WHERE id=1'); return outcome; };
  await assert.rejects(withOperationTransaction(pool, request, async tx => { await callback(tx); throw new Error('abort'); }), /abort/);
  assert.deepEqual(await counts(), { total: 0, receipts: 0 });
  for (const bad of [
    { result: NaN },
    { result: 1n },
    { result: 'x'.repeat(1048576) },
    { ...outcome, effects: [outcome.effects[0], outcome.effects[0]] },
    { ...outcome, effects: [{ ...outcome.effects[0], payload: 'x'.repeat(65536) }] },
  ]) {
    await assert.rejects(withOperationTransaction(pool, request, async tx => { await callback(tx); return bad; }));
    assert.deepEqual(await counts(), { total: 0, receipts: 0 });
  }
  const attempts = await Promise.all(Array.from({ length: 8 }, () => withOperationTransaction(pool, request, callback)));
  assert.equal(attempts.filter(value => !value.replayed).length, 1);
  assert.ok(attempts.every(value => value.body === attempts[0].body));
  assert.deepEqual(await counts(), { total: 1, receipts: 1 });
  const later = await withOperationTransaction(pool, { ...request, generation: '2' }, () => { throw new Error('committed callback reran'); });
  assert.equal(later.replayed, true);
  assert.equal(later.body, attempts[0].body);
  for (const change of [
    { body: Buffer.from('{}') }, { path: '/other' }, { method: 'PUT' },
    { platformTenantId: randomUUID() }, { platformTenantId: undefined }, { appId: randomUUID() }, { accountId: randomUUID() },
  ]) {
    await assert.rejects(withOperationTransaction(pool, { ...request, ...change }, callback), OperationConflictError);
  }
  assert.deepEqual(await counts(), { total: 1, receipts: 1 });
  const connection = await pool.connect();
  const wrapped = { connect: async () => ({
    query: async (sql, values) => {
      const result = await connection.query(sql, values);
      if (sql === 'COMMIT') throw new Error('lost commit acknowledgement');
      return result;
    },
    release: discard => connection.release(discard),
  }) };
  const ambiguous = { ...request, operationId: randomUUID() };
  await assert.rejects(withOperationTransaction(wrapped, ambiguous, callback), OperationCommitUnknownError);
  const recovered = await withOperationTransaction(pool, ambiguous, () => { throw new Error('ambiguous commit duplicated mutation'); });
  assert.equal(recovered.replayed, true);
  assert.deepEqual(await counts(), { total: 2, receipts: 2 });
  assert.equal((await pool.query('SELECT count(*)::int AS n FROM business.gregale_operation_inbox')).rows[0].n, 0);
  // The installed CHECK rejects incomplete envelopes, even outside the SDK.
  await assert.rejects(pool.query('INSERT INTO public.gregale_operation_inbox(operation_id,account_id,app_id,request_digest,response_body) VALUES ($1,$2,$3,$4,$5)', [randomUUID(), request.accountId, request.appId, Buffer.alloc(32), '{}']), { code: '23514' });
});

function signal(child, phase) {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => finish(new Error(`worker did not reach ${phase}`)), 10000);
    const message = value => { if (value.phase === phase) finish(null, value); };
    const exit = () => finish(new Error(`worker exited before ${phase}`));
    const finish = (error, value) => {
      clearTimeout(timeout); child.off('message', message); child.off('exit', exit);
      error ? reject(error) : resolve(value);
    };
    child.on('message', message); child.on('exit', exit);
  });
}

test('HTTP handler process death before and after commit recovers without duplicate business writes', { skip: !dsn, timeout: 40000 }, async t => {
  const children = [];
  const stop = async child => {
    if (child.exitCode !== null || child.signalCode !== null) return;
    const exited = once(child, 'exit'); child.kill('SIGKILL'); await exited;
  };
  const { counts, url, close } = await databaseFixture();
  t.after(async () => {
    try { for (const child of children) await stop(child); }
    finally { await close(); }
  });
  const start = async mode => {
    const child = spawn(process.execPath, [new URL('fixtures/operation-server.mjs', import.meta.url).pathname], {
      stdio: ['ignore', 'ignore', 'pipe', 'ipc'],
      env: { ...process.env, OPERATION_DATABASE_URL: url, OPERATION_FAULT: mode, OPERATION_WEBHOOK_ID: 'cccbbbaa-3333-4333-8333-cccccccccccc' },
    });
    children.push(child);
    const ready = await signal(child, 'ready');
    return { child, base: `http://127.0.0.1:${ready.port}` };
  };
  const headers = { ...fixture.headers, 'content-type': 'application/json' };
  let worker = await start('before-commit');
  const beforeSignal = signal(worker.child, 'business-write');
  const first = fetch(worker.base + fixture.path, { method: fixture.method, headers, body: request.body }).catch(() => null);
  await beforeSignal; await stop(worker.child); await first;
  assert.deepEqual(await counts(), { total: 0, receipts: 0 });
  worker = await start('after-commit');
  const committedSignal = signal(worker.child, 'committed');
  const second = fetch(worker.base + fixture.path, { method: fixture.method, headers, body: request.body }).catch(() => null);
  const committed = await committedSignal;
  assert.deepEqual(await counts(), { total: 1, receipts: 1 });
  await stop(worker.child); await second;
  worker = await start('normal');
  const retry = await fetch(worker.base + fixture.path, { method: fixture.method, headers: { ...headers, 'x-gregale-operation-generation': '2' }, body: request.body });
  assert.equal(retry.status, 200);
  assert.equal(retry.headers.get('x-replayed'), 'true');
  assert.equal(await retry.text(), committed.body);
  assert.deepEqual(JSON.parse(committed.body).effects[0], { name: 'notify', webhook_id: 'cccbbbaa-3333-4333-8333-cccccccccccc', type: 'order.fulfilled', payload: { order_id: 123 } });
  assert.deepEqual(await counts(), { total: 1, receipts: 1 });
});
