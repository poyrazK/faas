import test from 'node:test';
import assert from 'node:assert/strict';
import { GregaleOperationSession } from '../src/operation-session.js';
import { GregaleOperationClient, type OperationSubmissionLookup } from '../src/customer-operations.js';
import { createBrowserOperationReceiptStore, OPERATION_BROWSER_RECEIPT_REPLAY_SECONDS, type SavedOperationSubmission } from '../src/operation-submission.js';

const account = '11111111-1111-4111-8111-111111111111';
const tenant = '22222222-2222-4222-8222-222222222222';
const app = '33333333-3333-4333-8333-333333333333';
const definition = '44444444-4444-4444-8444-444444444444';
const id = '55555555-5555-4555-8555-555555555555';
const receipt = { id, status_url: `/v1/platform-tenant-self/customer-operations/${id}`, events_url: `/v1/platform-tenant-self/customer-operations/${id}/events` };
const input = { count: 1, filter: { b: 'private-value', a: 2 } };

// Separate store instances share storage and a lock manager, like two tabs.
function browser() {
  const values = new Map<string, string>();
  const storage: Storage = {
    get length() { return values.size; }, clear() { values.clear(); }, key(index) { return [...values.keys()][index] ?? null; },
    getItem(key) { return values.get(key) ?? null; }, setItem(key, value) { values.set(key, value); }, removeItem(key) { values.delete(key); },
  };
  const pending = new Map<string, Promise<void>>();
  const locks = { request: async (key: string, action: () => Promise<unknown>) => {
    const previous = pending.get(key) ?? Promise.resolve();
    let release!: () => void;
    const next = new Promise<void>(resolve => { release = resolve; }); pending.set(key, next);
    await previous;
    try { return await action(); } finally { release(); if (pending.get(key) === next) pending.delete(key); }
  } } as unknown as Pick<LockManager, 'request'>;
  return { values, storage, locks, store: () => createBrowserOperationReceiptStore({ storage, locks }), saved: () => JSON.parse([...values.values()][0]!) as SavedOperationSubmission };
}

function fixture() {
  const disk = browser();
  let owner = tenant, credential = 'customer-secret', starts = 0, executions = 0, lookups = 0, reads = 0;
  let loseResponse = false, rejectRequest = false, unavailableStatus = false;
  let observation: OperationSubmissionLookup = { state: 'unresolved' };
  const keys: string[] = [], definitions: string[] = [];
  const snapshot = { id, name: 'export', state: 'succeeded', generation: 1, latest_sequence: 4, cancellation_requested: false,
    completion_delivery: { state: 'failed', attempts: 2 }, result: { csv: 'private-result' },
    created_at: new Date().toISOString(), updated_at: new Date().toISOString(), expires_at: new Date(Date.now() + 86400000).toISOString() };
  const client = (apiURL = 'https://api.example.com') => new GregaleOperationClient({ apiURL, credential: () => credential, fetch: async (url, init) => {
    const path = new URL(String(url)).pathname;
    assert.equal(new Headers(init?.headers).get('Authorization'), `Bearer ${credential}`);
    if (path.endsWith('/identity')) return Response.json({ account_id: account.replaceAll('-', ''), platform_tenant_id: owner });
    if (path.endsWith('/submissions/lookup')) {
      lookups++;
      const body = JSON.parse(String(init?.body));
      assert.deepEqual(body, { app_id: app, scope: 'default', name: 'export', idempotency_key: disk.saved().key, expected_identity: { account_id: account, platform_tenant_id: owner } });
      return Response.json(observation);
    }
    if (init?.method === 'POST') {
      starts++;
      const body = JSON.parse(String(init.body)), saved = disk.saved();
      // Publication must finish before any submission leaves the browser.
      assert.equal(saved.key, new Headers(init.headers).get('Idempotency-Key'));
      assert.deepEqual(body.expected_identity, { account_id: account, platform_tenant_id: owner });
      assert.deepEqual(body.expected_scope, { app_id: app, scope: 'default', name: 'export' });
      assert.equal(body.input.count, 1);
      keys.push(saved.key); definitions.push(body.definition_id);
      if (rejectRequest) return Response.json({ code: 'invalid_argument' }, { status: 422 });
      if (observation.state === 'unresolved') {
        executions++; observation = { state: 'accepted', receipt, accepted_at: new Date().toISOString() };
      }
      if (loseResponse) throw Error('response lost');
      return Response.json(receipt, { status: 202 });
    }
    if (path.endsWith('/events')) return new Response('', { headers: { 'Content-Type': 'text/event-stream' } });
    if (path.endsWith(`/${id}`)) { reads++; if (unavailableStatus) throw Error('status unavailable'); return Response.json(snapshot); }
    return Response.json({ operations: [snapshot] });
  } });
  const feature = (overrides = {}) => new GregaleOperationSession({ client: client(), receiptStore: disk.store(), appID: app, scope: 'default', name: 'export', definitionID: definition, ...overrides });
  return { disk, feature, client, keys, definitions, get starts() { return starts; }, get executions() { return executions; }, get lookups() { return lookups; }, get reads() { return reads; },
    set owner(value: string) { owner = value; }, set credential(value: string) { credential = value; },
    set loseResponse(value: boolean) { loseResponse = value; }, set rejectRequest(value: boolean) { rejectRequest = value; }, set unavailableStatus(value: boolean) { unavailableStatus = value; },
    set observation(value: OperationSubmissionLookup) { observation = value; } };
}

