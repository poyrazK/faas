import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { JobContract, jobValueHash } from '../src/job-runtime.js';
import { defineJobPipeline, type PipelineJob } from '../src/job-pipelines.js';
import { pipelineGateIntent, parsePipelineGateRecord, createPipelineGateService, nativePipelineGateTransport, pipelineGateSchema, postgresPipelineGateStore, type PipelineGateStore, type PipelineGateRecord, type PipelineGateIntent, type PipelineGateOutcome } from '../src/pipeline-gates.js';
import type { PipelineJobStore, PipelineScope } from '../src/pipeline-state.js';
import { parseLiveJobSnapshot } from '../src/live-jobs.js';
import { WORKFLOW_CODE_PROTOCOL, runWorkflowInvocation, type JournalEntry, type WorkflowInvocationResponse } from '../src/workflows.js';

const fixture = JSON.parse(readFileSync(new URL('../../../request-contracts/pipeline-gates.json', import.meta.url), 'utf8'));
const scope: PipelineScope = fixture.scope;
const root = new JobContract<{ n: number }, { n: number }>(fixture.root_contract);
const contract = new JobContract<{ sha256: string; rows: number; label: string }, { reason: string }>(fixture.contract);
const intent = fixture.intent as PipelineGateIntent;
const signal = new AbortController().signal, settings = { timeoutMs: 1000 };

function memory() {
  const rows = new Map<string, PipelineGateRecord>(); let clock = Date.parse('2026-11-15T12:00:00.000Z');
  const resolve = async (id: string) => {
    let r = rows.get(id);
    if (r && !r.decision && Date.parse(r.expires_at) <= clock) {
      r = parsePipelineGateRecord({ ...r, decision: { state: 'expired', actor_id: null, payload: null, decided_at: new Date(clock).toISOString() } }); rows.set(id, r);
    }
    return r ?? null;
  };
  const store: PipelineGateStore = {
    async open(i) {
      const old = await resolve(i.id);
      if (old) { if (jobValueHash(old.intent) !== jobValueHash(i)) throw new Error('changed'); return old; }
      const r = parsePipelineGateRecord({ intent: i, expires_at: new Date(clock + i.timeout_ms).toISOString(), decision: null, delivery: 'pending' }); rows.set(i.id, r); return r;
    }, resolve,
    async decide(id, binding, proposal) {
      const r = await resolve(id); if (!r || r.intent.binding_sha256 !== binding) throw new Error();
      // resolve is async; take the current row again at the CAS point.
      const current = rows.get(id)!;
      if (current.decision) return current;
      const saved = parsePipelineGateRecord({ ...current, decision: { ...proposal, decided_at: new Date(clock).toISOString() } }); rows.set(id, saved); return saved;
    },
    async mark(id, decision, delivery) { const r = rows.get(id)!; assert.deepEqual(r.decision, decision); rows.set(id, parsePipelineGateRecord({ ...r, delivery })); },
    async pending(limit, s, pipeline, c) { return [...rows.values()].filter(r => r.delivery === 'pending' && jobValueHash(r.intent.scope) === jobValueHash(s) && r.intent.pipeline_sha256 === pipeline && r.intent.contract_sha256 === c).slice(0, limit); },
    async list(runId, owner) { return [...rows.values()].filter(r => r.intent.workflow_run_id === runId && r.intent.owner_id === owner); },
  };
  return { store, rows, advance: (ms: number) => { clock += ms; } };
}
function setup(state = memory()) {
  let active = true, loseAck = false, role = true;
  const events: unknown[] = [], authorize = async (_r: PipelineGateRecord, _action: string, user: string) => ({ actor_id: user, owner_id: user === 'carol' ? 'alice' : user, roles: role ? ['reviewer'] : [] });
  const transport = { async active() { return active; }, async publish(i: PipelineGateIntent) { events.push({ key: 'pipeline-gate:' + i.id, binding: i.binding_sha256 }); if (loseAck) throw new Error('lost native ack'); } };
  const service = () => createPipelineGateService({ scope, pipelineSha256: root.sha256, contract, store: state.store, transport, authorize });
  return { ...state, events, service, get active() { return active; }, set active(v: boolean) { active = v; }, get loseAck() { return loseAck; }, set loseAck(v: boolean) { loseAck = v; }, get role() { return role; }, set role(v: boolean) { role = v; } };
}
const request = (state: 'approved' | 'rejected' | 'submitted' | 'cancelled' = 'approved') => ({ binding_sha256: intent.binding_sha256, state, payload: { reason: 'checked' } });
function pipeline(store: PipelineGateStore, handler?: (outcome: PipelineGateOutcome<{ reason: string }>) => Promise<{ n: number }>) {
  return defineJobPipeline(root, { scope, store: {} as PipelineJobStore, gateStore: store, jobs: {} }, async (_input, wf) => {
    const outcome = await wf.approval('review', contract, fixture.intent.review, { timeoutMs: 60000, requiredRoles: ['reviewer'] });
    return handler ? handler(outcome) : { n: outcome.state === 'approved' ? 1 : 0 };
  });
}
function invoke(p: ReturnType<typeof pipeline>, journal: JournalEntry[] = []) {
  return runWorkflowInvocation(p.definition, { protocol: WORKFLOW_CODE_PROTOCOL, workflow: root.spec.name, run_id: intent.workflow_run_id, input: fixture.envelope, invocation: journal.length + 1, journal });
}
function commit(journal: JournalEntry[], reply: WorkflowInvocationResponse) {
  for (const op of reply.ops) journal[op.seq] = { ...op, status: op.kind === 'event' ? 'succeeded' : op.status!, attempt: 1 };
}

