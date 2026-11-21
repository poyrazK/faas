import { JobContract, jobSnapshot, jobValueHash, jobScope, type JobInput } from './job-runtime.js';
import { createJobPipelineClient, type PipelineCheckpoint, type PipelineInspection } from './job-pipeline-client.js';
import { pipelineScope, type PipelineScope, type PipelineEnvelope } from './pipeline-state.js';
import type { WorkflowContext } from './workflows.js';
import type { PipelineGateStore } from './pipeline-gates.js';
import { parseLiveJobSnapshot, type LiveJobSnapshot } from './live-jobs.js';
import { PipelineChildrenError, pipelineChildrenIntent, pipelineChildrenIdentity, pipelineChildLineage, parsePipelineChildrenReceipt, matchPipelineChild, type PipelineChildrenStore, type PipelineChildrenIntent, type PipelineChildrenReceipt, type PipelineChildrenState, type PipelineChildState } from './pipeline-children-state.js';
import type { PipelineChildPolicy, PipelineBatchResult, PipelineChildView } from './pipeline-children-types.js';
export * from './pipeline-children-state.js';
export type * from './pipeline-children-types.js';

export interface PipelineChild<I, O> { readonly contract: JobContract<I, O>; }
export interface PipelineChildrenTransport {
  parent(intent: PipelineChildrenIntent, signal: AbortSignal): Promise<PipelineInspection>;
  start(intent: PipelineChildrenIntent, index: number, signal: AbortSignal): Promise<PipelineCheckpoint>;
  attach(intent: PipelineChildrenIntent, index: number, runId: string, signal: AbortSignal): Promise<PipelineCheckpoint>;
  observe(intent: PipelineChildrenIntent, index: number, checkpoint: PipelineCheckpoint, signal: AbortSignal): Promise<PipelineChildState>;
  cancel(intent: PipelineChildrenIntent, index: number, checkpoint: PipelineCheckpoint, signal: AbortSignal): Promise<void>;
}
/** Uses isolated typed clients for every parent/child provenance check. */
export function nativePipelineChildrenTransport(options: { scope: PipelineScope; parentContract: JobContract<any, any>; children: readonly PipelineChild<any, any>[]; token: string; timeoutMs?: number; fetch?: typeof fetch; gateStore?: PipelineGateStore }): PipelineChildrenTransport {
  const scope = pipelineScope(options.scope), token = options.token, timeout = options.timeoutMs ?? 10000, fetcher = options.fetch;
  const parentContract = options.parentContract, contracts = new Map(options.children.map(d => [d.contract.sha256, d.contract]));
  const client = (i: PipelineChildrenIntent, parent = false) => {
    const contract = parent ? parentContract : contracts.get(i.child_sha256);
    if (!contract || i.parent_sha256 !== parentContract.sha256 || jobValueHash(i.scope) !== jobValueHash(scope) || !parent && contract.spec.name !== i.child_workflow) throw new PipelineChildrenError('forbidden');
    return createJobPipelineClient({ baseURL: scope.base_url, scope, token, ownerId: i.owner_id, contract, fetch: fetcher });
  };
  return {
    parent: (i, signal) => client(i, true).resume({ version: 1, run_id: i.parent_run_id, scope, owner_id: i.owner_id, contract_sha256: i.parent_sha256, input_sha256: i.parent_input_sha256 }).inspect({ timeoutMs: timeout, signal }),
    async start(i, n, signal) { return (await client(i).start(i.inputs[n]!.value, { timeoutMs: timeout, signal, idempotencyKey: 'pipeline-child:' + i.identity + ':' + n, lineage: pipelineChildLineage(i, n) })).checkpoint; },
    async attach(i, n, runId, signal) { return matchPipelineChild(i, n, (await client(i).resumeById(runId, { timeoutMs: timeout, signal })).checkpoint); },
    async observe(i, n, checkpoint, signal) {
      const run = client(i).resume(matchPipelineChild(i, n, checkpoint)), p = await run.inspect({ timeoutMs: timeout, signal });
      if (p.cancelled) return { phase: 'cancelled', checkpoint, cancel_requested: false, native_status: p.status };
      if (p.status === 'succeeded') return { phase: 'succeeded', checkpoint, cancel_requested: false, native_status: p.status, output: await run.result({ timeoutMs: timeout, signal }) };
      return { phase: ['failed', 'dead'].includes(p.status) ? 'failed' : 'running', checkpoint, cancel_requested: false, native_status: p.status };
    },
    async cancel(i, n, checkpoint, signal) { await client(i).resume(matchPipelineChild(i, n, checkpoint)).cancel({ timeoutMs: timeout, signal }); },
  };
}

