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
  GregaleOperations, customerOperationReceiptSchema, customerOperationRequestFromHeaders,
  customerOperationRequestDigest, withCustomerOperationTransaction, OperationMilestonePublicationError,
} from '../dist/index.js';

const dsn = process.env.DATABASE_URL;
const fixture = JSON.parse(await readFile(new URL('../../operation-tests/request-fixture.json', import.meta.url), 'utf8'));
const request = operationRequestFromHeaders(fixture.headers, fixture.method, fixture.path, Buffer.from(fixture.body_base64, 'base64'));
const customerHeaders = {
  'x-gregale-customer-operation-transaction-version': '1',
  'x-gregale-customer-operation-result-max-bytes': '262144',
  'x-gregale-customer-operation-id': request.operationId,
  'x-gregale-operation-attempt': '1', 'x-gregale-operation-capability': 'a'.repeat(64),
  'x-faas-invocation-id': 'eeeeeeef-5555-4555-8555-eeeeeeeeeeee',
  'x-faas-tenant-id': request.accountId, 'x-faas-app-id': request.appId,
  'x-faas-platform-tenant-id': request.platformTenantId,
};
const customerRequest = customerOperationRequestFromHeaders(customerHeaders, 'POST', '/orders/fulfill', request.body);

test('Customer transaction requires explicit trusted HTTP execution and customer ownership', async () => {
  for (const change of [
    { 'x-gregale-customer-operation-transaction-version': undefined },
    { 'x-gregale-customer-operation-transaction-version': '2' },
    { 'x-gregale-customer-operation-milestone-version': '2' },
    { 'x-gregale-customer-operation-result-max-bytes': '01' },
    { 'x-gregale-customer-operation-result-max-bytes': '1048577' },
    { 'x-gregale-customer-operation-id': [request.operationId, request.operationId] },
    { 'X-Gregale-Customer-Operation-Id': request.operationId },
    { 'x-gregale-operation-attempt': '0' },
    { 'x-gregale-operation-capability': 'forged' },
    { 'x-faas-invocation-id': '00000000-0000-0000-0000-000000000000' },
    { 'x-faas-app-id': 'forged' },
    { 'x-faas-platform-tenant-id': undefined },
    { 'x-gregale-operation-id': request.operationId },
    { 'x-gregale-operation-execution-kind': 'job' },
  ]) assert.throws(() => customerOperationRequestFromHeaders({ ...customerHeaders, ...change }, 'POST', '/orders/fulfill', request.body));
  assert.throws(() => customerOperationRequestFromHeaders(fixture.headers, fixture.method, fixture.path, request.body));
  const { [Object.getOwnPropertySymbols(customerRequest)[0]]: _brand, ...unbranded } = customerRequest;
  await assert.rejects(withCustomerOperationTransaction({ connect: () => { throw new Error('must not connect'); } }, unbranded, async () => ({})), /negotiated support/);
  const next = customerOperationRequestFromHeaders({ ...customerHeaders, 'x-gregale-operation-attempt': '2', 'x-faas-invocation-id': randomUUID(), 'x-gregale-operation-capability': 'b'.repeat(64) }, 'POST', '/orders/fulfill', request.body);
  assert.deepEqual(customerOperationRequestDigest(next), customerOperationRequestDigest(customerRequest));
  assert.notDeepEqual(customerOperationRequestDigest(customerRequest), operationRequestDigest({ ...request, path: customerRequest.path }));
});

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
  await pool.query(customerOperationReceiptSchema);
  await pool.query(customerOperationReceiptSchema);
  await pool.query('CREATE SCHEMA business; CREATE TABLE business.counter(id integer PRIMARY KEY,total integer NOT NULL); INSERT INTO business.counter VALUES(1,0); CREATE TABLE business.gregale_operation_inbox(LIKE public.gregale_operation_inbox INCLUDING ALL)');
  const counts = async () => (await pool.query('SELECT (SELECT total FROM business.counter WHERE id=1) AS total,(SELECT count(*)::int FROM public.gregale_operation_inbox) AS receipts')).rows[0];
  const customerCounts = async () => (await pool.query('SELECT (SELECT total FROM business.counter WHERE id=1) AS total,(SELECT count(*)::int FROM public.gregale_customer_operation_inbox) AS receipts')).rows[0];
  return { pool, counts, customerCounts, url: url.toString(), close };
}