test('lost acceptance survives reload, fresh sign-in and concurrent tab resume with one execution', async () => {
  const f = fixture(); f.loseResponse = true;
  const original = f.feature(); await assert.rejects(original.start(input), /response lost/); original.close();
  assert.equal(f.disk.saved().acknowledgement, undefined);
  const serialized = [...f.disk.values.values()].join('');
  for (const secret of ['private-value', 'customer-secret', 'private-result', '"input"']) assert.ok(!serialized.includes(secret));
  f.credential = 'new-customer-secret'; f.loseResponse = false;
  const first = f.feature(), second = f.feature();
  const results = await Promise.all([first.resume(), second.resume()]);
  assert.deepEqual(results.map(result => result.receipt?.id), [id, id]);
  assert.equal(first.snapshot?.state, 'succeeded');
  assert.equal(first.snapshot?.completion_delivery.state, 'failed');
  assert.equal(f.disk.saved().acknowledgement?.id, id);
  assert.equal(f.starts, 1); assert.equal(f.executions, 1); assert.equal(f.lookups, 2);
  first.close(); second.close();
});

test('two simultaneous submissions in different tabs share a published identity', async () => {
  const f = fixture(), first = f.feature(), second = f.feature();
  const accepted = await Promise.all([first.start(input), second.start({ filter: { a: 2, b: 'private-value' }, count: 1 })]);
  assert.deepEqual(accepted.map(value => value.id), [id, id]); assert.equal(f.starts, 1); assert.equal(f.executions, 1);
  first.close(); second.close();
});

test('unresolved restore never submits and explicit matching retry freezes definition across rollout', async () => {
  const f = fixture(); f.loseResponse = true;
  const first = f.feature(); await assert.rejects(first.start(input)); first.close(); f.observation = { state: 'unresolved' };
  const next = f.feature({ definitionID: app });
  assert.equal((await next.resume()).state, 'unresolved'); assert.equal(f.starts, 1);
  await assert.rejects(next.start({ count: 2 }), /same input/); assert.equal(f.starts, 1);
  f.loseResponse = false;
  await next.start(input);
  assert.equal(f.keys[0], f.keys[1]); assert.deepEqual(f.definitions, [definition, definition]); next.close();
});

test('an acceptance acknowledgement survives unavailable status and prevents silent identity replacement', async () => {
  const f = fixture(); f.unavailableStatus = true;
  const first = f.feature(); await first.start(input); first.close();
  const next = f.feature(); assert.equal((await next.resume()).state, 'accepted'); assert.equal(f.starts, 1);
  f.observation = { state: 'accepted', accepted_at: new Date().toISOString(), receipt: { ...receipt, id: app, status_url: `/v1/platform-tenant-self/customer-operations/${app}`, events_url: `/v1/platform-tenant-self/customer-operations/${app}/events` } };
  await assert.rejects(next.resume(), /identity was replaced/); assert.equal(f.starts, 1); next.close();
});

