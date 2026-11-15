import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { JobContract, jobHash, prepareJobInput, type JobContractSpec, type JobInput } from '../src/job-runtime.js';
import { defineJobPipeline, type PipelineJob } from '../src/job-pipelines.js';
import { PipelineError, pipelineJobIdentity, parsePipelineJobReceipt, readPipelineArtifact, type PipelineJobStore, type PipelineJobReceipt, type PipelineScope } from '../src/pipeline-state.js';
import type { FileBucket } from '../src/files.js';
import { runWorkflowInvocation, WORKFLOW_CODE_PROTOCOL, type JournalEntry, type WorkflowInvocationResponse } from '../src/workflows.js';
import { createJobPipelineClient } from '../src/job-pipeline-client.js';
import type { TypedJob, JobCheckpoint, TypedJobRun } from '../src/typed-jobs.js';
const f = JSON.parse(readFileSync(new URL('../../../request-contracts/job-pipelines.json', import.meta.url), 'utf8')) as {
  scope: PipelineScope; workflow_run_id: string; root_contract: JobContractSpec; job_contracts: Record<string, JobContractSpec>;
  job_id: string; job_run_ids: string[]; input: { n: number }; output: { n: number }; validate_receipt: PipelineJobReceipt; validate_journal: JournalEntry[];
};
const scope = f.scope, root = new JobContract<{ n: number }, { n: number }>(f.root_contract);
function memoryStore() {
  const rows = new Map<string, PipelineJobReceipt>();
  let loseReserve = false, loseComplete = false;
  const store: PipelineJobStore = {
    async load(id) { return rows.get(id) ?? null; },
    async reserve(intent) {
      const old = rows.get(intent.identity); if (old) return { created: false, receipt: old };
      const receipt = parsePipelineJobReceipt({ intent, checkpoint: null }); rows.set(intent.identity, receipt);
      if (loseReserve) throw new Error('lost acknowledgement');
      return { created: true, receipt };
    },
    async complete(intent, checkpoint) {
      const receipt = parsePipelineJobReceipt({ intent, checkpoint }); rows.set(intent.identity, receipt);
      if (loseComplete) throw new Error('lost completion acknowledgement'); return receipt;
    },
  };
  return { store, rows, set loseReserve(v: boolean) { loseReserve = v; }, set loseComplete(v: boolean) { loseComplete = v; } };
}
function jobs() {
  const definitions: Record<string, PipelineJob<{ n: number }, { n: number }>> = {};
  const accepted = new Map<string, JobCheckpoint>(), outputs = new Map<string, readonly JobInput<{ n: number }>[]>();
  let posts = 0, loseAccept = false, pending = false, failed = false;
  for (const [index, [stage, spec]] of Object.entries(f.job_contracts).entries()) {
    const contract = new JobContract<{ n: number }, { n: number }>(spec);
    const handle = (checkpoint: JobCheckpoint): TypedJobRun<{ n: number }> => ({
      checkpoint,
      async status() { return { runId: checkpoint.run_id, status: pending ? 'running' : failed ? 'failed' : 'succeeded', total: 1, succeeded: pending || failed ? 0 : 1, failed: failed ? 1 : 0, cancelled: 0, running: pending ? 1 : 0 }; },
      async results() { return outputs.get(checkpoint.run_id)!.map((item, i) => ({ inputId: item.id, index: i, attempt: 1, outcome: 'succeeded' as const, result: { n: item.value.n + 1 } })); },
      async wait() { return this.status({ timeoutMs: 100 }); }, async cancel() { return this.status({ timeoutMs: 100 }); }, async retryFailed() { return this; },
    });
    const job: TypedJob<{ n: number }, { n: number }> = {
      async submit(inputs, options) {
        assert.equal(options.expectedAccountId, scope.account_id); assert.equal(options.expectedBaseURL, scope.base_url); posts++;
        const refs = await Promise.all(inputs.map(item => prepareJobInput(contract, item, { inputId: item.id, attempt: 0, signal: new AbortController().signal })));
        const checkpoint: JobCheckpoint = { version: 1, base_url: scope.base_url, account_id: scope.account_id, job_id: f.job_id, job_name: spec.name, run_id: f.job_run_ids[index]!, contract_sha256: contract.sha256, input_digest: jobHash(Buffer.from(JSON.stringify(refs))), source_run_id: null, inputs: refs.map(r => ({ input_id: r.input_id, binding_sha256: jobHash(Buffer.from(r.input_ref)), source_task_index: null })) };
        accepted.set(checkpoint.run_id, checkpoint); outputs.set(checkpoint.run_id, inputs);
        if (loseAccept) throw new Error('uncertain POST'); return handle(checkpoint);
      }, resume: handle, async resumeById(id) { return handle(accepted.get(id)!); },
    };
    definitions[stage] = { contract, job };
  }
  return { definitions, accepted, get posts() { return posts; }, set loseAccept(v: boolean) { loseAccept = v; }, set pending(v: boolean) { pending = v; }, set failed(v: boolean) { failed = v; } };
}
function pipeline(store: PipelineJobStore, definitions: ReturnType<typeof jobs>['definitions'], options = {}) {
  return defineJobPipeline(root, { scope, store, jobs: definitions, ...options }, async (input, wf) => {
    let n = input.n;
    for (const stage of Object.keys(definitions)) n = (await wf.job(stage, [{ id: 'one', value: { n } }])).results[0]!.result.n;
    return { n };
  });
}
const envelope = { version: 1, contract_sha256: root.sha256, scope, owner_id: 'alice', input: f.input };
const invoke = (definition: ReturnType<typeof pipeline>['definition'], journal: JournalEntry[] = []) => runWorkflowInvocation(definition, { protocol: WORKFLOW_CODE_PROTOCOL, run_id: f.workflow_run_id, workflow: root.spec.name, input: envelope, invocation: journal.length + 1, journal });
function commit(journal: JournalEntry[], response: WorkflowInvocationResponse): void {
  for (const op of response.ops) journal[op.seq] = { seq: op.seq, name: op.name, kind: op.kind, status: op.kind === 'sleep' ? 'succeeded' : op.status!, attempt: 1, ...(op.output !== undefined ? { output: op.output } : {}), ...(op.error ? { error: op.error } : {}) };
}
test('three typed stages survive lost submission/completion journals and fresh runtimes', async () => {
  const state = memoryStore(), native = jobs(), journal: JournalEntry[] = [], dropped = new Set<number>(); let result;
  for (let i = 0; i < 40; i++) {
    const response = await invoke(pipeline(state.store, native.definitions).definition, journal);
    if (response.result.status === 'succeeded') { result = response.result.output; break; }
    assert.equal(response.result.status, 'running');
    const seq = response.ops[0]!.seq;
    if (!dropped.has(seq)) { dropped.add(seq); continue; } // crash before persisting journal
    commit(journal, response);
  }
  assert.deepEqual(result, { version: 1, contract_sha256: root.sha256, scope, owner_id: 'alice', output: f.output });
  assert.equal(native.posts, 3); assert.equal(state.rows.size, 3); assert.equal(journal.length, 6);
  assert.deepEqual([...state.rows.values()][0], f.validate_receipt);
});
test('shared Node/Python journal and receipts replay completed validation without resubmitting it', async () => {
  assert.deepEqual(parsePipelineJobReceipt(f.validate_receipt), f.validate_receipt);
  const state = memoryStore(), native = jobs(), journal = structuredClone(f.validate_journal);
  let output;
  for (let i = 0; i < 10; i++) { const r = await invoke(pipeline(state.store, native.definitions).definition, journal); if (r.result.status === 'succeeded') { output = r.result.output; break; } commit(journal, r); }
  assert.deepEqual((output as { output: unknown }).output, f.output); assert.equal(native.posts, 2);
});
test('a repeated stage identity cannot adopt different logical inputs', async () => {
  const state = memoryStore(), native = jobs(), p = pipeline(state.store, native.definitions);
  await invoke(p.definition);
  const reply = await runWorkflowInvocation(p.definition, { protocol: WORKFLOW_CODE_PROTOCOL, run_id: f.workflow_run_id, workflow: root.spec.name, input: { ...envelope, input: { n: 99 } }, invocation: 2, journal: [] });
  assert.equal(reply.ops[0]!.status, 'failed'); assert.equal(reply.ops[0]!.error, 'pipeline:receipt_conflict:validate'); assert.equal(native.posts, 1);
});
test('concurrent duplicate drivers reserve once, and uncertain submission reconciles without another POST', async () => {
  const state = memoryStore(), native = jobs(); native.loseAccept = true;
  const p = pipeline(state.store, native.definitions);
  const [a, b] = await Promise.all([invoke(p.definition), invoke(p.definition)]);
  assert.equal(native.posts, 1); assert.equal(a.ops[0]!.status, 'succeeded'); assert.equal(b.ops[0]!.status, 'succeeded');
  const journal: JournalEntry[] = []; commit(journal, a);
  const unresolved = await invoke(p.definition, journal); assert.deepEqual(unresolved.ops[0]!.output, { state: 'submission_unresolved' }); commit(journal, unresolved);
  const pause = await invoke(p.definition, journal); assert.equal(pause.ops[0]!.kind, 'sleep'); commit(journal, pause);
  await assert.rejects(p.reconcile(f.workflow_run_id, 'validate', f.job_run_ids[0]!, 'bob', { timeoutMs: 100 }), { code: 'receipt_conflict' });
  await p.reconcile(f.workflow_run_id, 'validate', f.job_run_ids[0]!, 'alice', { timeoutMs: 100 });
  native.loseAccept = false;
  for (let i = 0; i < 20; i++) { const r = await invoke(pipeline(state.store, native.definitions).definition, journal); if (r.result.status === 'succeeded') break; commit(journal, r); }
  assert.equal(native.posts, 3);
});
test('lost durable-store acknowledgements never repeat accepted or uncertain mutations', async () => {
  for (const mode of ['reserve', 'complete']) {
    const state = memoryStore(), native = jobs(); if (mode === 'reserve') state.loseReserve = true; else state.loseComplete = true;
    await invoke(pipeline(state.store, native.definitions).definition);
    state.loseReserve = state.loseComplete = false;
    const after = await invoke(pipeline(state.store, native.definitions).definition);
    assert.equal(after.ops[0]!.status, 'succeeded'); assert.equal(native.posts, mode === 'reserve' ? 0 : 1);
  }
});
test('native job failure, poll/operation/journal bounds and scoped recovery stop downstream stages', async () => {
  for (const mode of ['failed', 'poll', 'operations', 'journal']) {
    const state = memoryStore(), native = jobs(), journal: JournalEntry[] = [];
    if (mode === 'failed') native.failed = true; if (mode === 'poll') native.pending = true;
    const p = pipeline(state.store, native.definitions, { maxPolls: 1, ...(mode === 'operations' ? { maxOperations: 1 } : {}), ...(mode === 'journal' ? { maxJournalBytes: 1 } : {}) });
    let stopped = false;
    for (let i = 0; i < 10; i++) { const r = await invoke(p.definition, journal); if (r.result.status === 'failed' || r.ops.some(op => op.status === 'failed')) { stopped = true; break; } commit(journal, r); }
    assert.equal(stopped, true); assert.equal(native.posts, 1);
    await assert.rejects(p.inspectJob(f.workflow_run_id, 'validate', 'bob', { timeoutMs: 100 }), { code: 'receipt_conflict' });
  }
});
test('scoped typed client starts, reattaches, verifies results and denies another owner/account before mutation', async () => {
  let posts = 0, cancelled = false;
  const fetch: typeof globalThis.fetch = async (url, init) => {
    const path = new URL(String(url)).pathname;
    if (path === '/v1/account') return Response.json({ id: scope.account_id });
    if (path === '/v1/apps/imports') return Response.json({ id: scope.app_id });
    if (path.endsWith('/steps')) return Response.json({ steps: [] });
    if (init?.method === 'POST') { posts++; if (path.endsWith('/cancel')) cancelled = true; }
    const output = { version: 1, contract_sha256: root.sha256, scope, owner_id: 'alice', output: f.output };
    return Response.json({ id: f.workflow_run_id, app_id: scope.app_id, workflow_name: root.spec.name, input: envelope, output, status: 'succeeded', ...(cancelled ? { cancelled_at: '2026-11-15T00:00:00Z' } : {}) });
  };
  const alice = createJobPipelineClient({ baseURL: scope.base_url, token: 'alice-platform', scope, ownerId: 'alice', contract: root, fetch });
  const run = await alice.start(f.input, { timeoutMs: 1000, idempotencyKey: 'one' });
  assert.deepEqual(await run.result({ timeoutMs: 1000 }), f.output);
  const resumed = await alice.resumeById(run.checkpoint.run_id, { timeoutMs: 1000 }); assert.deepEqual(resumed.checkpoint, run.checkpoint);
  const bob = createJobPipelineClient({ baseURL: scope.base_url, token: 'bob-platform', scope, ownerId: 'bob', contract: root, fetch });
  await assert.rejects(bob.resumeById(f.workflow_run_id, { timeoutMs: 1000 }), { code: 'scope_mismatch' });
  assert.equal(posts, 1); assert.equal((await run.cancel({ timeoutMs: 1000 })).cancelled, true); assert.equal(posts, 2);
  await assert.rejects(run.result({ timeoutMs: 1000 }), { code: 'invalid_result' });
});
test('artifact handoff checks owner, scope, exact bytes, limits and cancellation', async () => {
  const bytes = new TextEncoder().encode('report'), fileScope = { base_url: scope.base_url, app_slug: scope.app_slug, bucket: f.job_id }; let reads = 0;
  const files = { scope: fileScope, async download() { reads++; return bytes; } } as unknown as FileBucket;
  const artifact = { version: 1 as const, scope: fileScope, owner_id: 'alice', key: 'reports/one.csv', size_bytes: bytes.length, sha256: jobHash(bytes) };
  assert.deepEqual(await readPipelineArtifact(files, artifact, 'alice', { timeoutMs: 100 }), bytes);
  assert.throws(() => readPipelineArtifact(files, artifact, 'bob', { timeoutMs: 100 }), { code: 'artifact_changed' });
  assert.throws(() => readPipelineArtifact(files, artifact, 'alice', { timeoutMs: 100, maxBytes: 1 }), { code: 'journal_limit' });
  await assert.rejects(readPipelineArtifact(files, { ...artifact, sha256: '0'.repeat(64) }, 'alice', { timeoutMs: 100 }), { code: 'artifact_changed' });
  const abort = new AbortController(); abort.abort(); await assert.rejects(readPipelineArtifact(files, artifact, 'alice', { timeoutMs: 100, signal: abort.signal })); assert.equal(reads, 2);
});
test('expired or cancelled store work cannot submit late; reconciliation rejects another account', async () => {
  for (const cpu of [false, true]) {
    const state = memoryStore(), native = jobs();
    const stalled: PipelineJobStore = { ...state.store, async reserve(intent, signal) {
      if (cpu) { const end = performance.now() + 20; while (performance.now() < end) { /* delay timer callbacks */ } }
      else await new Promise(resolve => setTimeout(resolve, 20));
      return state.store.reserve(intent, signal);
    } };
    const reply = await invoke(pipeline(stalled, native.definitions, { requestTimeoutMs: 5 }).definition);
    assert.equal(reply.ops[0]!.status, 'failed'); await new Promise(resolve => setTimeout(resolve, 30)); assert.equal(native.posts, 0);
  }
  const state = memoryStore(), native = jobs(); await invoke(pipeline(state.store, native.definitions).definition);
  const accepted = native.accepted.get(f.job_run_ids[0]!)!; native.accepted.set(accepted.run_id, { ...accepted, account_id: f.workflow_run_id });
  await assert.rejects(pipeline(state.store, native.definitions).reconcile(f.workflow_run_id, 'validate', accepted.run_id, 'alice', { timeoutMs: 100 }), { code: 'receipt_conflict' });
});
test('typed start has no hidden mutation retries and a delayed deadline cannot pass the account guard', async () => {
  let posts = 0;
  const fetch: typeof globalThis.fetch = async (url, init) => {
    if (new URL(String(url)).pathname === '/v1/account') return Response.json({ id: scope.account_id });
    if (init?.method !== 'POST') return Response.json({ id: scope.app_id });
    posts++; return Response.json({}, { status: 503 });
  };
  const client = createJobPipelineClient({ baseURL: scope.base_url, token: 'one', ownerId: 'alice', scope, contract: root, fetch });
  await assert.rejects(client.start(f.input, { timeoutMs: 100, idempotencyKey: 'one' }), { code: 'acceptance_unknown' }); assert.equal(posts, 1);
  const delayed = createJobPipelineClient({ baseURL: scope.base_url, token: 'one', ownerId: 'alice', scope, contract: root, fetch: async (url, init) => { const end = performance.now() + 20; while (performance.now() < end) { /* timer fence */ } return fetch(url, init); } });
  await assert.rejects(delayed.start(f.input, { timeoutMs: 5, idempotencyKey: 'two' })); assert.equal(posts, 1);
  const abort = new AbortController(); abort.abort(); await assert.rejects(client.start(f.input, { timeoutMs: 100, signal: abort.signal, idempotencyKey: 'three' })); assert.equal(posts, 1);
});
