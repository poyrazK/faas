/** Authenticated CloudEvents -> typed native workflows, with one durable start intent. */
import { EventContract } from './event-contract.js';
import { JobContract, JobError, jobHash, jobScope, jobSnapshot, jobValueHash } from './job-runtime.js';
import { newEventConsumerRequest, eventConsumerUUID, type EventConsumerIdentity } from './event-consumer-transactions.js';
import { eventConsumerRequestMaxBytes } from './event-consumer-contract.js';
import { createJobPipelineClient, type PipelineCheckpoint, type PipelineRunOptions } from './job-pipeline-client.js';
import { PipelineError, pipelineName, type PipelineScope } from './pipeline-state.js';
import { EventPipelineError, eventPipelineIdentity, eventPipelineScope, matchEventPipelineCheckpoint, parseEventPipelineReceipt,
  type EventPipelineIntent, type EventPipelineReceipt, type EventPipelineStore } from './event-pipeline-state.js';
export * from './event-contract.js';
export * from './event-pipeline-state.js';

export interface EventPipelineDelivery {
  readonly headers: Readonly<Record<string, string | readonly string[] | undefined>>;
  readonly body: Uint8Array;
}
export interface PipelineEvent<E> {
  readonly identity: EventConsumerIdentity;
  readonly type: string;
  readonly data: E;
}
export type EventPipelineAction = 'start' | 'inspect' | 'reconcile';
export type EventPipelineOutcome =
  | { readonly state: 'started'; readonly receipt: EventPipelineReceipt & { readonly checkpoint: PipelineCheckpoint }; readonly replayed: boolean }
  | { readonly state: 'start_unknown'; readonly receipt: EventPipelineReceipt & { readonly checkpoint: null }; readonly replayed: boolean };