test('Customer transaction rolls back invalid work, serializes duplicates, and fences receipt ownership', { skip: !dsn, timeout: 30000 }, async t => {
  const { pool, customerCounts, close } = await databaseFixture(); t.after(close);
  const runtime = new GregaleOperations({ apiURL: 'https://api.gregale.test', identityEndpoint: 'http://127.0.0.1/identity' });
  const httpRequest = { headers: customerHeaders, method: customerRequest.method, path: customerRequest.path, body: customerRequest.body };
  let callbacks = 0;
  const callback = async tx => {
    callbacks++;
    assert.equal(runtime.context()?.id, request.operationId);
    assert.ok(!JSON.stringify(runtime.context()).includes('capability'));
    await tx.query('UPDATE business.counter SET total=total+1 WHERE id=1');
    return { order_id: 123, label: 'fulfilled π <>&', status: 'fulfilled' };
  };
  await assert.rejects(runtime.transaction(httpRequest, pool, async tx => { await callback(tx); throw new Error('abort'); }), /abort/);
  for (const result of [undefined, NaN, 1n, { nested: undefined }, 'x'.repeat(customerRequest.resultMaxBytes)]) {
    await assert.rejects(runtime.transaction(httpRequest, pool, async tx => { await callback(tx); return result; }));
    assert.deepEqual(await customerCounts(), { total: 0, receipts: 0 });
  }
  callbacks = 0;
  const attempts = await Promise.all(Array.from({ length: 8 }, () => runtime.transaction(httpRequest, pool, callback)));
  assert.equal(callbacks, 1);
  assert.equal(attempts.filter(value => !value.replayed).length, 1);
  assert.ok(attempts.every(value => value.body === attempts[0].body));
  assert.deepEqual(JSON.parse(attempts[0].body), { order_id: 123, label: 'fulfilled π <>&', status: 'fulfilled' });
  assert.deepEqual(await customerCounts(), { total: 1, receipts: 1 });
  assert.equal(runtime.context(), undefined);
  const later = await runtime.transaction({ ...httpRequest, headers: { ...customerHeaders, 'x-faas-invocation-id': randomUUID(), 'x-gregale-operation-attempt': '2', 'x-gregale-operation-capability': 'b'.repeat(64) } }, pool, async () => { throw new Error('committed callback reran'); });
  assert.deepEqual(later, { body: attempts[0].body, replayed: true });
  for (const change of [
    { body: Buffer.from('{}') }, { path: '/other' }, { method: 'PUT' },
    ...['x-faas-platform-tenant-id', 'x-faas-app-id', 'x-faas-tenant-id'].map(name => ({ headers: { ...customerHeaders, [name]: randomUUID() } })),
  ]) await assert.rejects(runtime.transaction({ ...httpRequest, ...change }, pool, async () => { throw new Error('conflict reran callback'); }), OperationConflictError);
  assert.deepEqual(await customerCounts(), { total: 1, receipts: 1 });
  // Identical UUIDs in managed and customer namespaces cannot replay each other's receipts.
  const managed = await withOperationTransaction(pool, request, async () => ({ result: { managed: true } }));
  assert.equal(managed.replayed, false);
  assert.equal((await runtime.transaction(httpRequest, pool, async () => { throw new Error('namespace collision'); })).body, attempts[0].body);
  const connection = await pool.connect();
  const wrapped = { connect: async () => ({
    query: async (sql, values) => {
      const result = await connection.query(sql, values);
      if (sql === 'COMMIT') throw new Error('lost commit acknowledgement');
      return result;
    }, release: discard => connection.release(discard),
  }) };
  const unknown = { ...httpRequest, headers: { ...customerHeaders, 'x-gregale-customer-operation-id': randomUUID() } };
  await assert.rejects(runtime.transaction(unknown, wrapped, async tx => {
    await tx.query('UPDATE business.counter SET total=total+1 WHERE id=1'); return null;
  }), OperationCommitUnknownError);
  assert.deepEqual(await runtime.transaction(unknown, pool, async () => { throw new Error('unknown commit reran'); }), { body: 'null', replayed: true });
  assert.deepEqual(await customerCounts(), { total: 2, receipts: 2 });
});

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

