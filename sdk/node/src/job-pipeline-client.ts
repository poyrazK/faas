import { createClient } from './isolated-client-preview.js';
import { JobContract, jobScope, jobSnapshot, jobValueHash } from './job-runtime.js';
import { PipelineError, pipelineScope, pipelineName, pipelineFailure, parsePipelineLineage, parsePipelineTrigger, type PipelineTrigger, type PipelineLineage, type PipelineScope, type PipelineEnvelope } from './pipeline-state.js';
import { fileEventUUID } from './file-events.js';

export interface PipelineRunOptions { timeoutMs: number; signal?: AbortSignal; }
export interface PipelineCheckpoint {
  readonly version: 1; readonly run_id: string; readonly scope: PipelineScope;
  readonly owner_id: string; readonly contract_sha256: string; readonly input_sha256: string;
}
export interface PipelineInspection {
  readonly runId: string;
  readonly status: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead';
  readonly cancelled: boolean;
  readonly steps: readonly { readonly name: string; readonly status: string; readonly attempt: number }[];
  readonly failedStep?: string;
  readonly failure?: ReturnType<typeof pipelineFailure>;
}
export interface TypedPipelineRun<O> {
  readonly checkpoint: PipelineCheckpoint;
  inspect(options: PipelineRunOptions): Promise<PipelineInspection>;
  result(options: PipelineRunOptions): Promise<O>;
  /** Cancels native orchestration. Already accepted Jobs continue independently. */
  cancel(options: PipelineRunOptions): Promise<PipelineInspection>;
}
export interface TypedJobPipeline<I, O> {
  start(input: I, options: PipelineRunOptions & { idempotencyKey: string; lineage?: PipelineLineage; trigger?: PipelineTrigger }): Promise<TypedPipelineRun<O>>;
  /** Attach to an existing run. Does not resume a failed native code workflow. */
  resumeById(runId: string, options: PipelineRunOptions): Promise<TypedPipelineRun<O>>;
  resume(checkpoint: PipelineCheckpoint): TypedPipelineRun<O>;
}
/** A private isolated native client, with no mutable ambient SDK configuration.
 * Application servers must authorize ownerId before creating this handle.
 */