export interface EventPipeline<A> {
  handle(delivery: EventPipelineDelivery, access: A, options: PipelineRunOptions): Promise<EventPipelineOutcome>;
  inspect(delivery: EventPipelineDelivery, access: A, options: PipelineRunOptions): Promise<EventPipelineOutcome | null>;
  /** Read and verify the exact native run; never starts or restarts work. */
  reconcile(delivery: EventPipelineDelivery, runId: string, access: A, options: PipelineRunOptions): Promise<EventPipelineOutcome>;
}
export interface EventPipelineDefinition<E, I, O, A> {
  readonly name: string;
  readonly scope: PipelineScope;
  readonly subscriptionId: string;
  readonly source: string;
  readonly contract: JobContract<I, O>;
  readonly store: EventPipelineStore;
  readonly token: string;
  readonly fetch?: typeof fetch;
  /** Authenticate the route before trusting delivery headers. Header parsing alone is not authentication. */
  readonly verify: (delivery: EventPipelineDelivery, signal: AbortSignal) => Promise<EventConsumerIdentity>;
  /** Check current application authority on every start, duplicate, read and recovery. Never trust a payload owner claim. */
  readonly authorize: (event: PipelineEvent<E>, access: A, action: EventPipelineAction, signal: AbortSignal) => Promise<string | null>;
  /** Deterministic, side-effect-free mapping. Its validated result is bound to the retained intent. */
  readonly map: (event: PipelineEvent<E>, ownerId: string, signal: AbortSignal) => Promise<I>;
}
function capture(delivery: EventPipelineDelivery): EventPipelineDelivery {
  try {
    if (!(delivery.body instanceof Uint8Array) || !delivery.body.length || delivery.body.length > eventConsumerRequestMaxBytes) throw new Error();
    const headers = jobSnapshot(delivery.headers);
    return { headers, body: Uint8Array.from(delivery.body) };
  } catch { throw new EventPipelineError('invalid_event'); }
}
function outcome(receipt: EventPipelineReceipt, replayed: boolean): EventPipelineOutcome {
  return receipt.checkpoint === null ? { state: 'start_unknown', receipt: { ...receipt, checkpoint: null }, replayed }
    : { state: 'started', receipt: { ...receipt, checkpoint: receipt.checkpoint }, replayed };
}
export function createEventPipeline<E, I, O, A>(eventContract: EventContract<E>, definition: EventPipelineDefinition<E, I, O, A>): EventPipeline<A> {
  const d = { ...definition, scope: eventPipelineScope(definition.scope), subscriptionId: eventConsumerUUID(definition.subscriptionId) };
  if (!pipelineName(d.name) || !d.source || typeof d.token !== 'string' || !d.token) throw new EventPipelineError('invalid_event');
  const contract = d.contract, scope = d.scope;
  async function execute<T>(settings: PipelineRunOptions, work: (signal: AbortSignal, check: () => void, remaining: () => number) => Promise<T>): Promise<T> {
    const deadline = performance.now() + settings.timeoutMs;
    try {
      return await jobScope(settings.timeoutMs, settings.signal, async signal => {
        const check = () => { signal.throwIfAborted(); if (performance.now() >= deadline) throw new EventPipelineError('deadline'); };
        check();
        const value = await work(signal, check, () => { check(); return Math.max(1, Math.ceil(deadline - performance.now())); });
        check(); return value;
      });
    } catch (error) {
      if (error instanceof EventPipelineError) throw error;
      if (error instanceof JobError && error.code === 'deadline') throw new EventPipelineError('deadline');
      if (settings.signal?.aborted) throw new EventPipelineError('cancelled');
      if (error instanceof TypeError) throw error; // invalid timeout configuration
      throw new EventPipelineError('request_failed');
    }
  }
  async function stored<T>(signal: AbortSignal, check: () => void, work: () => Promise<T>): Promise<T> {
    check();
    try { const result = await work(); check(); return result; }
    catch (error) { check(); if (error instanceof EventPipelineError) throw error; throw new EventPipelineError('store_failed'); }
  }
  async function prepare(delivery: EventPipelineDelivery, access: A, action: EventPipelineAction, signal: AbortSignal, check: () => void) {
    const captured = capture(delivery);
    let identity: EventConsumerIdentity;
    try { identity = { ...await d.verify(capture(captured), signal) }; check(); }
    catch { check(); throw new EventPipelineError('unauthorized'); }
    let event: PipelineEvent<E>;
    try {
      const normalized = newEventConsumerRequest(identity, captured.body);
      const body = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(captured.body));
      if (normalized.accountId !== scope.account_id || normalized.appId !== scope.app_id || normalized.subscriptionId !== d.subscriptionId || normalized.source !== d.source) throw new EventPipelineError('scope_mismatch');
      if (body.type !== eventContract.spec.type) throw new EventPipelineError('invalid_event');
      event = jobSnapshot({ identity: normalized, type: body.type, data: eventContract.input(body.data) });
    } catch (error) { if (error instanceof EventPipelineError) throw error; throw new EventPipelineError('invalid_event'); }
    let owner: string | null;
    try { owner = await d.authorize(event, access, action, signal); check(); }
    catch { check(); throw new EventPipelineError('unauthorized'); }
    if (!pipelineName(owner)) throw new EventPipelineError('unauthorized');
    const id = eventPipelineIdentity(scope, d.name, d.subscriptionId, event.identity.acceptanceId);
    const binding: Omit<EventPipelineIntent, 'input_sha256'> = jobSnapshot({ version: 1, identity: id, scope, name: d.name,
      subscription_id: d.subscriptionId, acceptance_id: event.identity.acceptanceId, source: event.identity.source, event_id: event.identity.eventId,
      event_type: event.type, body_sha256: jobHash(captured.body), event_contract_sha256: eventContract.sha256,
      pipeline_sha256: contract.sha256, owner_id: owner, idempotency_key: 'event-pipeline:' + id });
    return { event, owner, binding };
  }
  function bound(binding: Omit<EventPipelineIntent, 'input_sha256'>, raw: unknown): EventPipelineReceipt {
    const receipt = parseEventPipelineReceipt(raw);
    if (Object.entries(binding).some(([key, value]) => jobValueHash(value) !== jobValueHash(receipt.intent[key as keyof EventPipelineIntent]))) throw new EventPipelineError('receipt_conflict');
    return receipt;
  }
  const client = (ownerId: string) => createJobPipelineClient({ baseURL: scope.base_url, scope, token: d.token, ownerId, contract, ...(d.fetch ? { fetch: d.fetch } : {}) });
  return Object.freeze({
    handle(delivery, access, settings) {
      const captured = capture(delivery);
      return execute(settings, async (signal, check, remaining) => {
        const { event, owner, binding } = await prepare(captured, access, 'start', signal, check);
        let input: I;
        try { input = contract.input(await d.map(event, owner, signal)); check(); }
        catch { check(); throw new EventPipelineError('invalid_event'); }
        const trigger = { version: 1 as const, kind: 'event' as const, identity: binding.identity };
        const intent: EventPipelineIntent = jobSnapshot({ ...binding, input_sha256: jobValueHash({ version: 1, scope, owner_id: owner, contract_sha256: contract.sha256, input, trigger }) });
        const reservation = await stored(signal, check, () => d.store.reserve(intent, signal));
        const receipt = bound(binding, reservation.receipt);
        if (typeof reservation.created !== 'boolean' || jobValueHash(receipt.intent) !== jobValueHash(intent)) throw new EventPipelineError('receipt_conflict');
        if (!reservation.created) return outcome(receipt, true);
        if (receipt.checkpoint !== null) throw new EventPipelineError('receipt_conflict');
        check();
        let checkpoint: PipelineCheckpoint;
        try { checkpoint = (await client(owner).start(input, { timeoutMs: remaining(), signal, idempotencyKey: intent.idempotency_key, trigger })).checkpoint; check(); }
        catch (error) {
          check();
          if (error instanceof PipelineError && error.code === 'scope_mismatch') throw new EventPipelineError('scope_mismatch');
          return outcome(receipt, false);
        }
        matchEventPipelineCheckpoint(intent, checkpoint);
        const completed = bound(binding, await stored(signal, check, () => d.store.complete(intent, checkpoint, signal)));
        if (jobValueHash(completed.checkpoint) !== jobValueHash(checkpoint)) throw new EventPipelineError('receipt_conflict');
        return outcome(completed, false);
      });
    },
    inspect(delivery, access, settings) {
      const captured = capture(delivery);
      return execute(settings, async (signal, check) => {
        const { binding } = await prepare(captured, access, 'inspect', signal, check);
        const receipt = await stored(signal, check, () => d.store.load(binding.identity, signal));
        return receipt === null ? null : outcome(bound(binding, receipt), true);
      });
    },
    reconcile(delivery, runId, access, settings) {
      const captured = capture(delivery);
      return execute(settings, async (signal, check, remaining) => {
        const { binding, owner } = await prepare(captured, access, 'reconcile', signal, check);
        const raw = await stored(signal, check, () => d.store.load(binding.identity, signal));
        if (raw === null) throw new EventPipelineError('receipt_conflict');
        const receipt = bound(binding, raw);
        if (receipt.checkpoint !== null) {
          if (receipt.checkpoint.run_id !== runId) throw new EventPipelineError('receipt_conflict');
          return outcome(receipt, true);
        }
        let checkpoint: PipelineCheckpoint;
        try { checkpoint = (await client(owner).resumeById(runId, { timeoutMs: remaining(), signal })).checkpoint; check(); }
        catch { check(); throw new EventPipelineError('receipt_conflict'); }
        matchEventPipelineCheckpoint(receipt.intent, checkpoint);
        const completed = bound(binding, await stored(signal, check, () => d.store.complete(receipt.intent, checkpoint, signal)));
        if (jobValueHash(completed.checkpoint) !== jobValueHash(checkpoint)) throw new EventPipelineError('receipt_conflict');
        return outcome(completed, true);
      });
    },
  } satisfies EventPipeline<A>);
}