test('shared Node/Python review binding, SQL schema and journal replay', async () => {
  const { version: _v, id: _id, binding_sha256: _b, review_sha256: _r, ...base } = intent;
  assert.deepEqual(pipelineGateIntent(base), intent);
  assert.deepEqual(parsePipelineGateRecord(fixture.record), fixture.record);
  assert.equal(pipelineGateSchema, readFileSync(new URL('../../../request-contracts/pipeline-gates.sql', import.meta.url), 'utf8'));
  const reply = await invoke(pipeline(memory().store), fixture.journal);
  assert.equal(reply.result.status, 'succeeded');
  assert.deepEqual((reply.result as { output: { output: unknown } }).output.output, { n: 1 });
});
test('lost open and resolution journals replay from durable state; early events cannot approve', async () => {
  const state = setup(), journal: JournalEntry[] = [];
  const first = await invoke(pipeline(state.store)); assert.equal(first.ops[0]?.status, 'succeeded');
  const again = await invoke(pipeline(state.store)); assert.deepEqual(again.ops, first.ops); commit(journal, again);
  commit(journal, await invoke(pipeline(state.store), journal)); // forged/early wake
  commit(journal, await invoke(pipeline(state.store), journal)); // authoritative null
  const waiting = await invoke(pipeline(state.store), journal); assert.equal(waiting.ops[0]?.kind, 'event');
  await state.service().decide(intent.id, request(), 'alice', settings);
  commit(journal, waiting);
  const resolved = await invoke(pipeline(state.store), journal); assert.equal(resolved.ops[0]?.status, 'succeeded');
  const retry = await invoke(pipeline(state.store), journal); assert.deepEqual(retry.ops, resolved.ops); commit(journal, retry);
  assert.equal((await invoke(pipeline(state.store), journal)).result.status, 'succeeded');
  assert.equal(state.rows.size, 1);
});
test('current owner and reviewer membership are required, including on duplicate retries', async () => {
  const state = setup(); await state.store.open(intent, signal);
  await assert.rejects(state.service().inspect(intent.id, 'bob', settings), { code: 'forbidden' });
  await assert.rejects(state.service().decide(intent.id, request(), 'bob', settings), { code: 'forbidden' });
  state.role = false;
  assert.equal((await state.service().inspect(intent.id, 'alice', settings)).can_decide, false);
  await assert.rejects(state.service().decide(intent.id, request(), 'alice', settings), { code: 'forbidden' });
  state.role = true;
  assert.equal((await state.service().decide(intent.id, request(), 'carol', settings)).decision?.actor_id, 'carol');
  state.role = false;
  await assert.rejects(state.service().decide(intent.id, request(), 'carol', settings), { code: 'forbidden' });
});
test('opposing concurrent decisions have one winner; identical retries preserve the original audit', async () => {
  const state = setup(); await state.store.open(intent, signal);
  const choices = await Promise.allSettled([state.service().decide(intent.id, request(), 'alice', settings), state.service().decide(intent.id, request('rejected'), 'alice', settings)]);
  assert.equal(choices.filter(c => c.status === 'fulfilled').length, 1);
  const first = await state.service().decide(intent.id, request(), 'alice', settings);
  state.advance(1000);
  assert.deepEqual((await state.service().decide(intent.id, request(), 'alice', settings)).decision, first.decision);
  await assert.rejects(state.service().decide(intent.id, { ...request(), binding_sha256: '0'.repeat(64) }, 'alice', settings), { code: 'conflict' });
  await assert.rejects(state.service().decide(intent.id, request(), 'carol', settings), { code: 'conflict' });
});
test('uncertain delivery survives a new service; repeated native keys/content stay identical', async () => {
  const state = setup(); await state.store.open(intent, signal); state.loseAck = true;
  assert.equal((await state.service().decide(intent.id, request(), 'alice', settings)).delivery, 'pending');
  state.loseAck = false;
  assert.equal(await state.service().drain(10, settings), 1);
  assert.deepEqual(state.events[0], state.events[1]);
  assert.equal(state.rows.get(intent.id)?.delivery, 'delivered');
});
test('expiry is enforced at commit after slow authorization; inactive parents close without publishing', async () => {
  const state = setup(); await state.store.open(intent, signal);
  const service = createPipelineGateService({ scope, pipelineSha256: root.sha256, contract, store: state.store,
    transport: { async active() { return true; }, async publish() { throw new Error('must not grant'); } },
    async authorize() { state.advance(60000); return { actor_id: 'alice', owner_id: 'alice', roles: ['reviewer'] }; } });
  await assert.rejects(service.decide(intent.id, request(), 'alice', settings), { code: 'conflict' });
  assert.equal(state.rows.get(intent.id)?.decision?.state, 'expired');
  const cancelled = setup(); await cancelled.store.open(intent, signal); cancelled.active = false;
  await assert.rejects(cancelled.service().decide(intent.id, request(), 'alice', settings), { code: 'inactive' });
  await cancelled.service().drain(10, settings);
  assert.equal(cancelled.rows.get(intent.id)?.decision?.state, 'cancelled'); assert.equal(cancelled.rows.get(intent.id)?.delivery, 'closed'); assert.equal(cancelled.events.length, 0);
});
test('changed reviewed inputs conflict; rejected gates cannot be ignored before Jobs submission', async () => {
  const state = setup(); await state.store.open(intent, signal);
  const { version: _v, id: _id, binding_sha256: _b, review_sha256: _r, ...base } = intent;
  const changed = pipelineGateIntent({ ...base, review: { ...fixture.intent.review, rows: 4 } });
  assert.equal(changed.id, intent.id); assert.notEqual(changed.binding_sha256, intent.binding_sha256);
  await assert.rejects(state.store.open(changed, signal));
  let posts = 0;
  const job = { contract, job: { submit: async () => { posts++; throw new Error(); } } } as unknown as PipelineJob<any, any>;
  const p = defineJobPipeline(root, { scope, store: {} as PipelineJobStore, gateStore: state.store, jobs: { import: job } }, async (_input, wf) => {
    await wf.approval('review', contract, fixture.intent.review, { timeoutMs: 60000, requiredRoles: ['reviewer'] });
    await wf.job('import', [{ id: 'row', value: fixture.intent.review }]); return { n: 1 };
  });
  const journal = structuredClone(fixture.journal); journal[2].output.state = 'rejected';
  const reply = await invoke(p, journal);
  assert.equal(reply.result.status, 'failed'); assert.equal(reply.result.error, 'pipeline-gate:blocked'); assert.equal(posts, 0);
});
test('external input validates typed replies and cancellation has a typed outcome', async () => {
  const state = setup(); const { version: _v, id: _id, binding_sha256: _b, review_sha256: _r, ...base } = intent;
  const input = pipelineGateIntent({ ...base, kind: 'input' }); await state.store.open(input, signal);
  await assert.rejects(state.service().decide(input.id, { ...request(), binding_sha256: input.binding_sha256 }, 'alice', settings), { code: 'conflict' });
  await assert.rejects(state.service().decide(input.id, { binding_sha256: input.binding_sha256, state: 'submitted', payload: {} as { reason: string } }, 'alice', settings), { code: 'request_failed' });
  assert.equal((await state.service().decide(input.id, { ...request('submitted'), binding_sha256: input.binding_sha256 }, 'alice', settings)).decision?.state, 'submitted');
  const other = setup(); await other.store.open(intent, signal);
  assert.equal((await other.service().decide(intent.id, request('cancelled'), 'alice', settings)).decision?.state, 'cancelled');
});
test('deadline/cancellation fences a late authorization callback before decision commit', async () => {
  const state = setup(); await state.store.open(intent, signal);
  let release!: () => void; const wait = new Promise<void>(r => { release = r; });
  const service = createPipelineGateService({ scope, pipelineSha256: root.sha256, contract, store: state.store, transport: { async active() { return true; }, async publish() {} }, async authorize() { await wait; return { actor_id: 'alice', owner_id: 'alice', roles: ['reviewer'] }; } });
  await assert.rejects(service.decide(intent.id, request(), 'alice', { timeoutMs: 5 }));
  release(); await new Promise(r => setImmediate(r)); assert.equal(state.rows.get(intent.id)?.decision, null);
  const abort = new AbortController(); abort.abort();
  await assert.rejects(service.decide(intent.id, request(), 'alice', { ...settings, signal: abort.signal }));
  assert.equal(state.rows.get(intent.id)?.decision, null);
});
test('native transport verifies the root ownership/input and sends a stable wakeup with no hidden retry', async () => {
  const calls: { path: string; key: string | null; body: unknown }[] = []; let owner = 'alice';
  const transport = nativePipelineGateTransport({ scope, token: 'backend', contract: root, timeoutMs: 1000, fetch: async (url, init) => {
    const path = new URL(String(url)).pathname; calls.push({ path, key: new Headers(init?.headers).get('Idempotency-Key'), body: init?.body ? JSON.parse(String(init.body)) : null });
    const value = path === '/v1/account' ? { id: scope.account_id } : path.endsWith('/apps/imports') ? { id: scope.app_id } : path.endsWith('/steps') ? { steps: [] } : path.endsWith('/events') ? { status: 'received', event_name: 'pipeline-gate.' + intent.id } : { id: intent.workflow_run_id, app_id: scope.app_id, workflow_name: root.spec.name, status: 'awaiting_event', input: { ...fixture.envelope, owner_id: owner } };
    return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
  } });
  assert.equal(await transport.active(intent, signal), true); await transport.publish(intent, signal); await transport.publish(intent, signal);
  assert.deepEqual(calls.at(-1), calls.at(-2)); assert.equal(calls.at(-1)?.key, 'pipeline-gate:' + intent.id);
  owner = 'bob'; await assert.rejects(transport.active(intent, signal));
});
test('live snapshots retain validated pending gate metadata', () => {
  const value = { version: 1, resource_id: 'review', run_id: intent.workflow_run_id, status: 'running', total: 1, succeeded: 1, failed: 0, cancelled: 0, running: 0, report: 'none', failures: [], waiting: { kind: 'approval', gate_id: intent.id, name: 'review' } };
  assert.deepEqual(parseLiveJobSnapshot(value, 'review').waiting, value.waiting);
  assert.throws(() => parseLiveJobSnapshot({ ...value, waiting: { ...value.waiting, gate_id: 'unsafe' } }, 'review'));
});