class ChildrenContentionError extends Error { readonly code = 'contention'; constructor() { super('pipeline-children:contention'); } }
const terminal = (c: PipelineChildState) => ['succeeded', 'failed', 'cancelled'].includes(c.phase);
type Settings = { timeoutMs: number; signal?: AbortSignal };
export class PipelineChildren {
  readonly scope: PipelineScope;
  private readonly contracts: Map<PipelineChild<any, any>, JobContract<any, any>>;
  private readonly transport: PipelineChildrenTransport;
  private readonly store: PipelineChildrenStore;
  private readonly parent: JobContract<any, any>;
  private readonly timeout: number;
  private readonly gateStore?: PipelineGateStore;
  constructor(options: { scope: PipelineScope; parentContract: JobContract<any, any>; children: readonly PipelineChild<any, any>[]; store: PipelineChildrenStore; transport?: PipelineChildrenTransport; token?: string; requestTimeoutMs?: number; fetch?: typeof fetch; gateStore?: PipelineGateStore }) {
    this.scope = pipelineScope(options.scope); this.parent = options.parentContract; this.store = options.store; this.timeout = options.requestTimeoutMs ?? 10000; this.gateStore = options.gateStore;
    this.contracts = new Map(options.children.map(d => [d, d.contract]));
    if (!this.contracts.size || this.contracts.size > 50 || new Set([...this.contracts.values()].map(c => c.sha256)).size !== this.contracts.size || !Number.isSafeInteger(this.timeout) || this.timeout < 1 || !options.transport && !options.token) throw new PipelineChildrenError('invalid_input');
    this.transport = options.transport ?? nativePipelineChildrenTransport({ scope: this.scope, parentContract: this.parent, children: options.children, token: options.token!, timeoutMs: this.timeout, fetch: options.fetch });
  }
  private contract(i: PipelineChildrenIntent): JobContract<any, any> {
    const c = [...this.contracts.values()].find(c => c.sha256 === i.child_sha256 && c.spec.name === i.child_workflow);
    if (!c || i.parent_sha256 !== this.parent.sha256 || jobValueHash(i.scope) !== jobValueHash(this.scope)) throw new PipelineChildrenError('forbidden'); return c;
  }
  private owned(raw: PipelineChildrenReceipt, owner?: string): PipelineChildrenReceipt {
    const r = parsePipelineChildrenReceipt(raw), c = this.contract(r.intent);
    if (owner !== undefined && owner !== r.intent.owner_id) throw new PipelineChildrenError('forbidden');
    for (const input of r.intent.inputs) c.input(input.value);
    for (const child of r.state.children) if (child.phase === 'succeeded') c.output(child.output);
    return r;
  }
  private execute<T>(settings: Settings, fn: (signal: AbortSignal) => Promise<T>): Promise<T> {
    return jobScope(settings.timeoutMs, settings.signal, async signal => {
      try { const value = await fn(signal); signal.throwIfAborted(); return value; }
      catch (e) { signal.throwIfAborted(); if (e instanceof PipelineChildrenError || e instanceof ChildrenContentionError) throw e; throw new Error('pipeline-children:request_failed'); }
    });
  }
  private async load(id: string, signal: AbortSignal): Promise<PipelineChildrenReceipt> {
    const r = await this.store.load(id, signal); signal.throwIfAborted(); if (!r || r.intent.identity !== id) throw new PipelineChildrenError('conflict'); return this.owned(r);
  }
  private async change(id: string, fn: (r: PipelineChildrenReceipt) => PipelineChildrenState | null, signal: AbortSignal, admission = false): Promise<{ saved: boolean; receipt: PipelineChildrenReceipt }> {
    for (let attempt = 0; attempt < 8; attempt++) {
      const r = await this.load(id, signal), state = fn(r); signal.throwIfAborted();
      if (!state) return { saved: false, receipt: r };
      const saved = await this.store.save(r, state, admission, signal); signal.throwIfAborted();
      if (saved) { const checked = this.owned(saved); if (checked.revision !== r.revision + 1 || jobValueHash(checked.intent) !== jobValueHash(r.intent) || jobValueHash(checked.state) !== jobValueHash(state)) throw new PipelineChildrenError('conflict'); return { saved: true, receipt: checked }; }
    }
    throw new ChildrenContentionError();
  }
  private async complete(i: PipelineChildrenIntent, n: number, checkpoint: PipelineCheckpoint, signal: AbortSignal) {
    checkpoint = matchPipelineChild(i, n, checkpoint);
    return this.change(i.identity, r => {
      const c = r.state.children[n]!;
      if (c.checkpoint) { if (jobValueHash(c.checkpoint) !== jobValueHash(checkpoint)) throw new PipelineChildrenError('conflict'); return null; }
      if (c.phase !== 'submission_unresolved') throw new PipelineChildrenError('conflict');
      return { ...r.state, children: r.state.children.map((v, index) => index === n ? { ...v, phase: 'running', checkpoint } : v) };
    }, signal);
  }
  private async round(id: string, signal: AbortSignal, admit: boolean, guard: () => void): Promise<PipelineChildrenReceipt> {
    let r = await this.load(id, signal);
    const parent = await this.transport.parent(r.intent, signal); signal.throwIfAborted();
    const stopped = parent.cancelled || ['succeeded', 'failed', 'dead'].includes(parent.status);
    await this.change(id, current => {
      const stop = current.state.stop_reason ?? (stopped ? 'parent_stopped' : current.expired ? 'deadline' : null);
      return stop !== current.state.stop_reason || stopped && !current.state.parent_stopped ? { ...current.state, stop_reason: stop, parent_stopped: current.state.parent_stopped || stopped } : null;
    }, signal);
    for (let n = 0; n < r.intent.inputs.length; n++) {
      r = await this.load(id, signal); const child = r.state.children[n]!;
      if (child.phase !== 'running' || !child.checkpoint) continue;
      const observed = await this.transport.observe(r.intent, n, child.checkpoint, signal); signal.throwIfAborted();
      matchPipelineChild(r.intent, n, observed.checkpoint!);
      if (observed.phase === 'succeeded') this.contract(r.intent).output(observed.output);
      await this.change(id, current => {
        const old = current.state.children[n]!; if (terminal(old)) return null;
        if (jobValueHash(old.checkpoint) !== jobValueHash(observed.checkpoint)) throw new PipelineChildrenError('conflict');
        const next = { ...observed, cancel_requested: old.cancel_requested };
        return { ...current.state, stop_reason: current.state.stop_reason ?? (['failed', 'cancelled'].includes(next.phase) && current.intent.failure_policy === 'fail_fast' ? 'child_failed' : null), children: current.state.children.map((v, index) => index === n ? next : v) };
      }, signal);
    }
    r = await this.load(id, signal);
    const cancel = r.state.parent_stopped && r.intent.on_parent_stopped === 'cancel_children' || r.state.stop_reason === 'child_failed' && r.intent.on_fail_fast === 'cancel_children' || r.state.stop_reason === 'deadline' && r.intent.on_deadline === 'cancel_children';
    if (cancel) for (let n = 0; n < r.intent.inputs.length; n++) {
      r = await this.load(id, signal); const child = r.state.children[n]!;
      if (child.phase !== 'running' || !child.checkpoint) continue;
      await this.change(id, current => current.state.children[n]!.cancel_requested ? null : { ...current.state, children: current.state.children.map((c, index) => index === n ? { ...c, cancel_requested: true } : c) }, signal);
      // Native cancellation is explicitly idempotent on terminal workflow runs.
      await this.transport.cancel(r.intent, n, child.checkpoint, signal); signal.throwIfAborted();
    }
    if (admit) for (let n = 0; n < r.intent.inputs.length; n++) {
      guard(); signal.throwIfAborted();
      const reserved = await this.change(id, current => {
        if (current.expired || current.state.stop_reason || current.state.children[n]!.phase !== 'unstarted' || current.state.children.filter(c => ['running', 'submission_unresolved'].includes(c.phase)).length >= current.intent.concurrency) return null;
        return { ...current.state, children: current.state.children.map((v, index) => index === n ? { phase: 'submission_unresolved', checkpoint: null, cancel_requested: false } : v) };
      }, signal, true);
      if (!reserved.saved) continue;
      guard(); signal.throwIfAborted();
      const current = await this.load(id, signal);
      if (current.expired || current.state.stop_reason) {
        // This driver owns the acknowledged reservation and has not sent POST.
        await this.change(id, latest => latest.state.children[n]!.phase !== 'submission_unresolved' ? null : { ...latest.state, stop_reason: latest.state.stop_reason ?? 'deadline', children: latest.state.children.map((v, index) => index === n ? { phase: 'unstarted', checkpoint: null, cancel_requested: false } : v) }, signal);
        continue;
      }
      if (current.state.children[n]!.phase !== 'submission_unresolved') continue;
      try {
        const checkpoint = await this.transport.start(reserved.receipt.intent, n, signal); signal.throwIfAborted();
        await this.complete(reserved.receipt.intent, n, checkpoint, signal);
      } catch (e) { signal.throwIfAborted(); if (e instanceof PipelineChildrenError || e instanceof ChildrenContentionError) throw e; /* never repeat uncertain starts */ }
    }
    return this.load(id, signal);
  }
  private snapshot<O>(r: PipelineChildrenReceipt): PipelineBatchResult<O> {
    this.owned(r);
    return jobSnapshot({ identity: r.intent.identity, parent_run_id: r.intent.parent_run_id, batch: r.intent.batch, stop_reason: r.state.stop_reason ?? (r.expired ? 'deadline' : null), complete: r.state.children.every(c => terminal(c) || c.phase === 'unstarted' && !!(r.state.stop_reason || r.expired)), children: r.state.children.map((c, n) => ({ id: r.intent.inputs[n]!.id, phase: c.phase === 'unstarted' && (r.state.stop_reason || r.expired) ? 'skipped' : c.phase, run_id: c.checkpoint?.run_id ?? null, cancel_requested: c.cancel_requested, ...(c.native_status ? { native_status: c.native_status } : {}), ...(c.phase === 'succeeded' ? { output: this.contract(r.intent).output(c.output) as O } : {}), ...(c.phase === 'failed' ? { error: 'child_failed' as const } : {}) })) });
  }
  /** Internal workflow adapter. Calls must be awaited sequentially; batches own fan-out. */
  async run<I, O>(wf: WorkflowContext, envelope: PipelineEnvelope<unknown>, name: string, definition: PipelineChild<I, O>, inputs: readonly JobInput<I>[], policy: PipelineChildPolicy, hooks: { claim(): void; budget(value: unknown): unknown; guard(): void; polls: number; delay: number }): Promise<PipelineBatchResult<O>> {
    const contract = this.contracts.get(definition); if (!contract || envelope.contract_sha256 !== this.parent.sha256 || jobValueHash(envelope.scope) !== jobValueHash(this.scope)) throw new PipelineChildrenError('forbidden');
    const intent = pipelineChildrenIntent({ scope: this.scope, parent_run_id: wf.runId, owner_id: envelope.owner_id, batch: name, parent_sha256: this.parent.sha256, parent_input_sha256: jobValueHash({ ...envelope, input: this.parent.input(envelope.input) }), child_sha256: contract.sha256, child_workflow: contract.spec.name, inputs: inputs.map(i => { const value = contract.input(i.value); return { id: i.id, value, input_sha256: jobValueHash({ lineage: { version: 1, parent_run_id: wf.runId, batch_identity: pipelineChildrenIdentity(this.scope, wf.runId, name), item_id: i.id }, version: 1, scope: this.scope, owner_id: envelope.owner_id, contract_sha256: contract.sha256, input: value }) }; }), concurrency: policy.concurrency ?? 1, timeout_ms: policy.timeoutMs, failure_policy: policy.failurePolicy ?? 'collect', on_fail_fast: policy.onFailFast ?? 'leave_running', on_parent_stopped: policy.onParentStopped ?? 'leave_running', on_deadline: policy.onDeadline ?? 'leave_running' });
    const retry = { retry: { maxAttempts: 3, backoff: 'exponential' as const } }, binding = jobValueHash(intent);
    hooks.claim();
    const opened = await wf.step('pipeline-children/' + name + '/open', () => this.execute({ timeoutMs: this.timeout }, async signal => {
      hooks.guard(); await this.transport.parent(intent, signal); signal.throwIfAborted();
      const r = this.owned(await this.store.open(intent, signal)); if (jobValueHash(r.intent) !== binding) throw new PipelineChildrenError('conflict');
      return { identity: intent.identity, binding_sha256: binding };
    }), retry);
    if (opened.identity !== intent.identity || opened.binding_sha256 !== binding) throw new PipelineChildrenError('conflict'); hooks.budget(opened);
    for (let poll = 0; poll < hooks.polls; poll++) {
      hooks.claim();
      const result = await wf.step('pipeline-children/' + name + '/round', () => this.execute({ timeoutMs: this.timeout }, async signal => {
        const current = await this.load(intent.identity, signal); if (jobValueHash(current.intent) !== binding) throw new PipelineChildrenError('conflict');
        const view = this.snapshot<O>(await this.round(intent.identity, signal, true, hooks.guard));
        return view.complete || view.stop_reason ? view : { identity: intent.identity, complete: false as const, stop_reason: null };
      }), retry);
      if (result.identity !== intent.identity) throw new PipelineChildrenError('conflict');
      hooks.budget(result);
      if ('children' in result) {
        if (result.children.length !== inputs.length || result.children.some((c, n) => c.id !== inputs[n]!.id)) throw new PipelineChildrenError('conflict');
        result.children.forEach(c => { if (c.phase === 'succeeded') contract.output(c.output); });
        if (result.complete || result.stop_reason) return jobSnapshot(result);
      } else if (result.complete || result.stop_reason) throw new PipelineChildrenError('conflict');
      hooks.claim(); await wf.sleep('pipeline-children/' + name + '/wait', hooks.delay);
    }
    throw new PipelineChildrenError('poll_limit');
  }
  inspect<O>(parentRunId: string, batch: string, owner: string, definition: PipelineChild<any, O>, settings: Settings): Promise<PipelineBatchResult<O>> {
    return this.execute(settings, async signal => {
      const r = this.owned(await this.load(pipelineChildrenIdentity(this.scope, parentRunId, batch), signal), owner);
      if (this.contracts.get(definition)?.sha256 !== r.intent.child_sha256) throw new PipelineChildrenError('forbidden');
      await this.transport.parent(r.intent, signal); signal.throwIfAborted();
      const children = [...r.state.children];
      for (let n = 0; n < children.length; n++) if (children[n]!.checkpoint && !terminal(children[n]!)) { children[n] = { ...await this.transport.observe(r.intent, n, children[n]!.checkpoint!, signal), cancel_requested: children[n]!.cancel_requested }; signal.throwIfAborted(); }
      let view = this.snapshot<O>({ ...r, state: { ...r.state, children } });
      if (this.gateStore) {
        const detailed: PipelineChildView<O>[] = [];
        for (const c of view.children) {
          let waiting: PipelineChildView<O>['waiting'];
          if (c.run_id && c.native_status === 'awaiting_event') {
            const gates = await this.gateStore.list(c.run_id, owner, signal); signal.throwIfAborted();
            const g = gates.find(g => !g.decision && g.intent.pipeline_sha256 === r.intent.child_sha256 && g.intent.owner_id === owner && jobValueHash(g.intent.scope) === jobValueHash(this.scope));
            if (g) waiting = { kind: g.intent.kind, gate_id: g.intent.id, name: g.intent.name };
          }
          detailed.push({ ...c, ...(waiting ? { waiting } : {}) });
        }
        view = jobSnapshot({ ...view, children: detailed });
      }
      return view;
    });
  }
  /** Backend recovery only: observes/cancels accepted children, never starts one. */
  recover(limit: number, settings: Settings): Promise<number> {
    return this.execute(settings, async signal => {
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 100) throw new PipelineChildrenError('invalid_input');
      const rows = await this.store.scan(this.scope, this.parent.sha256, limit, signal); signal.throwIfAborted();
      if (rows.length > limit) throw new PipelineChildrenError('conflict');
      for (const row of rows) { this.owned(row); await this.round(row.intent.identity, signal, false, () => {}); signal.throwIfAborted(); }
      return rows.length;
    });
  }
  reconcile(parentRunId: string, batch: string, itemId: string, childRunId: string, owner: string, settings: Settings): Promise<void> {
    return this.execute(settings, async signal => {
      const r = this.owned(await this.load(pipelineChildrenIdentity(this.scope, parentRunId, batch), signal), owner), n = r.intent.inputs.findIndex(i => i.id === itemId);
      if (n < 0) throw new PipelineChildrenError('invalid_input');
      await this.transport.parent(r.intent, signal); signal.throwIfAborted();
      const c = await this.transport.attach(r.intent, n, childRunId, signal); signal.throwIfAborted(); await this.complete(r.intent, n, c, signal);
    });
  }
}

/** Authorize the root owner first. Full child detail comes from inspect(). */
export function pipelineChildrenLiveSnapshot<O>(view: PipelineBatchResult<O>, parent: PipelineInspection, resourceId: string): LiveJobSnapshot {
  const count = (p: string) => view.children.filter(c => c.phase === p).length;
  if (view.parent_run_id !== parent.runId) throw new PipelineChildrenError('conflict');
  return parseLiveJobSnapshot({ version: 1, resource_id: resourceId, run_id: parent.runId, status: parent.cancelled ? 'cancelled' : parent.status === 'dead' ? 'dead_letter' : ['succeeded', 'failed'].includes(parent.status) ? parent.status : 'running', total: view.children.length, succeeded: count('succeeded'), failed: count('failed'), cancelled: count('cancelled'), running: count('running'), failures: view.children.filter(c => c.phase === 'failed').map(c => ({ input_id: c.id, code: 'child_failed' })), report: 'none', ...(!['succeeded', 'failed', 'dead'].includes(parent.status) && !parent.cancelled && view.children.some(c => c.waiting) ? { waiting: view.children.find(c => c.waiting)!.waiting } : {}) }, resourceId);
}
