// Best-effort, process-local telemetry. Only fixed metadata enters observer payloads.
import { EventAcknowledgmentIntentError } from './event-acknowledgment-intent-store.js';
import { EventAcknowledgmentWorkerError } from './event-acknowledgment-protocol.js';
import { EventConsumerProcessingError } from './event-consumer-processing.js';
import { EventConsumerCommitUnknownError, EventConsumerConflictError } from './event-consumer-transactions.js';

export type EventAcknowledgmentPreparationStage = 'receipt_reconciliation' | 'platform_start' | 'claim'
  | 'local_renewal' | 'preparation' | 'local_handoff' | 'completion';
export type EventAcknowledgmentPreparationOutcome = 'committed' | 'commit_unknown' | 'cancelled' | 'failed';
export type EventAcknowledgmentPreparationReason = 'cancelled' | 'preparation_timeout' | 'claim_busy'
  | 'local_lease_lost' | 'local_operation_busy' | 'local_store_failed' | 'platform_lease_lost'
  | 'platform_unconfirmed' | 'receipt_conflict' | 'receipt_missing' | 'intent_conflict'
  | 'intent_superseded' | 'intent_store_failed' | 'commit_unknown' | 'failed';
type PreparationEventDetail =
  | { readonly kind: 'started' }
  | { readonly kind: 'claim_acquired' }
  | { readonly kind: 'preparation_started' }
  | { readonly kind: 'preparation_finished'; readonly outcome: 'returned' | 'threw'; readonly durationMs: number }
  | { readonly kind: 'renewal_failed'; readonly lease: 'local' | 'platform'; readonly reason: EventAcknowledgmentPreparationReason }
  | { readonly kind: 'intent_committed'; readonly replayed: boolean }
  | { readonly kind: 'commit_unknown'; readonly stage: EventAcknowledgmentPreparationStage }
  | { readonly kind: 'shutdown'; readonly stage: EventAcknowledgmentPreparationStage;
      readonly outcome: EventAcknowledgmentPreparationOutcome; readonly reason?: EventAcknowledgmentPreparationReason };
/** Frozen primitive metadata only. elapsedMs uses a monotonic clock and includes local cleanup at shutdown. */
export type EventAcknowledgmentPreparationEvent = PreparationEventDetail & { readonly elapsedMs: number };

