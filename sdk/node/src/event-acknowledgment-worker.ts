import { inspect } from 'node:util';
import { eventConsumerRequestFromHeaders, withEventConsumerTransaction, EventConsumerCommitUnknownError,
  type EventConsumerPool, type EventConsumerRequest, type EventConsumerTransaction, type EventConsumerTransactionResult } from './event-consumer-transactions.js';
import { eventConsumerDeliveryContextFromHeaders, type EventConsumerDeliveryContext } from './event-consumer-delivery-context.js';
import { timestamp } from './event-deadline-policy.js';
import type { EventAcknowledgmentStatus } from './generated/models/EventAcknowledgmentStatus.js';
import { acknowledgmentCall, acknowledgmentWait, AcknowledgmentCallbackFailure,
  type AcknowledgmentWorkerSettings, type AcknowledgmentWorkerScope } from './event-acknowledgment-callbacks.js';

export interface EventAcknowledgmentWorkerOptions {
  /** Integer 1..10; default three, including the first call. */
  callbackAttempts?: number;
  /** Whole milliseconds, 1..min(5000, lease/6); default that maximum rounded down. */
  callbackTimeoutMs?: number;
  /** Whole milliseconds, 1..lease/3; default min(100, lease/12) rounded down. */
  retryBackoffMs?: number;
}
export interface EventAcknowledgmentWorkerOperationOptions { signal?: AbortSignal }
export interface EventAcknowledgmentWorkerResult {
  /** False does not establish rollback. Reconcile unknown commits using completeReceipt. */
  readonly receiptConfirmed: boolean;
  readonly receipt?: Readonly<EventConsumerTransactionResult>;
  readonly acknowledgment?: Readonly<EventAcknowledgmentStatus>;
}
export type EventAcknowledgmentWorkerErrorCode = 'event_acknowledgment_lease_lost' | 'event_acknowledgment_unconfirmed'
  | 'event_acknowledgment_worker_busy' | 'event_consumer_receipt_missing' | 'event_consumer_receipt_committed';
const messages: Record<EventAcknowledgmentWorkerErrorCode, string> = {
  event_acknowledgment_lease_lost: 'event acknowledgment lease expired, replaced or unavailable',
  event_acknowledgment_unconfirmed: 'event acknowledgment callback outcome unconfirmed',
  event_acknowledgment_worker_busy: 'event acknowledgment worker operation already running',
  event_consumer_receipt_missing: 'no committed event consumer receipt',
  event_consumer_receipt_committed: 'event consumer receipt already committed; complete instead of rejecting',
};
const emptyResult: EventAcknowledgmentWorkerResult = Object.freeze({ receiptConfirmed: false });

/** Callback errors exclude remote bodies, request objects and raw transport causes. */
export class EventAcknowledgmentWorkerError extends Error {
  readonly result: EventAcknowledgmentWorkerResult;
  constructor(readonly code: EventAcknowledgmentWorkerErrorCode, result: EventAcknowledgmentWorkerResult = emptyResult,
    readonly status?: number) {
    super(messages[code]);
    this.name = 'EventAcknowledgmentWorkerError';
    this.result = Object.freeze({ ...result });
  }
  toJSON(): object { return { name: this.name, code: this.code, receiptConfirmed: this.result.receiptConfirmed, status: this.status }; }
  [inspect.custom](): object { return this.toJSON(); }
}

