import { createHash } from 'node:crypto';

/** Optional capped exponential full jitter for future saved-callback recovery.
 * These are delay caps, not minimum waits. This policy has no lifetime attempt limit. */
export interface EventAcknowledgmentIntentRetryPolicy {
  /** Whole milliseconds 1..3600000; default 1000. */
  readonly initialBackoffMs?: number;
  /** Whole milliseconds, at least initialBackoffMs and at most 3600000; default 300000. */
  readonly maxBackoffMs?: number;
}

/** Payload-free immutable binding. The current total grant lives on status. */
export type EventAcknowledgmentIntentStoredRetryPolicy = Readonly<{
  readonly initialBackoffMs: number;
  readonly maxBackoffMs: number;
}> & (Readonly<{ version: 1; maxDeliveryAttempts?: never }> | Readonly<{ version: 2; maxDeliveryAttempts: number }>);

/** Opt-in lifetime claim budget; the fallback timing policy remains unlimited. */
export interface EventAcknowledgmentIntentRetryBudget {
  readonly maxDeliveryAttempts: number;
  readonly retryPolicy?: EventAcknowledgmentIntentRetryPolicy;
}

/** Snapshot a staging binding before any asynchronous operation. */
export function intentStagingPolicy(options: { readonly backoffMs?: unknown; readonly retryPolicy?: unknown; readonly retryBudget?: unknown }): EventAcknowledgmentIntentStoredRetryPolicy | undefined {
  try {
    const policy = ownValue(options, 'retryPolicy'), budget = ownValue(options, 'retryBudget');
    if (budget === undefined) {
      const normalized = intentRetrySchedule(options).retryPolicy;
      return normalized === undefined ? undefined : Object.freeze({ version: 1, ...normalized });
    }
    if (policy !== undefined || ownValue(options, 'backoffMs') !== undefined || !budget || typeof budget !== 'object' || Array.isArray(budget)
      || Reflect.ownKeys(budget).some(key => key !== 'maxDeliveryAttempts' && key !== 'retryPolicy')) invalid();
    const maxDeliveryAttempts = ownValue(budget, 'maxDeliveryAttempts');
    if (typeof maxDeliveryAttempts !== 'number' || !Number.isInteger(maxDeliveryAttempts)
      || maxDeliveryAttempts < 1 || maxDeliveryAttempts > 2147483647) invalid();
    const timing = ownValue(budget, 'retryPolicy');
    const normalized = intentRetrySchedule({ retryPolicy: timing === undefined ? {} : timing }).retryPolicy!;
    return Object.freeze({ version: 2, ...normalized, maxDeliveryAttempts });
  } catch { return invalid(); }
}

export type IntentRetrySchedule = Readonly<{ backoffMs: number; retryPolicy?: undefined }>
  | Readonly<{ backoffMs?: undefined; retryPolicy: Readonly<Required<EventAcknowledgmentIntentRetryPolicy>> }>;

function invalid(): never { throw new TypeError('invalid acknowledgment intent retry options'); }
function ownValue(value: object, key: string): unknown {
  const property = Object.getOwnPropertyDescriptor(value, key);
  if (property === undefined) return undefined;
  if (!Object.hasOwn(property, 'value')) invalid();
  return property.value;
}
function milliseconds(value: unknown, fallback: number): number {
  if (value === undefined) return fallback;
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 1 || value > 3600000) invalid();
  return value;
}

/** Snapshot only retry fields; pass options separately enforce their closed shape.
 * Nested policy keys must be own data properties. Never invoke configuration getters. */
export function intentRetrySchedule(options: { readonly backoffMs?: unknown; readonly retryPolicy?: unknown }): IntentRetrySchedule {
  try {
    const backoff = ownValue(options, 'backoffMs'), value = ownValue(options, 'retryPolicy');
    if (value === undefined) return Object.freeze({ backoffMs: milliseconds(backoff, 1000) });
    if (backoff !== undefined || !value || typeof value !== 'object' || Array.isArray(value)) invalid();
    const keys = Reflect.ownKeys(value);
    if (keys.length > 2 || keys.some(key => key !== 'initialBackoffMs' && key !== 'maxBackoffMs')) invalid();
    const initialBackoffMs = milliseconds(ownValue(value, 'initialBackoffMs'), 1000);
    const maxBackoffMs = milliseconds(ownValue(value, 'maxBackoffMs'), 300000);
    if (maxBackoffMs < initialBackoffMs) invalid();
    return Object.freeze({ retryPolicy: Object.freeze({ initialBackoffMs, maxBackoffMs }) });
  } catch { return invalid(); }
}

/** Inputs are normalized and attempts come from the committed claim update. */
export function intentRetryDelay(schedule: IntentRetrySchedule, claim: Buffer, attempts: number): number {
  if (schedule.retryPolicy === undefined) return schedule.backoffMs;
  const policy = schedule.retryPolicy;
  let cap = policy.initialBackoffMs;
  for (let n = 1; n < attempts && cap < policy.maxBackoffMs; n++) cap = Math.min(policy.maxBackoffMs, cap * 2);
  // Reuse entropy generated before claim I/O; do not read randomness after an
  // uncertain callback or expose raw claim-token bits in the saved delay.
  const entropy = createHash('sha256').update('gregale-event-acknowledgment-intent-jitter-v1\n').update(claim).digest();
  return Number(entropy.readBigUInt64BE(0) % BigInt(cap)) + 1;
}
