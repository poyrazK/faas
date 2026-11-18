/** Typed Jobs composed using the existing code-first workflow journal. */
import { defineWorkflow, type WorkflowDefinition } from './workflows.js';
import { JobContract, jobHash, jobValueHash, jobSnapshot, jobScope, jobInputId, prepareJobInput, type JobInput, type JobStore } from './job-runtime.js';
import type { TypedJob, JobProgress } from './typed-jobs.js';
import { PipelineError, parsePipelineTrigger, parsePipelineLineage, pipelineScope, pipelineName, pipelineJobIdentity, parsePipelineJobReceipt, matchPipelineJob, type PipelineScope, type PipelineEnvelope, type PipelineResultEnvelope, type PipelineJobStore, type PipelineJobIntent, type PipelineJobReceipt } from './pipeline-state.js';
export * from './pipeline-state.js';
import { PipelineChildren, PipelineChildrenError, type PipelineChild } from './pipeline-children.js';
import type { PipelineChildPolicy, PipelineBatchResult, PipelineChildView } from './pipeline-children-types.js';
import { PipelineGateError, pipelineGateIntent, runPipelineGate, type PipelineGateOptions, type PipelineGateStore, type PipelineGateOutcome, type PipelineGateKind } from './pipeline-gates.js';

export interface PipelineJob<I, O> {
  readonly contract: JobContract<I, O>; readonly job: TypedJob<I, O>; readonly jobStore?: JobStore;
  readonly parallelism?: number; readonly retryMax?: number;
}
type Jobs = Readonly<Record<string, PipelineJob<any, any>>>;
type InputOf<T> = T extends PipelineJob<infer I, any> ? I : never;
type OutputOf<T> = T extends PipelineJob<any, infer O> ? O : never;
export interface PipelineJobResult<O> {
  readonly run_id: string;
  readonly results: readonly { readonly input_id: string; readonly index: number; readonly attempt: number; readonly result: O }[];
}
export interface JobPipelineContext<J extends Jobs> {
  readonly runId: string; readonly ownerId: string;
  child<I, O>(name: string, definition: PipelineChild<I, O>, input: I, options: PipelineChildPolicy): Promise<PipelineChildView<O>>;
  children<I, O>(name: string, definition: PipelineChild<I, O>, inputs: readonly JobInput<I>[], options: PipelineChildPolicy): Promise<PipelineBatchResult<O>>;
  approval<R, D>(name: string, contract: JobContract<R, D>, review: R, options: PipelineGateOptions): Promise<PipelineGateOutcome<D>>;
  waitForInput<R, D>(name: string, contract: JobContract<R, D>, review: R, options: PipelineGateOptions): Promise<PipelineGateOutcome<D>>;
  /** Submit once, observe once per durable wake, and record only verified results. */
  job<K extends keyof J & string>(step: K, inputs: readonly JobInput<InputOf<J[K]>>[]): Promise<PipelineJobResult<OutputOf<J[K]>>>;
}
export interface JobPipelineOptions<J extends Jobs> {
  scope: PipelineScope; store: PipelineJobStore; jobs: J; gateStore?: PipelineGateStore; children?: PipelineChildren;
  requestTimeoutMs?: number; pollDelayMs?: number; maxPolls?: number;
  maxOperations?: number; maxJournalBytes?: number;
}
export interface JobPipeline<I, O, J extends Jobs> {
  readonly definition: WorkflowDefinition<PipelineEnvelope<I>, PipelineResultEnvelope<O>>;
  /** Read-only Job adoption followed by an exact receipt write. No submission. */
  reconcile<K extends keyof J & string>(workflowRunId: string, step: K, jobRunId: string, ownerId: string, options: { timeoutMs: number; signal?: AbortSignal }): Promise<PipelineJobReceipt>;
  /** Authorize the application user first. Useful for a live snapshot loader. */
  inspectJob<K extends keyof J & string>(workflowRunId: string, step: K, ownerId: string, options: { timeoutMs: number; signal?: AbortSignal }): Promise<{ receipt: PipelineJobReceipt | null; progress: JobProgress | null }>;
}
export function defineJobPipeline<I, O, J extends Jobs>(contract: JobContract<I, O>, options: JobPipelineOptions<J>, run: (input: I, context: JobPipelineContext<J>) => Promise<O>): JobPipeline<I, O, J> {
  const scope = pipelineScope(options.scope), store = options.store, gateStore = options.gateStore, children = options.children;
  const jobs = Object.freeze(Object.fromEntries(Object.entries(options.jobs).map(([k, v]) => [k, Object.freeze({ ...v })]))) as J;
  const timeout = options.requestTimeoutMs ?? 10000, delay = options.pollDelayMs ?? 1000;
  const polls = options.maxPolls ?? 100, operations = options.maxOperations ?? 900, bytesLimit = options.maxJournalBytes ?? 65536;
  if (!Object.keys(jobs).length && !gateStore && !children || Object.keys(jobs).some(k => !pipelineName(k)) || !Number.isSafeInteger(timeout) || timeout <= 0 || !Number.isSafeInteger(delay) || delay < 1 || !Number.isSafeInteger(polls) || polls < 1 || polls > 150 || !Number.isSafeInteger(operations) || operations < 1 || operations > 950 || !Number.isSafeInteger(bytesLimit) || bytesLimit < 1 || bytesLimit > 262144) throw new PipelineError('invalid_input');
  for (const d of Object.values(jobs)) if (d.parallelism !== undefined && (!Number.isSafeInteger(d.parallelism) || d.parallelism < 1) || d.retryMax !== undefined && (!Number.isSafeInteger(d.retryMax) || d.retryMax < 0)) throw new PipelineError('invalid_input');
  function owned(r: PipelineJobReceipt, runId: string, step: string, owner: string): PipelineJobReceipt {
    const i = r.intent;
    if (!pipelineName(owner) || i.identity !== pipelineJobIdentity(scope, runId, step) || i.workflow_run_id !== runId || i.step !== step || i.owner_id !== owner || i.pipeline_sha256 !== contract.sha256 || jobValueHash(i.scope) !== jobValueHash(scope) || i.contract_sha256 !== jobs[step]!.contract.sha256 || i.job_name !== jobs[step]!.contract.spec.name) throw new PipelineError('receipt_conflict', step);
    return r;
  }
  async function operation<T>(duration: number, signal: AbortSignal | undefined, work: (signal: AbortSignal, check: () => void) => Promise<T>): Promise<T> {
    const end = performance.now() + duration;
    return jobScope(duration, signal, async active => {
      const check = () => { active.throwIfAborted(); if (performance.now() >= end) throw new PipelineError('deadline'); };
      try { check(); const value = await work(active, check); check(); return value; }
      catch (error) { check(); if (error instanceof PipelineError || error instanceof PipelineGateError) throw error; throw new Error('pipeline:request_failed'); }
    });
  }
  async function load(runId: string, step: string, owner: string, signal: AbortSignal, check: () => void): Promise<PipelineJobReceipt | null> {
    const raw = await store.load(pipelineJobIdentity(scope, runId, step), signal); check();
    return raw === null ? null : owned(parsePipelineJobReceipt(raw), runId, step, owner);
  }
  const definition = defineWorkflow<PipelineEnvelope<I>, PipelineResultEnvelope<O>>(contract.spec.name, async (envelope, wf) => {
    if (!envelope || envelope.version !== 1 || envelope.contract_sha256 !== contract.sha256 || !pipelineName(envelope.owner_id) || jobValueHash(envelope.scope) !== jobValueHash(scope)) throw new PipelineError('scope_mismatch');
    if (envelope.lineage !== undefined) parsePipelineLineage(envelope.lineage);
    if (envelope.trigger !== undefined) parsePipelineTrigger(envelope.trigger);
    const input = contract.input(envelope.input), owner = envelope.owner_id, used = new Set<string>();
    let count = 0, journalBytes = 0, pendingGates = 0, blocked = false;
    const canSubmit = () => { if (pendingGates || blocked) throw new PipelineGateError('blocked'); };
    const claim = () => { if (++count > operations) throw new PipelineError('operation_limit'); };
    const budget = <T>(value: T): T => { journalBytes += Buffer.byteLength(JSON.stringify(value)); if (journalBytes > bytesLimit) throw new PipelineError('journal_limit'); return value; };
    async function gate<R, D>(kind: PipelineGateKind, name: string, gateContract: JobContract<R, D>, review: R, settings: PipelineGateOptions): Promise<PipelineGateOutcome<D>> {
      if (composing) throw new PipelineChildrenError('blocked');
      if (!gateStore || used.has(name)) throw new PipelineGateError('invalid_input');
      used.add(name); pendingGates++;
      try {
        const intent = pipelineGateIntent({ kind, name, scope, workflow_run_id: wf.runId, owner_id: owner, pipeline_sha256: contract.sha256, input_sha256: jobValueHash({ ...envelope, input }), contract_sha256: gateContract.sha256, review: gateContract.input(review), required_roles: settings.requiredRoles, timeout_ms: settings.timeoutMs });
        const outcome = await runPipelineGate(wf, gateStore, intent, gateContract, timeout, claim, budget);
        if (!['approved', 'submitted'].includes(outcome.state)) blocked = true;
        return outcome;
      } catch (e) { blocked = true; throw e; }
      finally { pendingGates--; }
    }
    let composing = false;
    async function batch<I, O>(name: string, definition: PipelineChild<I, O>, values: readonly JobInput<I>[], settings: PipelineChildPolicy): Promise<PipelineBatchResult<O>> {
      canSubmit();
      if (!children || used.has(name) || composing) throw new PipelineError('invalid_input');
      used.add(name); composing = true;
      try { return await children.run(wf, { ...envelope, input }, name, definition, values, settings, { claim, budget, guard: canSubmit, polls, delay }); }
      finally { composing = false; }
    }
    const context: JobPipelineContext<J> = {
      children: batch,
      child: async (name, definition, value, settings) => (await batch(name, definition, [{ id: 'one', value }], { ...settings, concurrency: 1 })).children[0]!,
      approval: (name, c, review, settings) => gate('approval', name, c, review, settings),
      waitForInput: (name, c, review, settings) => gate('input', name, c, review, settings),
      runId: wf.runId, ownerId: owner, async job(step, rawInputs) {
      canSubmit();
      if (composing) throw new PipelineChildrenError('blocked');
      if (!Object.hasOwn(jobs, step) || used.has(step)) throw new PipelineError('invalid_input', step); used.add(step);
      const d = jobs[step]!;
      if (!Array.isArray(rawInputs) || !rawInputs.length || rawInputs.length > 1000 || rawInputs.some(i => !i || !jobInputId(i.id))) throw new PipelineError('invalid_input', step);
      const inputs = jobSnapshot(rawInputs.map(i => ({ id: i.id, value: d.contract.input(i.value) })));
      if (!inputs.length || inputs.length > 1000 || new Set(inputs.map(i => i.id)).size !== inputs.length) throw new PipelineError('invalid_input', step);
      const identity = pipelineJobIdentity(scope, wf.runId, step);
      const valuesHash = jobValueHash({ inputs, parallelism: d.parallelism ?? null, retry_max: d.retryMax ?? null });
      const submittedValue = { identity, values_sha256: valuesHash, contract_sha256: d.contract.sha256 };
      claim();
      const submitted = await wf.step('pipeline/' + step + '/submit', () => operation(timeout, undefined, async (signal, check) => {
        const old = await load(wf.runId, step, owner, signal, check);
        if (old) { if (old.intent.values_sha256 !== valuesHash) throw new PipelineError('receipt_conflict', step); return submittedValue; }
        const refs = [];
        for (const item of inputs) { check(); refs.push(await prepareJobInput(d.contract, item, { inputId: item.id, attempt: 0, signal }, d.jobStore)); check(); }
        if (Buffer.byteLength(JSON.stringify(refs)) > 1024 * 1024) throw new PipelineError('invalid_input', step);
        const intent: PipelineJobIntent = jobSnapshot({ version: 1, identity, scope, workflow_run_id: wf.runId, step, owner_id: owner, pipeline_sha256: contract.sha256, contract_sha256: d.contract.sha256, job_name: d.contract.spec.name, values_sha256: valuesHash, input_digest: jobHash(Buffer.from(JSON.stringify(refs))), idempotency_key: 'pipeline-job:' + identity, inputs: refs.map(r => ({ input_id: r.input_id, binding_sha256: jobHash(Buffer.from(r.input_ref)), source_task_index: null })) });
        const reserved = await store.reserve(intent, signal); check();
        const saved = owned(parsePipelineJobReceipt(reserved.receipt), wf.runId, step, owner);
        // Another SDK may encode object order or floats differently. An old
        // intent with the same canonical values/settings owns its exact native
        // manifest. Only a newly created intent must match our prepared bytes.
        if (typeof reserved.created !== 'boolean' || saved.intent.values_sha256 !== valuesHash || reserved.created && jobValueHash(saved.intent) !== jobValueHash(intent)) throw new PipelineError('receipt_conflict', step);
        if (reserved.created) {
          if (saved.checkpoint) throw new PipelineError('receipt_conflict', step);
          check(); canSubmit();
          // A durable intent without an acknowledged checkpoint is never posted
          // again, even after the native Jobs idempotency window expires.
          try {
            const accepted = await d.job.submit(inputs, { timeoutMs: timeout, signal, idempotencyKey: intent.idempotency_key, expectedBaseURL: scope.base_url, expectedAccountId: scope.account_id, expectedContractSha256: d.contract.sha256, maxItems: 1000, ...(d.parallelism !== undefined ? { parallelism: d.parallelism } : {}), ...(d.retryMax !== undefined ? { retryMax: d.retryMax } : {}) }); check();
            const checkpoint = matchPipelineJob(intent, accepted.checkpoint);
            const done = await store.complete(intent, checkpoint, signal); check();
            if (jobValueHash(parsePipelineJobReceipt(done)) !== jobValueHash({ intent, checkpoint })) throw new PipelineError('receipt_conflict', step);
          } catch (error) { check(); if (error instanceof PipelineError || error instanceof PipelineGateError) throw error; /* acceptance needs observation/reconciliation */ }
        }
        return submittedValue;
      }), { retry: { maxAttempts: 3, backoff: 'exponential' } });
      if (jobValueHash(submitted) !== jobValueHash(submittedValue)) throw new PipelineError('receipt_conflict', step);
      budget(submitted);
      for (let poll = 0; poll < polls; poll++) {
        claim();
        const state = await wf.step('pipeline/' + step + '/observe', () => operation(timeout, undefined, async (signal, check) => {
          const receipt = await load(wf.runId, step, owner, signal, check);
          if (!receipt || receipt.intent.values_sha256 !== valuesHash) throw new PipelineError('receipt_conflict', step);
          if (!receipt.checkpoint) return { state: 'submission_unresolved' as const };
          const handle = d.job.resume(receipt.checkpoint);
          const p = await handle.status({ timeoutMs: timeout, signal }); check();
          if (!['succeeded', 'failed', 'cancelled', 'dead_letter'].includes(p.status)) return { state: 'running' as const };
          if (p.status !== 'succeeded') throw new PipelineError('job_failed', step, p.runId);
          const results = await handle.results({ timeoutMs: timeout, signal, maxItems: 1000 }); check();
          if (results.length !== inputs.length || results.some(r => r.outcome !== 'succeeded')) throw new PipelineError('invalid_result', step, p.runId);
          const value = { state: 'succeeded' as const, result: { run_id: p.runId, results: results.map(r => {
            if (r.outcome !== 'succeeded') throw new PipelineError('invalid_result', step);
            return { input_id: r.inputId, index: r.index, attempt: r.attempt, result: d.contract.output(r.result) };
          }) } };
          if (Buffer.byteLength(JSON.stringify(value)) > bytesLimit) throw new PipelineError('journal_limit', step);
          return value;
        }), { retry: { maxAttempts: 3, backoff: 'exponential' } });
        budget(state);
        if (state.state === 'succeeded') {
          if (!state.result || state.result.results.length !== inputs.length || state.result.results.some((r, n) => r.input_id !== inputs[n]!.id || r.index !== n || !Number.isSafeInteger(r.attempt) || r.attempt < 1)) throw new PipelineError('invalid_result', step);
          for (const item of state.result.results) d.contract.output(item.result);
          return jobSnapshot(state.result);
        }
        claim(); await wf.sleep('pipeline/' + step + '/wait', delay);
      }
      throw new PipelineError('poll_limit', step);
    } };
    const output = contract.output(await run(input, context));
    if (pendingGates) throw new PipelineGateError('pending');
    if (composing) { blocked = true; throw new PipelineChildrenError('blocked'); }
    budget(output);
    return jobSnapshot({ version: 1, contract_sha256: contract.sha256, scope, owner_id: owner, output });
  });
  return Object.freeze({ definition,
    reconcile(runId, step, jobRunId, owner, settings) {
      if (!Object.hasOwn(jobs, step)) throw new PipelineError('invalid_input');
      return operation(settings.timeoutMs, settings.signal, async (signal, check) => {
        const receipt = await load(runId, step, owner, signal, check); if (!receipt) throw new PipelineError('invalid_input');
        const accepted = await jobs[step]!.job.resumeById(jobRunId, { timeoutMs: settings.timeoutMs, signal, maxItems: 1000 }); check();
        const checkpoint = matchPipelineJob(receipt.intent, accepted.checkpoint);
        const result = await store.complete(receipt.intent, checkpoint, signal); check();
        const saved = owned(parsePipelineJobReceipt(result), runId, step, owner);
        if (saved.checkpoint?.run_id !== jobRunId || jobValueHash(saved.intent) !== jobValueHash(receipt.intent)) throw new PipelineError('receipt_conflict'); return saved;
      });
    },
    inspectJob(runId, step, owner, settings) {
      if (!Object.hasOwn(jobs, step)) throw new PipelineError('invalid_input');
      return operation(settings.timeoutMs, settings.signal, async (signal, check) => {
        const receipt = await load(runId, step, owner, signal, check);
        const progress = receipt?.checkpoint ? await jobs[step]!.job.resume(receipt.checkpoint).status({ timeoutMs: settings.timeoutMs, signal }) : null; check();
        return { receipt, progress };
      });
    },
  } satisfies JobPipeline<I, O, J>);
}