test('another authenticated customer cannot read or reuse a saved identity', async () => {
  const f = fixture(); f.loseResponse = true;
  const first = f.feature(); await assert.rejects(first.start(input)); first.close();
  f.owner = app;
  const foreign = f.feature(); assert.equal((await foreign.resume()).state, 'empty'); assert.equal(f.lookups, 0); assert.equal(f.starts, 1); foreign.close();
  f.owner = tenant;
  const returned = f.feature(); assert.equal((await returned.resume()).receipt?.id, id); assert.equal(f.starts, 1); returned.close();
});

test('receipt storage separates API, application, environment and operation name', async () => {
  const f = fixture(); f.loseResponse = true;
  const first = f.feature(); await assert.rejects(first.start(input)); first.close();
  for (const options of [{ client: f.client('https://other-api.example.com') }, { appID: definition }, { scope: 'staging' }, { name: 'another-export' }]) {
    const other = f.feature(options); assert.equal((await other.resume()).state, 'empty'); other.close();
  }
  assert.equal(f.lookups, 0); assert.equal(f.starts, 1); assert.equal(f.disk.values.size, 1);
});

test('a customer change during preflight fails before sending the submission', async () => {
  const f = fixture(), client = f.client(); let verified = 0;
  client.identity = async () => ({ account_id: account, platform_tenant_id: ++verified === 1 ? tenant : app });
  const feature = f.feature({ client });
  await assert.rejects(feature.start(input), /customer changed/);
  assert.equal(f.starts, 0); assert.equal(f.disk.values.size, 1); feature.close();
});

test('an existing session cannot start new work after its verified customer changes', async () => {
  const f = fixture(), feature = f.feature();
  await feature.start(input); f.owner = app;
  await assert.rejects(feature.start(input), /customer changed/);
  await assert.rejects(feature.resume(), /customer changed/);
  assert.equal(f.starts, 1); assert.equal(f.disk.values.size, 1); feature.close();
});

test('customer rotation between identity verification and submission sends a server principal fence', async () => {
  const f = fixture(), client = f.client(); let starts = 0;
  const original = client.start.bind(client);
  client.start = async (...args) => {
    starts++; f.owner = app;
    assert.deepEqual(args[4]?.identity, { account_id: account, platform_tenant_id: tenant });
    // A real server rejects the expected identity against its current bearer.
    throw new Error('operation_identity_conflict');
  };
  const feature = f.feature({ client });
  await assert.rejects(feature.start(input), /operation_identity_conflict/);
  assert.equal(starts, 1); assert.equal(f.disk.values.size, 1);
  client.start = original; feature.close();
});

test('storage failures and corrupt receipts stop before lookup or submission', async () => {
  const f = fixture();
  f.disk.storage.setItem = () => { throw Error('storage denied'); };
  const denied = f.feature(); await assert.rejects(denied.start(input), /storage denied/); assert.equal(f.starts, 0); denied.close();
  const g = fixture(); g.loseResponse = true;
  const first = g.feature(); await assert.rejects(first.start(input)); first.close();
  const key = [...g.disk.values.keys()][0]!;
  g.disk.values.set(key, JSON.stringify({ ...g.disk.saved(), scope: { ...g.disk.saved().scope, tenantID: app } }));
  const corrupt = g.feature(); await assert.rejects(corrupt.resume(), /another customer/); assert.equal(g.lookups, 0); assert.equal(g.starts, 1); corrupt.close();
});

test('unsupported HTTP keys cannot publish an unresolvable receipt', async () => {
  const f = fixture(), feature = f.feature();
  for (const key of [' padded-key ', 'unicode-🙂', 'unicode-é', '', 'x'.repeat(129)]) {
    await assert.rejects(feature.start(input, key), /idempotency/);
    assert.equal(f.disk.values.size, 0); assert.equal(f.starts, 0);
  }
  await feature.start(input, 'supported-key'); assert.equal(f.disk.saved().key, 'supported-key'); feature.close();
});

