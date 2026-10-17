// Internal shared validation and bounded callback delivery for live and durable workers.
import { inspect } from 'node:util';
import type { EventConsumerTransactionResult } from './event-consumer-transactions.js';
import type { EventAcknowledgmentStatus } from './generated/models/EventAcknowledgmentStatus.js';
import { timestamp } from './event-deadline-policy.js';
import { acknowledgmentCall, acknowledgmentWait, AcknowledgmentCallbackFailure,
  type AcknowledgmentWorkerSettings, type AcknowledgmentWorkerScope } from './event-acknowledgment-callbacks.js';
export interface AcknowledgmentDelivery { readonly invocationId: string; readonly attempt: number; readonly replayGeneration: string }
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
  | 'event_acknowledgment_worker_busy' | 'event_consumer_receipt_missing' | 'event_consumer_receipt_committed'
  | 'event_consumer_preparation_timeout';
const messages: Record<EventAcknowledgmentWorkerErrorCode, string> = {
  event_acknowledgment_lease_lost: 'event acknowledgment lease expired, replaced or unavailable',
  event_acknowledgment_unconfirmed: 'event acknowledgment callback outcome unconfirmed',
  event_acknowledgment_worker_busy: 'event acknowledgment worker operation already running',
  event_consumer_receipt_missing: 'no committed event consumer receipt',
  event_consumer_receipt_committed: 'event consumer receipt already committed; complete instead of rejecting',
  event_consumer_preparation_timeout: 'event consumer preparation budget expired',
};
export const emptyResult: EventAcknowledgmentWorkerResult = Object.freeze({ receiptConfirmed: false });

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

export function settings(options: EventAcknowledgmentWorkerOptions, seconds: number): AcknowledgmentWorkerSettings {
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
export function operationSignal(options: EventAcknowledgmentWorkerOperationOptions): AbortSignal | undefined {
  if (!options || typeof options !== 'object' || Array.isArray(options)
    || (options.signal !== undefined && !(options.signal instanceof AbortSignal))) throw new TypeError('invalid acknowledgment worker signal');
  const signal = options.signal;
  signal?.throwIfAborted();
  return signal;
}
export function reasonCode(reason: string): boolean { return typeof reason === 'string' && /^[a-z0-9_.-]{1,128}$/.test(reason); }
export function callbackError(error: unknown, result = emptyResult): EventAcknowledgmentWorkerError {
  const status = error instanceof AcknowledgmentCallbackFailure ? error.status : undefined;
  const lost = error instanceof AcknowledgmentCallbackFailure && error.leaseLost;
  return new EventAcknowledgmentWorkerError(lost || status === 404 || status === 409 || status === 410
    ? 'event_acknowledgment_lease_lost' : 'event_acknowledgment_unconfirmed', result, status);
}
export function missing(error: unknown): boolean {
  return error instanceof EventAcknowledgmentWorkerError && error.code === 'event_consumer_receipt_missing';
}
const minimumTime = timestamp('0001-01-01T00:00:00Z')!;
const maximumTime = timestamp('9999-12-31T23:59:59.999999999Z')!;
export function time(value: unknown): bigint | undefined {
  const at = timestamp(value);
  return at !== undefined && at > minimumTime && at <= maximumTime ? at : undefined;
}

export function observation(delivery: AcknowledgmentDelivery, raw: unknown): EventAcknowledgmentStatus {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new AcknowledgmentCallbackFailure();
  const r = raw as Record<string, unknown>;
  if (typeof r.invocation_id !== 'string' || r.invocation_id.toLowerCase() !== delivery.invocationId
    || r.attempt !== delivery.attempt || typeof r.replay_generation !== 'number' || !Number.isSafeInteger(r.replay_generation)
    || String(r.replay_generation) !== delivery.replayGeneration) throw new AcknowledgmentCallbackFailure(undefined, undefined, true);
  if (typeof r.state !== 'string' || !/^[a-z_]{1,32}$/.test(r.state)
    || !['waiting', 'delivering', 'processing', 'expired', 'completed', 'failed', 'dead_letter', 'cancelled', 'superseded'].includes(r.processing_state as string)
    || (r.disposition !== undefined && r.disposition !== 'retry' && r.disposition !== 'dead-letter')
    || (r.failure_reason !== undefined && r.failure_reason !== '' && !reasonCode(r.failure_reason as string))
    || ['lease_expires_at', 'deadline_at', 'next_attempt_at'].some(k => r[k] !== undefined && time(r[k]) === undefined)) {
    throw new AcknowledgmentCallbackFailure();
  }
  // Copy only known bounded fields. Future server fields must not leak tokens or payloads.
  return Object.freeze({ invocation_id: delivery.invocationId, attempt: delivery.attempt,
    replay_generation: r.replay_generation, state: r.state, processing_state: r.processing_state as EventAcknowledgmentStatus['processing_state'],
    ...(r.disposition === undefined ? {} : { disposition: r.disposition as 'retry' | 'dead-letter' }),
    ...(r.failure_reason === undefined ? {} : { failure_reason: r.failure_reason as string }),
    ...(r.lease_expires_at === undefined ? {} : { lease_expires_at: r.lease_expires_at as string }),
    ...(r.deadline_at === undefined ? {} : { deadline_at: r.deadline_at as string }),
    ...(r.next_attempt_at === undefined ? {} : { next_attempt_at: r.next_attempt_at as string }) });
}
export async function terminal(scope: AcknowledgmentWorkerScope, budget: AcknowledgmentWorkerSettings, delivery: AcknowledgmentDelivery, action: 'complete' | 'retry' | 'dead-letter', value: string, result: EventAcknowledgmentWorkerResult,
  signal?: AbortSignal): Promise<EventAcknowledgmentWorkerResult> {
  for (let attempt = 0; attempt < budget.callbackAttempts; attempt++) {
    try {
      const s = observation(delivery, await acknowledgmentCall(scope, budget, action, signal, value));
      const valid = action === 'complete' ? s.state === 'completed' && s.processing_state === 'completed' && !s.disposition && !s.failure_reason
        : s.disposition === action && s.failure_reason === value && s.lease_expires_at === undefined
          && (action === 'retry' && s.state === 'pending' && s.processing_state === 'waiting' && s.next_attempt_at !== undefined
            || s.state === 'dead_letter' && s.processing_state === 'dead_letter' && s.next_attempt_at === undefined);
      if (!valid) throw new AcknowledgmentCallbackFailure();
      return Object.freeze({ ...result, acknowledgment: s });
    } catch (error) {
      const delay = error instanceof AcknowledgmentCallbackFailure ? error.retryDelayMs : undefined;
      if (signal?.aborted || delay === undefined || attempt + 1 === budget.callbackAttempts) throw callbackError(error, result);
      try { await acknowledgmentWait(delay, signal); } catch { throw callbackError(undefined, result); }
    }
  }
  throw callbackError(undefined, result);
}
