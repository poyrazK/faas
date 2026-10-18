import { inspect } from 'node:util';
import { eventConsumerRequestFromHeaders, eventConsumerTransaction, EventConsumerCommitUnknownError,
  type EventConsumerTransactionHooks,
  type EventConsumerPool, type EventConsumerRequest, type EventConsumerTransaction, type EventConsumerTransactionResult } from './event-consumer-transactions.js';
import { settings, operationSignal, reasonCode, callbackError, missing, emptyResult, observation, terminal, time, EventAcknowledgmentWorkerError,
  type EventAcknowledgmentWorkerOptions, type EventAcknowledgmentWorkerOperationOptions, type EventAcknowledgmentWorkerResult } from './event-acknowledgment-protocol.js';
export { EventAcknowledgmentWorkerError, type EventAcknowledgmentWorkerOptions, type EventAcknowledgmentWorkerOperationOptions,
  type EventAcknowledgmentWorkerResult, type EventAcknowledgmentWorkerErrorCode } from './event-acknowledgment-protocol.js';
import { eventConsumerDeliveryContextFromHeaders, type EventConsumerDeliveryContext } from './event-consumer-delivery-context.js';
import { acknowledgmentCall, acknowledgmentWait, AcknowledgmentCallbackFailure,
  type AcknowledgmentWorkerSettings, type AcknowledgmentWorkerScope } from './event-acknowledgment-callbacks.js';

import { withEventConsumerProcessingLease, processingStoreError, type EventConsumerProcessingLease } from './event-consumer-processing.js';
import { coordinateEventAcknowledgmentPreparation, preparationSettings,
  type EventAcknowledgmentPreparedOptions } from './event-acknowledgment-preparation.js';
export type { EventAcknowledgmentPreparedOptions } from './event-acknowledgment-preparation.js';
import { observePreparation } from './event-acknowledgment-preparation-events.js';
import { saveIntent, saveNegativeIntent, intentStoreError, type EventAcknowledgmentIntentResult,
  type EventAcknowledgmentIntentStatus, type IntentInput, type EventAcknowledgmentIntentStagingOptions } from './event-acknowledgment-intent-store.js';
import { intentStagingPolicy, type EventAcknowledgmentIntentStoredRetryPolicy } from './event-acknowledgment-intent-retry-policy.js';

function header(headers: Record<string, string | readonly string[] | undefined>, name: string): string {
  const entries = Object.entries(headers).filter(([key]) => key.toLowerCase() === name);
  const raw = entries[0]?.[1];
  const value = typeof raw === 'string' ? raw : Array.isArray(raw) && raw.length === 1 ? raw[0] : undefined;
  if (entries.length !== 1 || typeof value !== 'string' || !value) {
    throw new TypeError('missing or duplicate acknowledgment header');
  }
  return value;
}

/** Configure FaaSClient first. Authenticate delivery before constructing this capability.
 * The application owns protected durable jobs; this class does not enqueue or install schemas. */
export class EventAcknowledgmentWorker {
  readonly #scope: AcknowledgmentWorkerScope;
  readonly #settings: AcknowledgmentWorkerSettings;
  readonly #input: EventConsumerRequest;
  readonly #delivery: EventConsumerDeliveryContext;
  readonly #seconds: number;
  #busy = false;

  private constructor(scope: AcknowledgmentWorkerScope, budget: AcknowledgmentWorkerSettings,
    input: EventConsumerRequest, delivery: EventConsumerDeliveryContext, seconds: number) {
    this.#scope = Object.freeze(scope); this.#settings = budget; this.#input = input; this.#delivery = delivery; this.#seconds = seconds;
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
    return new EventAcknowledgmentWorker({ app, invocationId: delivery.invocationId, token }, settings(options, Number(lease)), input, delivery, Number(lease));
  }
  toJSON(): string { return this.toString(); }
  toString(): string { return 'EventAcknowledgmentWorker{redacted}'; }
  [inspect.custom](): string { return this.toString(); }

