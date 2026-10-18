// Internal preparation lifecycle. The host owns the platform capability and durable jobs.
import { acknowledgmentWait } from './event-acknowledgment-callbacks.js';
import { EventAcknowledgmentWorkerError, type EventAcknowledgmentWorkerOperationOptions } from './event-acknowledgment-protocol.js';
import { claimEventConsumerProcessing, type EventConsumerProcessingLease } from './event-consumer-processing.js';
import { EventConsumerCommitUnknownError, type EventConsumerPool, type EventConsumerRequest } from './event-consumer-transactions.js';
import type { EventAcknowledgmentPreparationEvent, PreparationEvents } from './event-acknowledgment-preparation-events.js';
import type { EventAcknowledgmentIntentRetryPolicy, EventAcknowledgmentIntentRetryBudget } from './event-acknowledgment-intent-retry-policy.js';

export interface EventAcknowledgmentPreparedOptions extends EventAcknowledgmentWorkerOperationOptions {
  /** Optional immutable policy bound during final intent staging. */
  retryPolicy?: EventAcknowledgmentIntentRetryPolicy;
  /** Optional finite claim budget, mutually exclusive with retryPolicy. */
  retryBudget?: EventAcknowledgmentIntentRetryBudget;
  /** Required whole milliseconds, 1..2147483647. Covers platform startup, claim and preparation, before the final transaction. */
  preparationTimeoutMs: number;
  /** Whole milliseconds, 3..2147483647; default 30000. Renewed serially at one third of this duration. */
  processingLeaseMs?: number;
  /** Synchronous, nonblocking best-effort observer. Throws and accidental async rejections are ignored. */
  onEvent?: (event: EventAcknowledgmentPreparationEvent) => void;
}
export interface PreparationSettings { readonly timeoutMs: number; readonly leaseMs: number; readonly intervalMs: number;
  readonly onEvent?: (event: EventAcknowledgmentPreparationEvent) => void }
export function preparationSettings(options: EventAcknowledgmentPreparedOptions): PreparationSettings {
  if (!options || typeof options !== 'object' || Array.isArray(options)) throw new TypeError('invalid acknowledgment preparation options');
  const timeoutMs = options.preparationTimeoutMs, leaseMs = options.processingLeaseMs === undefined ? 30000 : options.processingLeaseMs;
  const onEvent = options.onEvent;
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 2147483647
    || !Number.isInteger(leaseMs) || leaseMs < 3 || leaseMs > 2147483647) throw new TypeError('invalid acknowledgment preparation budget');
  if (onEvent !== undefined && typeof onEvent !== 'function') throw new TypeError('invalid acknowledgment preparation observer');
  return Object.freeze({ timeoutMs, leaseMs, intervalMs: Math.floor(leaseMs / 3), onEvent });
}
interface PreparationHost<T, R> {
  start(work: AbortController): Promise<() => Promise<void>>;
  replay(signal: AbortSignal): Promise<R>;
  complete(lease: EventConsumerProcessingLease, prepared: T, signal: AbortSignal): Promise<R>;
}
async function renewLocal(pool: EventConsumerPool, lease: EventConsumerProcessingLease, work: AbortController,
  settings: PreparationSettings, events: PreparationEvents): Promise<void> {
  try { await lease.renew(pool, settings.leaseMs, { signal: work.signal }); }
  catch (error) {
    const alreadyAborted = work.signal.aborted;
    work.abort(error);
    events.renewalFailed('local', error, alreadyAborted);
    throw error;
  }
}
function renewProcessing(pool: EventConsumerPool, lease: EventConsumerProcessingLease, work: AbortController,
  settings: PreparationSettings, events: PreparationEvents): () => Promise<void> {
  const waiting = new AbortController();
  const abort = () => waiting.abort();
  work.signal.addEventListener('abort', abort, { once: true });
  if (work.signal.aborted) abort();
  let failure: { error: unknown } | undefined;
  const done = (async () => {
    try {
      while (!waiting.signal.aborted) {
        try { await acknowledgmentWait(settings.intervalMs, waiting.signal); }
        catch (error) { if (waiting.signal.aborted) return; throw error; }
        if (waiting.signal.aborted) return;
        // Stopping the loop cancels waits, never an active renewal. Join it before completion.
        await renewLocal(pool, lease, work, settings, events);
      }
    } catch (error) { failure = { error }; work.abort(error); }
  })();
  return async () => {
    waiting.abort(); await done;
    work.signal.removeEventListener('abort', abort);
    if (failure) throw failure.error;
  };
}
export async function coordinateEventAcknowledgmentPreparation<T, R>(pool: EventConsumerPool, input: EventConsumerRequest,
  parent: AbortSignal | undefined, settings: PreparationSettings, prepare: (signal: AbortSignal) => Promise<T>,
  host: PreparationHost<T, R>, events: PreparationEvents): Promise<R> {
  const work = new AbortController();
  const abort = () => work.abort(parent?.reason);
  parent?.addEventListener('abort', abort, { once: true });
  if (parent?.aborted) abort();
  const deadline = performance.now() + settings.timeoutMs;
  const timeout = () => work.abort(new EventAcknowledgmentWorkerError('event_consumer_preparation_timeout'));
  const check = () => { if (performance.now() >= deadline) timeout(); work.signal.throwIfAborted(); };
  const timer = setTimeout(timeout, settings.timeoutMs);
  let stopLocal = async (): Promise<void> => {}, stopPlatform = async (): Promise<void> => {};
  try {
    check();
    events.stage('platform_start');
    stopPlatform = await host.start(work);
    check();
    events.stage('claim');
    const lease = await claimEventConsumerProcessing(pool, input, settings.leaseMs, { signal: work.signal });
    if (!lease.completed) events.claimAcquired();
    check();
    if (lease.completed) {
      clearTimeout(timer);
      events.stage('receipt_reconciliation');
      return await host.replay(work.signal);
    }
    events.stage('local_renewal');
    await renewLocal(pool, lease, work, settings, events);
    check();
    stopLocal = renewProcessing(pool, lease, work, settings, events);
    events.stage('preparation');
    const prepared = await events.prepare(() => prepare(work.signal));
    check();
    events.stage('local_handoff');
    await stopLocal();
    check();
    events.stage('local_renewal');
    await renewLocal(pool, lease, work, settings, events);
    check();
    clearTimeout(timer);
    events.stage('completion');
    // Platform renewal continues; local renewal has joined before the row-locking transaction.
    return await host.complete(lease, prepared, work.signal);
  } catch (error) {
    const reason = work.signal.aborted ? work.signal.reason : error;
    work.abort(error);
    let renewalFailure: unknown;
    try { await stopLocal(); } catch (failure) { renewalFailure = failure; }
    if (error instanceof EventConsumerCommitUnknownError) throw error;
    if (renewalFailure instanceof EventConsumerCommitUnknownError) throw renewalFailure;
    throw reason;
  } finally {
    clearTimeout(timer); work.abort();
    await Promise.allSettled([stopLocal(), stopPlatform()]);
    parent?.removeEventListener('abort', abort);
  }
}