function settings(options: EventAcknowledgmentWorkerOptions, seconds: number): AcknowledgmentWorkerSettings {
  if (!options || typeof options !== 'object' || Array.isArray(options)) throw new TypeError('invalid acknowledgment worker options');
  const renewalIntervalMs = seconds * 1000 / 3;
  const maximumTimeout = Math.floor(Math.min(5000, renewalIntervalMs / 2));
  const callbackAttempts = options.callbackAttempts === undefined ? 3 : options.callbackAttempts;
  const callbackTimeoutMs = options.callbackTimeoutMs === undefined ? maximumTimeout : options.callbackTimeoutMs;
  const retryBackoffMs = options.retryBackoffMs === undefined ? Math.floor(Math.min(100, renewalIntervalMs / 4)) : options.retryBackoffMs;
  if (!Number.isInteger(callbackAttempts) || callbackAttempts < 1 || callbackAttempts > 10
    || !Number.isInteger(callbackTimeoutMs) || callbackTimeoutMs < 1 || callbackTimeoutMs > maximumTimeout
    || !Number.isInteger(retryBackoffMs) || retryBackoffMs < 1 || retryBackoffMs > renewalIntervalMs) {
    throw new TypeError('invalid acknowledgment worker callback budget');
  }
  return Object.freeze({ callbackAttempts, callbackTimeoutMs, retryBackoffMs, renewalIntervalMs });
}
function header(headers: Record<string, string | readonly string[] | undefined>, name: string): string {
  const entries = Object.entries(headers).filter(([key]) => key.toLowerCase() === name);
  const raw = entries[0]?.[1];
  const value = typeof raw === 'string' ? raw : Array.isArray(raw) && raw.length === 1 ? raw[0] : undefined;
  if (entries.length !== 1 || typeof value !== 'string' || !value) {
    throw new TypeError('missing or duplicate acknowledgment header');
  }
  return value;
}
function operationSignal(options: EventAcknowledgmentWorkerOperationOptions): AbortSignal | undefined {
  if (!options || typeof options !== 'object' || Array.isArray(options)
    || (options.signal !== undefined && !(options.signal instanceof AbortSignal))) throw new TypeError('invalid acknowledgment worker signal');
  const signal = options.signal;
  signal?.throwIfAborted();
  return signal;
}
function reasonCode(reason: string): boolean { return typeof reason === 'string' && /^[a-z0-9_.-]{1,128}$/.test(reason); }
function callbackError(error: unknown, result = emptyResult): EventAcknowledgmentWorkerError {
  const status = error instanceof AcknowledgmentCallbackFailure ? error.status : undefined;
  const lost = error instanceof AcknowledgmentCallbackFailure && error.leaseLost;
  return new EventAcknowledgmentWorkerError(lost || status === 404 || status === 409 || status === 410
    ? 'event_acknowledgment_lease_lost' : 'event_acknowledgment_unconfirmed', result, status);
}
function missing(error: unknown): boolean {
  return error instanceof EventAcknowledgmentWorkerError && error.code === 'event_consumer_receipt_missing';
}
const minimumTime = timestamp('0001-01-01T00:00:00Z')!;
const maximumTime = timestamp('9999-12-31T23:59:59.999999999Z')!;
function time(value: unknown): bigint | undefined {
  const at = timestamp(value);
  return at !== undefined && at > minimumTime && at <= maximumTime ? at : undefined;
}

/** Configure FaaSClient first. Authenticate delivery before constructing this capability.
 * The application owns protected durable jobs; this class does not enqueue or install schemas. */
export class EventAcknowledgmentWorker {
  readonly #scope: AcknowledgmentWorkerScope;
  readonly #settings: AcknowledgmentWorkerSettings;
  readonly #input: EventConsumerRequest;
  readonly #delivery: EventConsumerDeliveryContext;
  #busy = false;