export function createJobPipelineClient<I, O>(options: {
  baseURL: string; token: string; scope: PipelineScope; ownerId: string; contract: JobContract<I, O>; fetch?: typeof fetch;
}): TypedJobPipeline<I, O> {
  const scope = pipelineScope(options.scope), owner = options.ownerId, contract = options.contract;
  if (options.baseURL !== scope.base_url || !pipelineName(owner)) throw new PipelineError('scope_mismatch');
  const client = createClient(options.baseURL, { token: options.token, ...(options.fetch ? { fetch: options.fetch } : {}) });
  async function execute<T>(settings: PipelineRunOptions, work: (signal: AbortSignal, check: () => void) => Promise<T>): Promise<T> {
    const deadline = performance.now() + settings.timeoutMs;
    return jobScope(settings.timeoutMs, settings.signal, async signal => {
      const check = () => { signal.throwIfAborted(); if (performance.now() >= deadline) throw new PipelineError('deadline'); };
      try { check(); const value = await work(signal, check); check(); return value; }
      catch (error) { signal.throwIfAborted(); if (error instanceof PipelineError) throw error; throw new PipelineError('request_failed'); }
    });
  }
  async function verify(signal: AbortSignal, check: () => void): Promise<void> {
    const policy = { timeoutMs: 0, signal, retry: false as const };
    const account = await client.account.getAccount(undefined, policy); check();
    if (account.id !== scope.account_id) throw new PipelineError('scope_mismatch');
    const app = await client.apps.getApp({ slug: scope.app_slug }, policy); check();
    if (app.id !== scope.app_id) throw new PipelineError('scope_mismatch');
  }
  function envelope(value: unknown): PipelineEnvelope<I> {
    const v = value as PipelineEnvelope<I>;
    if (!v || v.version !== 1 || v.owner_id !== owner || v.contract_sha256 !== contract.sha256 || jobValueHash(v.scope) !== jobValueHash(scope)) throw new PipelineError('scope_mismatch');
    return jobSnapshot({ ...(v.lineage !== undefined ? { lineage: parsePipelineLineage(v.lineage) } : {}), ...(v.trigger !== undefined ? { trigger: parsePipelineTrigger(v.trigger) } : {}), version: 1, scope, owner_id: owner, contract_sha256: contract.sha256, input: contract.input(v.input) });
  }
  function checkpoint(runId: string, input: unknown): PipelineCheckpoint {
    if (!fileEventUUID(runId)) throw new PipelineError('receipt_conflict');
    return jobSnapshot({ version: 1, run_id: runId, scope, owner_id: owner, contract_sha256: contract.sha256, input_sha256: jobValueHash(envelope(input)) });
  }
  function validateRun(raw: unknown, c?: PipelineCheckpoint): Record<string, any> {
    const r = raw as Record<string, any>;
    if (!r || !fileEventUUID(r.id) || r.app_id !== scope.app_id || r.workflow_name !== contract.spec.name || r.platform_tenant_id != null || !['pending', 'running', 'awaiting_event', 'succeeded', 'failed', 'dead'].includes(r.status)) throw new PipelineError('receipt_conflict');
    const next = checkpoint(r.id, r.input);
    if (c && jobValueHash(c) !== jobValueHash(next)) throw new PipelineError('receipt_conflict');
    return r;
  }
  function make(c: PipelineCheckpoint): TypedPipelineRun<O> {
    if (Object.keys(c).sort().join() !== 'contract_sha256,input_sha256,owner_id,run_id,scope,version' || c.version !== 1 || jobValueHash(c.scope) !== jobValueHash(scope) || c.owner_id !== owner || c.contract_sha256 !== contract.sha256 || !fileEventUUID(c.run_id) || !/^[a-f0-9]{64}$/.test(c.input_sha256)) throw new PipelineError('receipt_conflict');
    c = jobSnapshot(c);
    async function read(signal: AbortSignal, check: () => void) { await verify(signal, check); const raw = await client.workflows.getWorkflowRun({ id: c.run_id }, { timeoutMs: 0, signal, retry: false }); check(); return validateRun(raw, c); }
    async function inspect(r: Record<string, any>, signal: AbortSignal): Promise<PipelineInspection> {
      const value = await client.workflows.listWorkflowSteps({ id: c.run_id }, { timeoutMs: 0, signal, retry: false }); signal.throwIfAborted();
      if (!Array.isArray(value.steps) || value.steps.length > 1000 || value.steps.some(s => typeof s.step_name !== 'string' || !['pending', 'running', 'awaiting_event', 'succeeded', 'failed', 'dead', 'skipped'].includes(s.status) || !Number.isSafeInteger(s.attempt) || s.attempt < 0)) throw new PipelineError('receipt_conflict');
      const steps = value.steps.map(s => ({ name: s.step_name, status: s.status, attempt: s.attempt }));
      const failedStep = steps.find(s => ['failed', 'dead'].includes(s.status))?.name;
      const failure = pipelineFailure(value.steps.find(s => s.step_name === failedStep)?.error) ?? pipelineFailure(r.last_error);
      return jobSnapshot({ runId: c.run_id, status: r.status, cancelled: !!r.cancelled_at, steps, ...(failedStep ? { failedStep } : {}), ...(failure ? { failure } : {}) });
    }
    return Object.freeze({ checkpoint: c,
      inspect: settings => execute(settings, async (signal, check) => inspect(await read(signal, check), signal)),
      result: settings => execute(settings, async (signal, check) => {
        const r = await read(signal, check);
        if (r.status !== 'succeeded' || r.cancelled_at) throw new PipelineError('invalid_result');
        const value = r.output;
        if (!value || value.version !== 1 || value.contract_sha256 !== contract.sha256 || value.owner_id !== owner || jobValueHash(value.scope) !== jobValueHash(scope)) throw new PipelineError('receipt_conflict');
        return contract.output(value.output);
      }),
      cancel: settings => execute(settings, async (signal, check) => {
        await read(signal, check); check();
        const r = validateRun(await client.workflows.cancelWorkflowRun({ id: c.run_id }, { timeoutMs: 0, signal, retry: false }), c);
        return inspect(r, signal);
      }),
    } satisfies TypedPipelineRun<O>);
  }
  return Object.freeze({
    start(input, settings) {
      const captured = contract.input(input), key = settings.idempotencyKey, lineage = settings.lineage === undefined ? undefined : parsePipelineLineage(settings.lineage), trigger = settings.trigger === undefined ? undefined : parsePipelineTrigger(settings.trigger);
      if (typeof key !== 'string' || !/^[!-~]{1,128}$/.test(key)) throw new PipelineError('invalid_input');
      return execute(settings, async (signal, check) => {
        await verify(signal, check); check();
        const value: PipelineEnvelope<I> = { ...(lineage !== undefined ? { lineage } : {}), ...(trigger !== undefined ? { trigger } : {}), version: 1, contract_sha256: contract.sha256, scope, owner_id: owner, input: captured };
        let accepted;
        try { accepted = await client.workflows.createWorkflowRun({ slug: scope.app_slug, name: contract.spec.name, idempotencyKey: key, requestBody: value }, { timeoutMs: 0, signal, retry: false }); }
        catch { signal.throwIfAborted(); throw new PipelineError('acceptance_unknown'); }
        check(); validateRun(accepted);
        if (jobValueHash(envelope(accepted.input)) !== jobValueHash(value)) throw new PipelineError('receipt_conflict');
        return make(checkpoint(accepted.id, accepted.input));
      });
    },
    resumeById(runId, settings) {
      if (!fileEventUUID(runId)) throw new PipelineError('invalid_input');
      return execute(settings, async (signal, check) => { await verify(signal, check); const r = validateRun(await client.workflows.getWorkflowRun({ id: runId }, { timeoutMs: 0, signal, retry: false })); check(); if (r.id !== runId) throw new PipelineError('receipt_conflict'); return make(checkpoint(r.id, r.input)); });
    },
    resume: make,
  } satisfies TypedJobPipeline<I, O>);
}