test('expired pending identity is preserved without automatic or explicit re-admission', async () => {
  const f = fixture(); f.loseResponse = true;
  const first = f.feature(); await assert.rejects(first.start(input)); first.close();
  const key = [...f.disk.values.keys()][0]!, saved = f.disk.saved();
  const ago = Date.now() - (OPERATION_BROWSER_RECEIPT_REPLAY_SECONDS + 60) * 1000;
  f.disk.values.set(key, JSON.stringify({ ...saved, createdAt: new Date(ago).toISOString(), replayNotAfter: new Date(ago + OPERATION_BROWSER_RECEIPT_REPLAY_SECONDS * 1000).toISOString() }));
  f.observation = { state: 'unresolved' };
  const next = f.feature(); assert.equal((await next.resume()).state, 'unresolved');
  await assert.rejects(next.start(input), /receipt expired/); assert.equal(f.starts, 1); assert.equal(f.disk.values.size, 1);
  f.observation = { state: 'expired' }; await assert.rejects(next.resume(), /submission has expired/); assert.equal(f.starts, 1); next.close();
});

test('resume beyond the local retry deadline accepts only an original retained acceptance', async () => {
  const f = fixture(); f.loseResponse = true;
  const first = f.feature(); await assert.rejects(first.start(input)); first.close();
  const key = [...f.disk.values.keys()][0]!, saved = f.disk.saved();
  const ago = Date.now() - (OPERATION_BROWSER_RECEIPT_REPLAY_SECONDS + 60) * 1000;
  f.disk.values.set(key, JSON.stringify({ ...saved, createdAt: new Date(ago).toISOString(), replayNotAfter: new Date(ago + OPERATION_BROWSER_RECEIPT_REPLAY_SECONDS * 1000).toISOString() }));
  f.observation = { state: 'accepted', receipt, accepted_at: new Date().toISOString() };
  const next = f.feature(); await assert.rejects(next.resume(), /replaced operation/); assert.equal(f.starts, 1);
  f.observation = { state: 'accepted', receipt, accepted_at: new Date(ago + 1000).toISOString() };
  assert.equal((await next.resume()).receipt?.id, id); assert.equal(f.starts, 1); next.close();
});

test('unavailable locks and storage that ignores writes cannot submit', async () => {
  for (const store of [createBrowserOperationReceiptStore({ storage: browser().storage, locks: {} as LockManager }),
    createBrowserOperationReceiptStore({ storage: { ...browser().storage, setItem() {} }, locks: browser().locks })]) {
    const f = fixture(), feature = f.feature({ receiptStore: store });
    await assert.rejects(feature.start(input), /Web Locks|saved durably/); assert.equal(f.starts, 0); feature.close();
  }
});

test('fresh confirmed rejection frees the slot, while a previously uncertain request stays saved', async () => {
  const f = fixture(); f.rejectRequest = true;
  const rejected = f.feature(); await assert.rejects(rejected.start(input)); assert.equal(f.disk.values.size, 0); rejected.close();
  const g = fixture(); g.loseResponse = true;
  const first = g.feature(); await assert.rejects(first.start(input)); first.close(); g.observation = { state: 'unresolved' }; g.rejectRequest = true;
  const retry = g.feature(); await assert.rejects(retry.start(input)); assert.equal(g.disk.values.size, 1); retry.close();
});

test('an accepted response is saved when signout happens during transport', async () => {
  const f = fixture(), client = f.client();
  let finish!: (value: typeof receipt) => void, entered!: () => void;
  const waiting = new Promise<void>(resolve => { entered = resolve; });
  client.start = async () => { entered(); return new Promise(resolve => { finish = resolve; }); };
  const first = f.feature({ client }), submitting = first.start(input);
  await waiting; first.close(); finish(receipt);
  await assert.rejects(submitting, /Session closed/); assert.equal(f.disk.saved().acknowledgement?.id, id); assert.equal(f.reads, 0);
});