test('Customer HTTP transaction survives process death and replays the original business result', { skip: !dsn, timeout: 40000 }, async t => {
  const { customerCounts, url, close } = await databaseFixture();
  const children = [];
  const stop = async child => {
    if (child.exitCode !== null || child.signalCode !== null) return;
    const exited = once(child, 'exit'); child.kill('SIGKILL'); await exited;
  };
  t.after(async () => { try { for (const child of children) await stop(child); } finally { await close(); } });
  const start = async mode => {
    const child = spawn(process.execPath, [new URL('fixtures/operation-server.mjs', import.meta.url).pathname], {
      stdio: ['ignore', 'ignore', 'pipe', 'ipc'],
      env: { ...process.env, OPERATION_DATABASE_URL: url, OPERATION_FAULT: mode, CUSTOMER_TRANSACTION: '1' },
    });
    children.push(child);
    const ready = await signal(child, 'ready');
    return { child, base: `http://127.0.0.1:${ready.port}` };
  };
  const send = (worker, headers = customerHeaders) => fetch(worker.base + customerRequest.path, { method: customerRequest.method, headers, body: customerRequest.body });
  let worker = await start('before-commit');
  const before = signal(worker.child, 'business-write');
  const first = send(worker).catch(() => null);
  await before; await stop(worker.child); await first;
  assert.deepEqual(await customerCounts(), { total: 0, receipts: 0 });
  worker = await start('after-commit');
  const committedSignal = signal(worker.child, 'committed');
  const second = send(worker).catch(() => null);
  const committed = await committedSignal;
  await stop(worker.child); await second;
  assert.deepEqual(await customerCounts(), { total: 1, receipts: 1 });
  worker = await start('normal');
  const recovered = await send(worker, { ...customerHeaders, 'x-faas-invocation-id': randomUUID(), 'x-gregale-operation-attempt': '2', 'x-gregale-operation-capability': 'b'.repeat(64) });
  assert.equal(recovered.status, 200); assert.equal(recovered.headers.get('x-replayed'), 'true');
  assert.equal(await recovered.text(), committed.body);
  assert.deepEqual(JSON.parse(committed.body), { order_id: 123, label: 'fulfilled π' });
  const foreign = await send(worker, { ...customerHeaders, 'x-faas-platform-tenant-id': randomUUID() });
  assert.equal(foreign.status, 409); assert.ok(!(await foreign.text()).includes('fulfilled π'));
  assert.deepEqual(await customerCounts(), { total: 1, receipts: 1 });
});