  /** Business callback uses only tx, observes signal and returns exact JSON text. No external effects. */
  async run(pool: EventConsumerPool, handler: (transaction: EventConsumerTransaction, signal: AbortSignal) => Promise<string>,
    options: EventAcknowledgmentWorkerOperationOptions = {}): Promise<EventAcknowledgmentWorkerResult> {
    return this.#operate(pool, options, async signal => this.#complete(await this.#process(pool, handler, signal), signal));
  }
  /** Commit business writes, receipt and completion intent together. Sends no terminal callback. */
  async runDurable(pool: EventConsumerPool, handler: (transaction: EventConsumerTransaction, signal: AbortSignal) => Promise<string>,
    options: EventAcknowledgmentIntentStagingOptions = {}): Promise<EventAcknowledgmentIntentResult> {
    if (typeof handler !== 'function') throw new TypeError('invalid event consumer handler');
    const policy = intentStagingPolicy(options);
    return this.#operate(pool, options, signal => this.#durable(pool, signal, handler, {}, policy));
  }
  /** Complete with an independently acquired local processing lease. Stop local renewal before calling. */
  async runWithProcessingLease(pool: EventConsumerPool, lease: EventConsumerProcessingLease,
    handler: (transaction: EventConsumerTransaction, signal: AbortSignal) => Promise<string>,
    options: EventAcknowledgmentWorkerOperationOptions = {}): Promise<EventAcknowledgmentWorkerResult> {
    return this.#operate(pool, options, signal => withEventConsumerProcessingLease(lease, this.#input, async hooks =>
      this.#complete(await this.#process(pool, handler, signal, { ...hooks, storageError: processingStoreError }), signal)));
  }
  /** Atomically saves the receipt/intent and releases the matching unexpired processing claim. */
  async runDurableWithProcessingLease(pool: EventConsumerPool, lease: EventConsumerProcessingLease,
    handler: (transaction: EventConsumerTransaction, signal: AbortSignal) => Promise<string>,
    options: EventAcknowledgmentIntentStagingOptions = {}): Promise<EventAcknowledgmentIntentResult> {
    if (typeof handler !== 'function') throw new TypeError('invalid event consumer handler');
    const policy = intentStagingPolicy(options);
    return this.#operate(pool, options, signal => withEventConsumerProcessingLease(lease, this.#input,
      hooks => this.#durable(pool, signal, handler, hooks, policy)));
  }
  /** Own both renewal loops during side-effect-free preparation, then atomically save writes/receipt/intent. */
  async runPreparedDurable<T>(pool: EventConsumerPool, prepare: (signal: AbortSignal) => Promise<T>,
    handler: (transaction: EventConsumerTransaction, signal: AbortSignal, prepared: T) => Promise<string>,
    options: EventAcknowledgmentPreparedOptions): Promise<EventAcknowledgmentIntentResult> {
    const preparation = preparationSettings(options);
    const policy = intentStagingPolicy(options);
    if (typeof prepare !== 'function' || typeof handler !== 'function') throw new TypeError('invalid event preparation callbacks');
    return this.#operate(pool, options, signal => observePreparation(preparation.onEvent, signal, async events => {
      const committed = async (operation: () => Promise<EventAcknowledgmentIntentResult>) => {
        const result = await operation(); events.intentCommitted(result.receipt.replayed); return result;
      };
      try { return await committed(() => this.#durable(pool, signal, undefined, {}, policy)); }
      catch (error) { if (!missing(error)) throw error; }
      return coordinateEventAcknowledgmentPreparation(pool, this.#input, signal, preparation, prepare, {
        start: async work => {
          try { await this.#startLease(work.signal); }
          catch (error) {
            const alreadyAborted = work.signal.aborted; work.abort(error);
            events.renewalFailed('platform', error, alreadyAborted); throw error;
          }
          return this.#monitor(work, error => events.renewalFailed('platform', error));
        },
        replay: workSignal => committed(() => this.#durable(pool, workSignal, undefined, {}, policy)),
        complete: (lease, prepared, workSignal) => committed(() => withEventConsumerProcessingLease(lease, this.#input,
          processingHooks => this.#withIntent(hooks => eventConsumerTransaction(pool, this.#input,
            tx => handler(tx, workSignal, prepared), { signal: workSignal }, hooks), processingHooks, policy))),
      }, events);
    }));
  }
  /** Receipt-only staging after uncertain COMMIT. Never invokes business code. */
  async saveCompletionIntent(pool: EventConsumerPool, options: EventAcknowledgmentIntentStagingOptions = {}): Promise<EventAcknowledgmentIntentResult> {
    const policy = intentStagingPolicy(options);
    return this.#operate(pool, options, signal => this.#durable(pool, signal, undefined, {}, policy));
  }
  /** Requires stopped work, known rollback and exclusive ownership. Saves locally before callback delivery. */
  async saveRetryIntent(pool: EventConsumerPool, reason: string, options: EventAcknowledgmentIntentStagingOptions = {}): Promise<EventAcknowledgmentIntentStatus> {
    const policy = intentStagingPolicy(options);
    return this.#operate(pool, options, signal => saveNegativeIntent(pool, this.#intentInput(), 'retry', reason, signal, policy));
  }
  /** Same ownership requirements as saveRetryIntent. */
  async saveDeadLetterIntent(pool: EventConsumerPool, reason: string, options: EventAcknowledgmentIntentStagingOptions = {}): Promise<EventAcknowledgmentIntentStatus> {
    const policy = intentStagingPolicy(options);
    return this.#operate(pool, options, signal => saveNegativeIntent(pool, this.#intentInput(), 'dead-letter', reason, signal, policy));
  }
  #intentInput(): IntentInput {
    return { input: this.#input, scope: this.#scope, delivery: this.#delivery, seconds: this.#seconds };
  }
  async #durable(pool: EventConsumerPool, signal?: AbortSignal,
    handler?: (transaction: EventConsumerTransaction, signal: AbortSignal) => Promise<string>,
    processingHooks: EventConsumerTransactionHooks = {}, policy?: Readonly<EventAcknowledgmentIntentStoredRetryPolicy>): Promise<EventAcknowledgmentIntentResult> {
    return this.#withIntent(hooks => handler === undefined ? this.#receipt(pool, signal, hooks) : this.#process(pool, handler, signal, hooks), processingHooks, policy);
  }
  async #withIntent(operation: (hooks: EventConsumerTransactionHooks) => Promise<EventConsumerTransactionResult>,
    processingHooks: EventConsumerTransactionHooks = {}, policy?: Readonly<EventAcknowledgmentIntentStoredRetryPolicy>): Promise<EventAcknowledgmentIntentResult> {
    let intent: EventAcknowledgmentIntentStatus | undefined;
    const input = this.#intentInput();
    const hooks: EventConsumerTransactionHooks = { ...processingHooks, storageError: intentStoreError,
      beforeCommit: async (tx, receipt) => { intent = await saveIntent(tx, input, 'complete', '', receipt.body, policy); } };
    const receipt = await operation(hooks);
    return Object.freeze({ receiptConfirmed: true, receipt: Object.freeze(receipt), intent: intent! });
  }
  async #process(pool: EventConsumerPool, handler: (transaction: EventConsumerTransaction, signal: AbortSignal) => Promise<string>,
    signal?: AbortSignal, hooks: EventConsumerTransactionHooks = {}): Promise<EventConsumerTransactionResult> {
    if (typeof handler !== 'function') throw new TypeError('invalid event consumer handler');
    try { return await this.#receipt(pool, signal, hooks); }
    catch (error) { if (!missing(error)) throw error; }
    await this.#startLease(signal);
    const work = new AbortController();
    const abort = () => work.abort(signal?.reason);
    signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) abort();
    const stop = this.#monitor(work);
    let receipt: EventConsumerTransactionResult;
    try {
      receipt = await eventConsumerTransaction(pool, this.#input, tx => handler(tx, work.signal), { signal: work.signal }, hooks);
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
    return receipt;
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
  async #receipt(pool: EventConsumerPool, signal?: AbortSignal, hooks: EventConsumerTransactionHooks = {}): Promise<EventConsumerTransactionResult> {
    return eventConsumerTransaction(pool, this.#input, async () => { throw new EventAcknowledgmentWorkerError('event_consumer_receipt_missing'); }, { signal },
      { ...hooks, beforeHandler: undefined, afterReceipt: undefined });
  }
  async #reject(pool: EventConsumerPool, action: 'retry' | 'dead-letter', reason: string,
    options: EventAcknowledgmentWorkerOperationOptions): Promise<EventAcknowledgmentWorkerResult> {
    return this.#operate(pool, options, async signal => {
      if (!reasonCode(reason)) throw new TypeError('invalid event acknowledgment failure reason');
      let receipt: EventConsumerTransactionResult | undefined;
      try { receipt = await this.#receipt(pool, signal); }
      catch (error) { if (!missing(error)) throw error; }
      if (receipt) throw new EventAcknowledgmentWorkerError('event_consumer_receipt_committed', { receiptConfirmed: true, receipt: Object.freeze(receipt) });
      return terminal(this.#scope, this.#settings, this.#delivery, action, reason, emptyResult, signal);
    });
  }
  async #complete(receipt: EventConsumerTransactionResult, signal?: AbortSignal): Promise<EventAcknowledgmentWorkerResult> {
    return terminal(this.#scope, this.#settings, this.#delivery, 'complete', receipt.body, Object.freeze({ receiptConfirmed: true, receipt: Object.freeze(receipt) }), signal);
  }
  #renewed(raw: unknown): void {
    const s = observation(this.#delivery, raw);
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
            const current = observation(this.#delivery, await acknowledgmentCall(this.#scope, this.#settings, 'get', signal));
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
  #monitor(work: AbortController, onFailure?: (error: EventAcknowledgmentWorkerError) => void): () => Promise<void> {
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
      } catch (error) {
        if (!monitor.signal.aborted) {
          const failure = callbackError(error); work.abort(failure);
          try { onFailure?.(failure); } catch { /* Telemetry cannot alter renewal cancellation. */ }
        }
      }
    })();
    return async () => { monitor.abort(); await done; work.signal.removeEventListener('abort', abort); };
  }
}

export const eventAcknowledgmentWorkerFromHeaders = EventAcknowledgmentWorker.fromHeaders;