function reason(error: unknown, cancelled = false): EventAcknowledgmentPreparationReason {
  try {
    if (error instanceof EventConsumerCommitUnknownError) return 'commit_unknown';
    if (cancelled) return 'cancelled';
    if (error instanceof EventConsumerConflictError) return 'receipt_conflict';
    // Translate known typed errors to literals; never forward arbitrary .code, message or cause values.
    if (error instanceof EventConsumerProcessingError) {
      switch (error.code) {
        case 'event_consumer_processing_busy': return 'claim_busy';
        case 'event_consumer_processing_lease_lost': return 'local_lease_lost';
        case 'event_consumer_processing_operation_busy': return 'local_operation_busy';
        case 'event_consumer_processing_store': return 'local_store_failed';
      }
    }
    if (error instanceof EventAcknowledgmentWorkerError) {
      switch (error.code) {
        case 'event_consumer_preparation_timeout': return 'preparation_timeout';
        case 'event_acknowledgment_lease_lost': return 'platform_lease_lost';
        case 'event_acknowledgment_unconfirmed': return 'platform_unconfirmed';
        case 'event_consumer_receipt_missing': return 'receipt_missing';
      }
    }
    if (error instanceof EventAcknowledgmentIntentError) {
      switch (error.code) {
        case 'event_acknowledgment_intent_conflict': return 'intent_conflict';
        case 'event_acknowledgment_intent_superseded': return 'intent_superseded';
        case 'event_acknowledgment_intent_store': return 'intent_store_failed';
      }
    }
  } catch { /* Even hostile error objects must not affect telemetry or processing. */ }
  return cancelled ? 'cancelled' : 'failed';
}
function clock(): number { try { return performance.now(); } catch { return 0; } }
function duration(start: number): number {
  const elapsed = clock() - start;
  return Number.isFinite(elapsed) ? Math.max(0, Math.min(Number.MAX_SAFE_INTEGER, Math.floor(elapsed))) : 0;
}
/** Internal state never retains errors, inputs, capabilities or prepared data. */
export class PreparationEvents {
  readonly #started = clock();
  readonly #observer: ((event: EventAcknowledgmentPreparationEvent) => void) | undefined;
  #stage: EventAcknowledgmentPreparationStage = 'receipt_reconciliation';
  #outcome: EventAcknowledgmentPreparationOutcome = 'failed';
  #reason: EventAcknowledgmentPreparationReason | undefined;
  #unknown = false;
  #committed = false;
  #closed = false;
  readonly #renewals = new Set<'local' | 'platform'>();
  constructor(observer?: (event: EventAcknowledgmentPreparationEvent) => void) {
    this.#observer = observer; this.#emit({ kind: 'started' });
  }
  stage(stage: EventAcknowledgmentPreparationStage): void { this.#stage = stage; }
  claimAcquired(): void { this.#emit({ kind: 'claim_acquired' }); }
  async prepare<T>(operation: () => Promise<T>): Promise<T> {
    this.#emit({ kind: 'preparation_started' });
    const start = clock();
    try {
      const prepared = await operation();
      this.#emit({ kind: 'preparation_finished', outcome: 'returned', durationMs: duration(start) });
      return prepared;
    } catch (error) {
      this.#emit({ kind: 'preparation_finished', outcome: 'threw', durationMs: duration(start) });
      throw error;
    }
  }
  renewalFailed(lease: 'local' | 'platform', error: unknown, alreadyAborted = false): void {
    const code = reason(error);
    if (this.#renewals.has(lease) || alreadyAborted && code !== 'commit_unknown') return;
    this.#renewals.add(lease);
    this.#emit({ kind: 'renewal_failed', lease, reason: code });
    if (code === 'commit_unknown') this.#commitUnknown('local_renewal');
  }
  intentCommitted(replayed: boolean): void {
    if (this.#committed) return;
    this.#committed = true;
    this.#emit({ kind: 'intent_committed', replayed });
  }
  failed(error: unknown, cancelled: boolean): void {
    this.#reason = reason(error, cancelled);
    this.#outcome = this.#reason === 'commit_unknown' ? 'commit_unknown' : this.#reason === 'cancelled' ? 'cancelled' : 'failed';
    if (this.#outcome === 'commit_unknown') this.#commitUnknown(this.#stage);
  }
  shutdown(): void {
    const outcome = this.#committed ? 'committed' : this.#outcome;
    this.#emit({ kind: 'shutdown', stage: this.#stage, outcome,
      ...(this.#committed || this.#reason === undefined ? {} : { reason: this.#reason }) });
    this.#closed = true;
  }
  #commitUnknown(stage: EventAcknowledgmentPreparationStage): void {
    if (this.#unknown) return;
    this.#unknown = true;
    this.#emit({ kind: 'commit_unknown', stage });
  }
  #emit(detail: PreparationEventDetail): void {
    const observer = this.#observer;
    if (!observer || this.#closed) return;
    try {
      const result: unknown = observer(Object.freeze({ ...detail, elapsedMs: duration(this.#started) }));
      // Do not await observers. Handle accidental async rejection without retaining or reporting its error.
      if (result && typeof (result as PromiseLike<unknown>).then === 'function') void Promise.resolve(result).catch(() => {});
    } catch { /* Diagnostic failures never choose processing, commit or acknowledgment outcomes. */ }
  }
}
export async function observePreparation<R>(observer: ((event: EventAcknowledgmentPreparationEvent) => void) | undefined,
  signal: AbortSignal | undefined, operation: (events: PreparationEvents) => Promise<R>): Promise<R> {
  const events = new PreparationEvents(observer);
  try { return await operation(events); }
  catch (error) { events.failed(error, signal?.aborted === true && error === signal.reason); throw error; }
  finally { events.shutdown(); }
}
