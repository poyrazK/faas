import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { JobContract, type JobContractSpec } from '../src/job-runtime.js';
import { createEventPipeline, EventContract, eventPipelineIdentity, eventPipelineSchema, parseEventPipelineReceipt,
  postgresEventPipelineStore, type EventContractSpec, type EventPipelineDefinition, type EventPipelineDelivery,
  type EventPipelineReceipt, type EventPipelineStore } from '../src/event-pipelines.js';
import { generateEventTypes } from '../src/event-types.js';
import { eventConsumerRequestFromHeaders } from '../src/event-consumer-transactions.js';
import type { PipelineScope } from '../src/pipeline-state.js';
const fixture = JSON.parse(readFileSync(new URL('../../../request-contracts/event-pipelines.json', import.meta.url), 'utf8'));
const scope = fixture.scope as PipelineScope;
type Data = { owner_id: string; n: number; label?: string; Input?: string };
const eventContract = new EventContract<Data>(fixture.event_contract as EventContractSpec);
const contract = new JobContract<{ n: number }, { n: number }>(fixture.pipeline_contract as JobContractSpec);
test('shared event type boundaries preserve native-length and Unicode names', () => {
  for (const type of fixture.event_types.valid) assert.equal(new EventContract({ ...fixture.event_contract, type }).spec.type, type);
  for (const type of [...fixture.event_types.invalid, '\ud800']) assert.throws(() => new EventContract({ ...fixture.event_contract, type }), { code: 'invalid_contract' });
});
const options = { timeoutMs: 1000 };
const delivery = (body = fixture.event_body, headers = {}): EventPipelineDelivery => ({ body: Buffer.from(body), headers: {
  authorization: 'Bearer ingress', 'x-gregale-event-subscription-id': fixture.subscription_id,
  'x-gregale-event-acceptance-id': fixture.acceptance_id, 'x-gregale-event-source': 'urn:imports',
  'x-gregale-event-id': 'import-17', ...headers,
} });
function memory() {
  const rows = new Map<string, EventPipelineReceipt>();
  const state = { reserveCalls: 0, loads: 0, loseReserve: false, loseComplete: false };
  const store: EventPipelineStore = {
    async load(id) { state.loads++; return rows.get(id) ?? null; },
    async reserve(intent) {
      state.reserveCalls++; const old = rows.get(intent.identity);
      if (old) return { created: false, receipt: old };
      const receipt = parseEventPipelineReceipt({ intent, checkpoint: null }); rows.set(intent.identity, receipt);
      if (state.loseReserve) throw Error('private database diagnostics');
      return { created: true, receipt };
    },
    async complete(intent, checkpoint) {
      const receipt = parseEventPipelineReceipt({ intent, checkpoint }); rows.set(intent.identity, receipt);
      if (state.loseComplete) throw Error('private database diagnostics'); return receipt;
    },
  };
  return { rows, store, state };
}
function native() {
  const state = { posts: 0, loseStart: false, wrongAccount: false };
  const records = new Map<string, any>();
  const keys = new Map<string, string>();
  const fetch: typeof globalThis.fetch = async (url, init) => {
    assert.equal(new Headers(init?.headers).get('authorization'), 'Bearer platform-backend');
    const path = new URL(String(url)).pathname;
    if (path === '/v1/account') return Response.json({ id: state.wrongAccount ? randomUUID() : scope.account_id });
    if (path === '/v1/apps/imports') return Response.json({ id: scope.app_id });
    if (init?.method === 'POST') {
      state.posts++;
      const key = new Headers(init.headers).get('idempotency-key')!;
      const id = keys.get(key) ?? (records.size === 0 ? fixture.receipt.checkpoint.run_id : randomUUID());
      if (!records.has(id)) records.set(id, { id, app_id: scope.app_id, workflow_name: contract.spec.name, status: 'running', input: JSON.parse(String(init.body)) });
      keys.set(key, id);
      if (state.loseStart) throw Error('lost reply with sensitive backend details');
      return Response.json(records.get(id), { status: 201 });
    }
    const record = records.get(path.split('/').at(-1)!);
    return record ? Response.json(record) : Response.json({}, { status: 404 });
  };
  return { state, records, keys, fetch };
}
function setup(overrides: Partial<EventPipelineDefinition<Data, { n: number }, { n: number }, string>> = {}) {
  const db = memory(), api = native();
  const definition: EventPipelineDefinition<Data, { n: number }, { n: number }, string> = {
    name: fixture.name, scope, subscriptionId: fixture.subscription_id, source: 'urn:imports', contract, store: db.store,
    token: 'platform-backend', fetch: api.fetch,
    verify: async (d) => {
      if (d.headers.authorization !== 'Bearer ingress') throw Error('secret signature diagnostics');
      return eventConsumerRequestFromHeaders(d.headers, d.body, scope.account_id, scope.app_id);
    },
    authorize: async (event, user) => user === 'alice' && event.data.owner_id === user ? user : null,
    map: async event => ({ n: event.data.n }), ...overrides,
  };
  return { db, api, definition, pipeline: createEventPipeline(eventContract, definition) };
}
test('shared Node/Python contracts, decimal identity, provenance, schema and exact start receipt', async () => {
  const { pipeline, api } = setup();
  assert.deepEqual(await pipeline.inspect(delivery(), 'alice', options), null);
  const result = await pipeline.handle(delivery(), 'alice', options);
  assert.equal(result.state, 'started'); assert.equal(result.replayed, false);
  assert.deepEqual(result.receipt, fixture.receipt);
  assert.deepEqual(api.records.get(fixture.receipt.checkpoint.run_id).input, fixture.envelope);
  assert.equal(api.state.posts, 1);
  assert.equal(eventPipelineSchema, readFileSync(new URL('../../../request-contracts/event-pipelines.sql', import.meta.url), 'utf8'));
  assert.equal(eventPipelineIdentity(scope, fixture.name, fixture.subscription_id, '9007199254740993'), fixture.receipt.intent.identity);
  assert.notEqual(eventPipelineIdentity(scope, fixture.name, fixture.subscription_id, '9007199254740992'), fixture.receipt.intent.identity);
  for (const language of ['typescript', 'python'] as const) assert.equal(generateEventTypes(fixture.event_contract, language), fixture.generated_types[language]);
  assert.equal(JSON.stringify(result.receipt).includes('café'), false);
  assert.equal(JSON.stringify(result.receipt).includes('platform-backend'), false);
});
test('concurrent duplicate deliveries reserve once and fresh adapters reuse terminal receipts', async () => {
  const { pipeline, api, definition } = setup();
  const results = await Promise.all(Array.from({ length: 24 }, () => pipeline.handle(delivery(), 'alice', options)));
  assert.equal(api.state.posts, 1); assert.equal(results.filter(r => !r.replayed).length, 1);
  const fresh = createEventPipeline(eventContract, definition);
  assert.equal((await fresh.handle(delivery(), 'alice', options)).state, 'started');
  assert.deepEqual((await fresh.inspect(delivery(), 'alice', options))!.receipt, fixture.receipt);
  assert.equal(api.state.posts, 1);
});
test('lost reserve/start/completion replies do not authorize another start, even without native retained keys', async () => {
  for (const stage of ['reserve', 'start', 'complete']) {
    const { pipeline, db, api, definition } = setup();
    if (stage === 'reserve') db.state.loseReserve = true;
    if (stage === 'start') api.state.loseStart = true;
    if (stage === 'complete') db.state.loseComplete = true;
    if (stage === 'start') assert.equal((await pipeline.handle(delivery(), 'alice', options)).state, 'start_unknown');
    else await assert.rejects(pipeline.handle(delivery(), 'alice', options), { code: 'store_failed' });
    db.state.loseReserve = db.state.loseComplete = api.state.loseStart = false;
    api.keys.clear(); // Native retention is not our deduplication boundary.
    const fresh = createEventPipeline(eventContract, definition);
    const repeated = await fresh.handle(delivery(), 'alice', options);
    assert.equal(repeated.state, stage === 'complete' ? 'started' : 'start_unknown');
    assert.equal(api.state.posts, stage === 'reserve' ? 0 : 1);
    if (stage === 'start') {
      const adopted = await fresh.reconcile(delivery(), fixture.receipt.checkpoint.run_id, 'alice', options);
      assert.deepEqual(adopted.receipt, fixture.receipt); assert.equal(api.state.posts, 1);
    }
  }
});
test('current authorization runs before receipt access and includes duplicate, inspection and reconciliation actions', async () => {
  const actions: string[] = []; let revoked = false;
  const s = setup({ authorize: async (event, user, action) => { actions.push(action); return !revoked && user === event.data.owner_id ? user : null; } });
  await assert.rejects(s.pipeline.handle(delivery(), 'bob', options), { code: 'unauthorized' });
  assert.equal(s.db.state.reserveCalls, 0); assert.equal(s.api.state.posts, 0);
  await s.pipeline.handle(delivery(), 'alice', options); revoked = true;
  for (const action of [() => s.pipeline.handle(delivery(), 'alice', options), () => s.pipeline.inspect(delivery(), 'alice', options), () => s.pipeline.reconcile(delivery(), fixture.receipt.checkpoint.run_id, 'alice', options)]) await assert.rejects(action(), { code: 'unauthorized' });
  assert.equal(s.db.state.loads, 0); assert.equal(s.db.state.reserveCalls, 1); assert.equal(s.api.state.posts, 1);
  assert.ok(actions.includes('inspect') && actions.includes('reconcile'));
  const forged = delivery(fixture.event_body, { authorization: 'Bearer attacker' });
  await assert.rejects(s.pipeline.handle(forged, 'alice', options), error => String(error).includes('unauthorized') && !String(error).includes('secret signature'));
});
test('same acceptance rejects changed bytes, mapping, owner, event contract and native run binding', async () => {
  const s = setup(); await s.pipeline.handle(delivery(), 'alice', options);
  for (const body of [fixture.event_body + ' ', fixture.event_body.replace('"n":1', '"n":2')]) await assert.rejects(s.pipeline.handle(delivery(body), 'alice', options), { code: 'receipt_conflict' });
  const remapped = createEventPipeline(eventContract, { ...s.definition, map: async () => ({ n: 2 }) });
  await assert.rejects(remapped.handle(delivery(), 'alice', options), { code: 'receipt_conflict' });
  const bob = createEventPipeline(eventContract, { ...s.definition, authorize: async () => 'bob' });
  await assert.rejects(bob.inspect(delivery(), 'bob', options), { code: 'receipt_conflict' });
  const changed = new EventContract<Data>({ ...fixture.event_contract, data: { ...fixture.event_contract.data, required: ['owner_id', 'n', 'label'] } });
  await assert.rejects(createEventPipeline(changed, s.definition).handle(delivery(), 'alice', options), { code: 'receipt_conflict' });
  assert.equal(s.api.state.posts, 1);
});
test('recovery refuses unrelated same-input runs, wrong owner/contract/trigger/input and foreign run IDs', async () => {
  const s = setup(); s.api.state.loseStart = true;
  await s.pipeline.handle(delivery(), 'alice', options);
  const id = fixture.receipt.checkpoint.run_id, original = structuredClone(s.api.records.get(id));
  for (const modify of [
    (r: any) => { delete r.input.trigger; }, (r: any) => { r.input.trigger.identity = '0'.repeat(64); },
    (r: any) => { r.input.owner_id = 'bob'; }, (r: any) => { r.input.input.n = 2; },
    (r: any) => { r.input.contract_sha256 = '0'.repeat(64); }, (r: any) => { r.workflow_name = 'foreign-pipeline'; },
    (r: any) => { r.id = randomUUID(); },
  ]) {
    const wrong = structuredClone(original); modify(wrong); s.api.records.set(id, wrong);
    await assert.rejects(s.pipeline.reconcile(delivery(), id, 'alice', options), { code: 'receipt_conflict' });
  }
  s.api.records.set(id, original);
  assert.equal((await s.pipeline.reconcile(delivery(), id, 'alice', options)).state, 'started');
  assert.equal(s.api.state.posts, 1);
});
test('invalid delivery, context and payload fail before durable admission or native mutation', async () => {
  for (const [body, headers] of [
    [fixture.event_body, { 'x-gregale-event-acceptance-id': '01' }],
    [fixture.event_body, { 'x-gregale-event-acceptance-id': '9223372036854775808' }],
    [fixture.event_body, { 'X-Gregale-Event-Id': 'import-17' }],
    [fixture.event_body, { 'x-gregale-event-subscription-id': randomUUID() }],
    [fixture.event_body.replace('urn:imports', 'urn:foreign'), { 'x-gregale-event-source': 'urn:foreign' }],
    [fixture.event_body.replace('import.requested', 'import.other'), {}],
    [fixture.event_body.replace('"n":1', '"n":"bad"'), {}],
    ['{', {}], ['x'.repeat(1024 * 1024 + 1), {}],
  ] as const) {
    const s = setup(); await assert.rejects(async () => s.pipeline.handle(delivery(body, headers), 'alice', options));
    assert.equal(s.db.state.reserveCalls, 0); assert.equal(s.api.state.posts, 0);
  }
  const s = setup(); s.api.state.wrongAccount = true;
  await assert.rejects(s.pipeline.handle(delivery(), 'alice', options), { code: 'scope_mismatch' }); assert.equal(s.api.state.posts, 0);
});
test('deadlines and cancellation fence late verifier and reservation callbacks before dispatch', async () => {
  for (const phase of ['verify', 'reserve']) for (const mode of ['deadline', 'cancelled']) {
    const s = setup(); let release!: () => void;
    const gate = new Promise<void>(r => { release = r; });
    const definition = { ...s.definition };
    if (phase === 'verify') definition.verify = async (d, signal) => { await gate; return s.definition.verify(d, signal); };
    else definition.store = { ...s.db.store, reserve: async (intent, signal) => { const r = await s.db.store.reserve(intent, signal); await gate; return r; } };
    const pipeline = createEventPipeline(eventContract, definition), abort = new AbortController();
    const pending = pipeline.handle(delivery(), 'alice', { timeoutMs: mode === 'deadline' ? 10 : 1000, signal: abort.signal });
    if (mode === 'cancelled') { await new Promise(r => setImmediate(r)); abort.abort(); }
    await assert.rejects(pending, { code: mode }); release(); await new Promise(r => setTimeout(r, 10));
    assert.equal(s.api.state.posts, 0);
  }
});
test('delivery bytes are captured before an asynchronous verifier can observe caller mutation', async () => {
  let release!: () => void;
  const gate = new Promise<void>(r => { release = r; }); const s = setup();
  const pipeline = createEventPipeline(eventContract, { ...s.definition, verify: async (d, signal) => { await gate; return s.definition.verify(d, signal); } });
  const d = delivery(), pending = pipeline.handle(d, 'alice', options);
  d.body.fill(0); release();
  assert.deepEqual((await pending).receipt, fixture.receipt);
});
test('deadline after native acceptance leaves uncertainty and never settles from a late reply', async () => {
  const s = setup(); let release!: () => void, accepted!: () => void;
  const gate = new Promise<void>(r => { release = r; }), sent = new Promise<void>(r => { accepted = r; });
  const pipeline = createEventPipeline(eventContract, { ...s.definition, fetch: async (url, init) => {
    const response = await s.api.fetch(url, init);
    if (init?.method === 'POST') { accepted(); await gate; }
    return response;
  } });
  const pending = pipeline.handle(delivery(), 'alice', { timeoutMs: 500 });
  await sent; await assert.rejects(pending, { code: 'deadline' }); release();
  await new Promise(r => setTimeout(r, 20));
  assert.equal(s.api.state.posts, 1);
  assert.equal([...s.db.rows.values()][0]!.checkpoint, null);
  assert.equal((await pipeline.handle(delivery(), 'alice', options)).state, 'start_unknown');
  assert.equal((await pipeline.reconcile(delivery(), fixture.receipt.checkpoint.run_id, 'alice', options)).state, 'started');
  assert.equal(s.api.state.posts, 1);
});
test('PostgreSQL adapter uses conditional immutable settlement and rejects missing/conflicting rows', async () => {
  let old: EventPipelineReceipt | null = null; const sqls: string[] = [];
  const store = postgresEventPipelineStore(async (sql, values) => {
    sqls.push(sql);
    if (sql.startsWith('INSERT')) { if (old) return { rows: [] }; old = parseEventPipelineReceipt(JSON.parse(String(values[1]))); }
    if (sql.startsWith('UPDATE')) old = parseEventPipelineReceipt({ intent: old!.intent, checkpoint: JSON.parse(String(values[2])) });
    return { rows: old ? [{ receipt: old }] : [] };
  });
  const signal = new AbortController().signal;
  assert.equal(await store.load(fixture.receipt.intent.identity, signal), null);
  assert.equal((await store.reserve(fixture.receipt.intent, signal)).created, true);
  assert.equal((await store.reserve(fixture.receipt.intent, signal)).created, false);
  assert.deepEqual(await store.complete(fixture.receipt.intent, fixture.receipt.checkpoint, signal), fixture.receipt);
  assert.ok(sqls.some(sql => sql.includes("receipt->'intent'=$2::jsonb") && sql.includes("receipt->'checkpoint'='null'::jsonb")));
  assert.equal(sqls.some(sql => /CREATE|DELETE|BEGIN|COMMIT/.test(sql)), false);
  assert.throws(() => parseEventPipelineReceipt({ ...fixture.receipt, extra: true }), { code: 'receipt_conflict' });
  await assert.rejects(store.complete(fixture.receipt.intent, { ...fixture.receipt.checkpoint, scope: undefined }, signal), { code: 'receipt_conflict' });
  // @ts-expect-error runtime callers cannot settle with an absent checkpoint
  await assert.rejects(store.complete(fixture.receipt.intent, null, signal), { code: 'receipt_conflict' });
});