  private constructor(scope: AcknowledgmentWorkerScope, budget: AcknowledgmentWorkerSettings,
    input: EventConsumerRequest, delivery: EventConsumerDeliveryContext) {
    this.#scope = Object.freeze(scope); this.#settings = budget; this.#input = input; this.#delivery = delivery;
    Object.freeze(this);
  }
  static fromHeaders(app: string, headers: Record<string, string | readonly string[] | undefined>, body: Uint8Array,
    accountId: string, appId: string, options: EventAcknowledgmentWorkerOptions = {}): EventAcknowledgmentWorker {
    if (typeof app !== 'string' || !/^[a-z0-9][a-z0-9-]{0,252}$/.test(app)) throw new TypeError('invalid acknowledgment application slug');
    if (!headers || typeof headers !== 'object' || Array.isArray(headers) || !(body instanceof Uint8Array)) {
      throw new TypeError('invalid event acknowledgment delivery');
    }
    headers = Object.fromEntries(Object.entries(headers).map(([name, value]) => [name, Array.isArray(value) ? [...value] : value]));
    const original = Buffer.from(body);
    const input = eventConsumerRequestFromHeaders(headers, original, accountId, appId);
    const delivery = eventConsumerDeliveryContextFromHeaders(headers, original, accountId, appId);
    // Generated status responses use JS numbers. Never round a fencing generation.
    if (BigInt(delivery.replayGeneration) > BigInt(Number.MAX_SAFE_INTEGER)) throw new TypeError('acknowledgment generation exceeds the Node safe-integer range');
    const token = header(headers, 'x-gregale-event-acknowledgment-token');
    const lease = header(headers, 'x-gregale-event-acknowledgment-lease-seconds');
    if (!/^[a-f0-9]{64}$/.test(token) || !/^[1-9][0-9]{0,2}$/.test(lease) || Number(lease) > 300) {
      throw new TypeError('invalid acknowledgment token or lease');
    }
    return new EventAcknowledgmentWorker({ app, invocationId: delivery.invocationId, token }, settings(options, Number(lease)), input, delivery);
  }
  toJSON(): string { return this.toString(); }
  toString(): string { return 'EventAcknowledgmentWorker{redacted}'; }
  [inspect.custom](): string { return this.toString(); }