test('bounded irrelevant wakes fail closed and expired/cancelled decisions replay as business outcomes', async () => {
  const state=memory(),journal:JournalEntry[]=[];
  let reply;
  for(let n=0;n<40;n++){
    reply=await invoke(pipeline(state.store),journal);
    if(reply.result.status==='failed')break;
    commit(journal,reply);
  }
  assert.equal(reply?.result.status,'failed');
  assert.equal(reply?.result.error,'pipeline-gate:wake_limit');
  for(const choice of ['expired','cancelled'] as const){
    const saved=structuredClone(fixture.journal);
    saved[2].output={state:choice,actor_id:null,payload:null,decided_at:fixture.record.decision.decided_at};
    const result=await invoke(pipeline(state.store),saved);
    assert.equal(result.result.status,'succeeded');
    assert.deepEqual((result.result as {output:{output:unknown}}).output.output,{n:0});
  }
});
test('external input journal replay returns its typed submitted payload', async () => {
  const state=memory(),{version:_v,id:_id,review_sha256:_r,binding_sha256:_b,...base}=intent;
  const inputIntent=pipelineGateIntent({...base,kind:'input'}),journal=structuredClone(fixture.journal);
  journal[0].output.binding_sha256=inputIntent.binding_sha256;journal[2].output.state='submitted';
  const p=defineJobPipeline(root,{scope,store:{} as PipelineJobStore,gateStore:state.store,jobs:{}},async(_value,wf)=>{
    const outcome=await wf.waitForInput('review',contract,fixture.intent.review,{timeoutMs:60000,requiredRoles:['reviewer']});
    return {n:outcome.state==='submitted'?outcome.payload.reason.length:0};
  });
  const result=await invoke(p,journal);
  assert.equal(result.result.status,'succeeded');
  assert.deepEqual((result.result as {output:{output:unknown}}).output.output,{n:7});
});
test('a pending gate fences parallel Jobs before their first request',async()=>{
  const state=memory();let called=false;
  const p=defineJobPipeline(root,{scope,store:{} as PipelineJobStore,gateStore:state.store,jobs:{work:{} as PipelineJob<any,any>}},async(_value,wf)=>{
    const approval=wf.approval('review',contract,fixture.intent.review,{timeoutMs:60000,requiredRoles:['reviewer']});
    await assert.rejects(wf.job('work',[{id:'row',value:{}}]),{code:'blocked'});called=true;
    await approval;return {n:1};
  });
  const result=await invoke(p);assert.equal(called,true);assert.equal(result.ops.length,1);assert.equal(result.ops[0]?.name,'pipeline-gate/review/open');
});

test('shared PostgreSQL statements match Python including rotating recovery batches',async()=>{
  const queries:string[]=[];
  const store=postgresPipelineGateStore(async sql=>{queries.push(sql);return {rows:[{record:fixture.record}]};});
  await store.open(intent,signal);await store.resolve(intent.id,signal);
  await store.decide(intent.id,intent.binding_sha256,{state:'approved',actor_id:'alice',payload:{reason:'checked'}},signal);
  await store.mark(intent.id,fixture.record.decision,'delivered',signal);
  await store.pending(2,scope,root.sha256,contract.sha256,signal);
  await store.list(intent.workflow_run_id,'alice',signal);
  assert.deepEqual(queries,fixture.queries);
});