test('Milestones roll back invalid schemas and recover committed facts after a lost publication acknowledgement', {skip: !dsn, timeout: 30000}, async t => {
  const {pool, customerCounts, close} = await databaseFixture(); t.after(close);
  const headers = {...customerHeaders, 'x-gregale-customer-operation-milestone-version': '1'};
  const http = {headers, method: 'POST', path: '/orders/fulfill', body: request.body};
  const facts = new Map(); const publishes = []; let loseAcknowledgement = true;
  let savedTx;
  const runtime = new GregaleOperations({apiURL: 'http://127.0.0.1:1', identityEndpoint: 'http://127.0.0.1:1/identity', fetch: async (url, init) => {
    if (new URL(url).pathname === '/identity') return Response.json({access_token: 'current-workload-identity'});
    const body = JSON.parse(init.body);
    if (String(url).endsWith('/milestones/validate')) {
      if (body.milestones.some(m => m.name !== 'paid' || typeof m.payload.total !== 'number')) return Response.json({code:'operation_invalid'}, {status:422});
      if (body.milestones.some(m => m.payload.total === 13)) return Response.json({valid:false});
      return Response.json({valid:true});
    }
    publishes.push({body, headers: init.headers});
    if (!facts.has(body.id)) facts.set(body.id, {...body,operation_id:request.operationId,created_at:new Date().toISOString(),sequence:1});
    if (loseAcknowledgement) {loseAcknowledgement=false;throw new Error('response lost after platform persisted the fact');}
    return Response.json(facts.get(body.id));
  }});
  const outbox = async () => (await pool.query('SELECT id::text,payload,acknowledged_at FROM public.gregale_customer_operation_milestones')).rows;
  await assert.rejects(runtime.transaction(http, pool, async tx => {
    await tx.query('UPDATE business.counter SET total=total+1 WHERE id=1');
    tx.milestone('paid', {total:'invalid'});return {status:'paid'};
  }), e => e.status === 422);
  assert.deepEqual(await customerCounts(), {total:0,receipts:0});assert.equal((await outbox()).length,0);
  await assert.rejects(runtime.transaction(http, pool, async tx => {
    await tx.query('UPDATE business.counter SET total=total+1 WHERE id=1');
    tx.milestone('paid', {total:13});return {status:'paid'};
  }), /validation was not confirmed/);
  assert.deepEqual(await customerCounts(), {total:0,receipts:0});assert.equal((await outbox()).length,0);
  await assert.rejects(runtime.transaction({...http,headers:customerHeaders},pool,async tx => {
    await tx.query('UPDATE business.counter SET total=total+1 WHERE id=1');tx.milestone('paid',{total:1});return {};
  }), /not negotiated/);
  assert.deepEqual(await customerCounts(), {total:0,receipts:0});
  await assert.rejects(runtime.transaction(http,pool,async tx => {
    savedTx=tx;await tx.query('UPDATE business.counter SET total=total+1 WHERE id=1');
    const payload={total:1};tx.milestone('paid',payload);payload.total=99;
    return {status:'paid'};
  }), e => e instanceof OperationMilestonePublicationError && e.committed === true);
  assert.deepEqual(await customerCounts(),{total:1,receipts:1});
  const pending=await outbox();assert.equal(pending.length,1);assert.equal(pending[0].acknowledged_at,null);
  assert.deepEqual(JSON.parse(pending[0].payload),{total:1});
  assert.throws(() => savedTx.milestone('paid',{total:2}),/inside the transaction callback/);
  const resumed={...http,headers:{...headers,'x-faas-invocation-id':randomUUID(),'x-gregale-operation-attempt':'2','x-gregale-operation-capability':'b'.repeat(64)}};
  const receipt=await runtime.transaction(resumed,pool,async () => {throw new Error('committed business callback repeated');});
  assert.equal(receipt.replayed,true);assert.equal(receipt.body,'{"status":"paid"}');
  assert.deepEqual(await customerCounts(),{total:1,receipts:1});assert.equal(facts.size,1);assert.equal(publishes.length,2);
  assert.deepEqual(publishes[0].body,publishes[1].body);
  assert.equal(publishes[1].headers['X-Faas-Invocation-Id'],resumed.headers['x-faas-invocation-id']);
  assert.equal(publishes[1].headers['X-Gregale-Operation-Attempt'],'2');
  assert.ok((await outbox())[0].acknowledged_at);
  await runtime.transaction(resumed,pool,async () => {throw new Error('replay repeated');});assert.equal(publishes.length,2);
});