  /** Business callback uses only tx, observes signal and returns exact JSON text. No external effects. */
  async run(pool: EventConsumerPool, handler: (transaction: EventConsumerTransaction, signal: AbortSignal) => Promise<string>,
    options: EventAcknowledgmentWorkerOperationOptions = {}): Promise<EventAcknowledgmentWorkerResult> {
    return this.#operate(pool, options, async signal => {
      if (typeof handler !== 'function') throw new TypeError('invalid event consumer handler');
      try { return await this.#complete(await this.#receipt(pool, signal), signal); }
      catch (error) { if (!missing(error)) throw error; }
      await this.#startLease(signal);
      const work = new AbortController();
      const abort = () => work.abort(signal?.reason);
      signal?.addEventListener('abort', abort, { once: true });
      if (signal?.aborted) abort();
      const stop = this.#monitor(work);
      let receipt: EventConsumerTransactionResult;
      try {
        receipt = await withEventConsumerTransaction(pool, this.#input, tx => handler(tx, work.signal), { signal: work.signal });
      } catch (error) {
        // An abort after COMMIT was sent cannot establish rollback.
        if (error instanceof EventConsumerCommitUnknownError) throw error;
        if (work.signal.aborted) throw work.signal.reason;
        throw error;
      } finally {
        await stop();
        signal?.removeEventListener('abort', abort);
        work.abort();
      }
      // Even a late renewal failure cannot undo a confirmed receipt. The server
      // decides whether completion still matches the attempt/token fence.
      return this.#complete(receipt, signal);
    });
  }
  /** Reconcile an uncertain commit/callback without ever invoking business code. */
  async completeReceipt(pool: EventConsumerPool, options: EventAcknowledgmentWorkerOperationOptions = {}): Promise<EventAcknowledgmentWorkerResult> {
    return this.#operate(pool, options, async signal => this.#complete(await this.#receipt(pool, signal), signal));
  }
  /** Requires stopped work, known rollback and exclusive durable-job ownership. Persist the decision first. */
  async retry(pool: EventConsumerPool, reason: string, options: EventAcknowledgmentWorkerOperationOptions = {}): Promise<EventAcknowledgmentWorkerResult> {
    return this.#reject(pool, 'retry', reason, options);
  }
  /** Has the same ownership and receipt requirements as retry. */
  async deadLetter(pool: EventConsumerPool, reason: string, options: EventAcknowledgmentWorkerOperationOptions = {}): Promise<EventAcknowledgmentWorkerResult> {
    return this.#reject(pool, 'dead-letter', reason, options);
  }
  async #operate<T>(pool: EventConsumerPool, options: EventAcknowledgmentWorkerOperationOptions,
    operation: (signal?: AbortSignal) => Promise<T>): Promise<T> {
    const signal = operationSignal(options);
    if (!pool || typeof pool.connect !== 'function') throw new TypeError('invalid event consumer pool');
    if (this.#busy) throw new EventAcknowledgmentWorkerError('event_acknowledgment_worker_busy');
    this.#busy = true;
    try { return await operation(signal); } finally { this.#busy = false; }
  }
  async #receipt(pool: EventConsumerPool, signal?: AbortSignal): Promise<EventConsumerTransactionResult> {
    return withEventConsumerTransaction(pool, this.#input, async () => { throw new EventAcknowledgmentWorkerError('event_consumer_receipt_missing'); }, { signal });
  }
  async #reject(pool: EventConsumerPool, action: 'retry' | 'dead-letter', reason: string,
    options: EventAcknowledgmentWorkerOperationOptions): Promise<EventAcknowledgmentWorkerResult> {
    return this.#operate(pool, options, async signal => {
      if (!reasonCode(reason)) throw new TypeError('invalid event acknowledgment failure reason');
      let receipt: EventConsumerTransactionResult | undefined;
      try { receipt = await this.#receipt(pool, signal); }
      catch (error) { if (!missing(error)) throw error; }
      if (receipt) throw new EventAcknowledgmentWorkerError('event_consumer_receipt_committed', { receiptConfirmed: true, receipt: Object.freeze(receipt) });
      return this.#terminal(action, reason, emptyResult, signal);
    });
  }
  async #complete(receipt: EventConsumerTransactionResult, signal?: AbortSignal): Promise<EventAcknowledgmentWorkerResult> {
    return this.#terminal('complete', receipt.body, Object.freeze({ receiptConfirmed: true, receipt: Object.freeze(receipt) }), signal);
  }
  #observation(raw: unknown): EventAcknowledgmentStatus {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new AcknowledgmentCallbackFailure();
    const r = raw as Record<string, unknown>;
    if (typeof r.invocation_id !== 'string' || r.invocation_id.toLowerCase() !== this.#delivery.invocationId
      || r.attempt !== this.#delivery.attempt || typeof r.replay_generation !== 'number' || !Number.isSafeInteger(r.replay_generation)
      || String(r.replay_generation) !== this.#delivery.replayGeneration) throw new AcknowledgmentCallbackFailure(undefined, undefined, true);
    if (typeof r.state !== 'string' || !/^[a-z_]{1,32}$/.test(r.state)
      || !['waiting', 'delivering', 'processing', 'expired', 'completed', 'failed', 'dead_letter', 'cancelled', 'superseded'].includes(r.processing_state as string)
      || (r.disposition !== undefined && r.disposition !== 'retry' && r.disposition !== 'dead-letter')
      || (r.failure_reason !== undefined && r.failure_reason !== '' && !reasonCode(r.failure_reason as string))
      || ['lease_expires_at', 'deadline_at', 'next_attempt_at'].some(k => r[k] !== undefined && time(r[k]) === undefined)) {
      throw new AcknowledgmentCallbackFailure();
    }
    // Copy only known bounded fields. Future server fields must not leak tokens or payloads.
    return Object.freeze({ invocation_id: this.#delivery.invocationId, attempt: this.#delivery.attempt,
      replay_generation: r.replay_generation, state: r.state, processing_state: r.processing_state as EventAcknowledgmentStatus['processing_state'],
      ...(r.disposition === undefined ? {} : { disposition: r.disposition as 'retry' | 'dead-letter' }),
      ...(r.failure_reason === undefined ? {} : { failure_reason: r.failure_reason as string }),
      ...(r.lease_expires_at === undefined ? {} : { lease_expires_at: r.lease_expires_at as string }),
      ...(r.deadline_at === undefined ? {} : { deadline_at: r.deadline_at as string }),
      ...(r.next_attempt_at === undefined ? {} : { next_attempt_at: r.next_attempt_at as string }) });
  }
  #renewed(raw: unknown): void {
    const s = this.#observation(raw);
    if (s.state !== 'dispatching' || s.processing_state !== 'processing' || s.disposition || s.failure_reason) throw new AcknowledgmentCallbackFailure(undefined, undefined, true);
    const expires = time(s.lease_expires_at), deadline = time(s.deadline_at);
    if (expires === undefined || deadline === undefined || expires > deadline) throw new AcknowledgmentCallbackFailure();
  }
  async #startLease(signal?: AbortSignal): Promise<void> {
    for (let attempt = 0; attempt < this.#settings.callbackAttempts; attempt++) {
      let raw: unknown;
      try {
        raw = await acknowledgmentCall(this.#scope, this.#settings, 'renew', signal);
      } catch (error) {
        let delay = error instanceof AcknowledgmentCallbackFailure ? error.retryDelayMs : undefined;
        let offered = false;
        if (!signal?.aborted && error instanceof AcknowledgmentCallbackFailure && error.status === 409) {
          try {
            const current = this.#observation(await acknowledgmentCall(this.#scope, this.#settings, 'get', signal));
            if (current.state !== 'dispatching' || current.processing_state !== 'delivering') throw new AcknowledgmentCallbackFailure(undefined, undefined, true);
            // GET is not token proof. Only a subsequent successful renewal admits work.
            delay = this.#settings.retryBackoffMs;
            offered = true;
          } catch (readError) { throw callbackError(readError); }
        }
        if (signal?.aborted || delay === undefined || attempt + 1 === this.#settings.callbackAttempts) {
          if (offered) throw new EventAcknowledgmentWorkerError('event_acknowledgment_unconfirmed', emptyResult, 409);
          throw callbackError(error);
        }
        await acknowledgmentWait(delay, signal);
        continue;
      }
      try { this.#renewed(raw); } catch (error) { throw callbackError(error); }
      return;
    }
  }
  #monitor(work: AbortController): () => Promise<void> {
    const monitor = new AbortController();
    const abort = () => monitor.abort();
    work.signal.addEventListener('abort', abort, { once: true });
    if (work.signal.aborted) abort();
    const done = (async () => {
      try {
        while (!monitor.signal.aborted) {
          await acknowledgmentWait(this.#settings.renewalIntervalMs, monitor.signal);
          this.#renewed(await acknowledgmentCall(this.#scope, this.#settings, 'renew', monitor.signal));
        }
      } catch (error) { if (!monitor.signal.aborted) work.abort(callbackError(error)); }
    })();
    return async () => { monitor.abort(); await done; work.signal.removeEventListener('abort', abort); };
  }
  async #terminal(action: 'complete' | 'retry' | 'dead-letter', value: string, result: EventAcknowledgmentWorkerResult,
    signal?: AbortSignal): Promise<EventAcknowledgmentWorkerResult> {
    for (let attempt = 0; attempt < this.#settings.callbackAttempts; attempt++) {
      try {
        const s = this.#observation(await acknowledgmentCall(this.#scope, this.#settings, action, signal, value));
        const valid = action === 'complete' ? s.state === 'completed' && s.processing_state === 'completed' && !s.disposition && !s.failure_reason
          : s.disposition === action && s.failure_reason === value && s.lease_expires_at === undefined
            && (action === 'retry' && s.state === 'pending' && s.processing_state === 'waiting' && s.next_attempt_at !== undefined
              || s.state === 'dead_letter' && s.processing_state === 'dead_letter' && s.next_attempt_at === undefined);
        if (!valid) throw new AcknowledgmentCallbackFailure();
        return Object.freeze({ ...result, acknowledgment: s });
      } catch (error) {
        const delay = error instanceof AcknowledgmentCallbackFailure ? error.retryDelayMs : undefined;
        if (signal?.aborted || delay === undefined || attempt + 1 === this.#settings.callbackAttempts) throw callbackError(error, result);
        try { await acknowledgmentWait(delay, signal); } catch { throw callbackError(undefined, result); }
      }
    }
    throw callbackError(undefined, result);
  }
}

export const eventAcknowledgmentWorkerFromHeaders = EventAcknowledgmentWorker.fromHeaders;
