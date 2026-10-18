/** Actual pass activity; health polling and scraping cannot refresh this cache. */
import { performance } from 'node:perf_hooks';
import type { EventConsumerPool } from './event-consumer-transactions.js';
import { intentPool, intentScope } from './event-acknowledgment-intent-store.js';
import { recoveryPassPolicy } from './event-acknowledgment-intent-recovery-pass-runtime.js';
import { recoverEventAcknowledgmentIntents, EventAcknowledgmentIntentRecoveryPassError,
  EventAcknowledgmentIntentRecoveryPassCancelled, EventAcknowledgmentIntentRecoveryPassCommitUnknownError,
  type EventAcknowledgmentIntentRecoveryPassOptions, type EventAcknowledgmentIntentRecoveryPassResult,
} from './event-acknowledgment-intent-recovery-pass.js';

export interface EventAcknowledgmentIntentRecoveryWorkerMetricsOptions {
  /** Whole milliseconds 100..86400000; default 60000. Observation, not a deadline. */
  readonly maxIdleMs?: number;
  /** Whole milliseconds 1000..86400000; default 300000. */
  readonly maxPassDurationMs?: number;
}
export interface EventAcknowledgmentIntentRecoveryWorkerPassSample {
  readonly startedAt: string;
  readonly completedAt: string;
  readonly durationSeconds: number;
  readonly completionAgeSeconds: number;
  /** Absent after malformed progress; native cleanup/uncertainty may overlap known outcomes. */
  readonly result?: Readonly<EventAcknowledgmentIntentRecoveryPassResult>;
}
export interface EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot {
  readonly clockValid: boolean;
  readonly inFlight: boolean;
  readonly overdue: boolean;
  readonly maxIdleSeconds: number;
  readonly maxPassSeconds: number;
  readonly activityAt: string;
  readonly activityAgeSeconds: number;
  readonly lastStartedAt?: string;
  readonly lastPass?: Readonly<EventAcknowledgmentIntentRecoveryWorkerPassSample>;
}

function invalid(): never { throw new TypeError('invalid acknowledgment recovery worker metrics input'); }
export function workerMetricData(value: unknown, keys: readonly string[], optional: readonly string[] = []): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid();
  const names = Reflect.ownKeys(value), out: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
  if (names.length > keys.length || names.some(key => typeof key !== 'string' || !keys.includes(key))) invalid();
  for (const key of keys) {
    const property = Object.getOwnPropertyDescriptor(value, key);
    if (!property) { if (!optional.includes(key)) invalid(); }
    else { if (!Object.hasOwn(property, 'value')) invalid(); out[key] = property.value; }
  }
  return out;
}
export function workerMetricCount(value: unknown, maximum: number): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0 || value > maximum) invalid();
  return value;
}
export function workerMetricAge(value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0 || value > Number.MAX_SAFE_INTEGER) invalid();
  return value;
}
export function workerMetricInstant(value: unknown): number {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/.test(value)) invalid();
  const parsed = new Date(value);
  if (!Number.isFinite(parsed.getTime()) || parsed.getUTCFullYear() < 1 || parsed.toISOString() !== value) invalid();
  return parsed.getTime();
}
export function workerMetricResult(value: unknown): Readonly<EventAcknowledgmentIntentRecoveryPassResult> {
  const data = workerMetricData(value, ['stopReason', 'attempts', 'confirmed', 'unconfirmed', 'parked', 'superseded', 'empty',
    'claimLost', 'interrupted', 'failed', 'commitUnknown', 'cleanupFailures']);
  const attempts = workerMetricCount(data.attempts, 10000);
  const confirmed = workerMetricCount(data.confirmed, attempts), unconfirmed = workerMetricCount(data.unconfirmed, attempts);
  const parked = workerMetricCount(data.parked, attempts), superseded = workerMetricCount(data.superseded, attempts);
  const empty = workerMetricCount(data.empty, attempts), claimLost = workerMetricCount(data.claimLost, attempts);
  const interrupted = workerMetricCount(data.interrupted, attempts), failed = workerMetricCount(data.failed, attempts);
  const commitUnknown = workerMetricCount(data.commitUnknown, attempts), cleanupFailures = workerMetricCount(data.cleanupFailures, attempts);
  const primary = confirmed + unconfirmed + parked + superseded + empty + claimLost + interrupted + failed;
  // Cleanup uncertainty may overlap a known outcome; incomplete coverage cannot manufacture zeroes.
  if (primary > attempts || primary + commitUnknown < attempts || primary + commitUnknown > attempts + Math.min(cleanupFailures, commitUnknown)) invalid();
  const stopReason = data.stopReason;
  if (commitUnknown > 0 ? stopReason !== 'commit_unknown' :
    stopReason === 'interrupted' ? false :
      stopReason === 'failed' ? failed + cleanupFailures === 0 || interrupted > 0 :
        stopReason === 'empty' ? empty === 0 || failed + cleanupFailures + interrupted > 0 :
          stopReason === 'limit' ? attempts === 0 || empty + failed + cleanupFailures + interrupted > 0 : true) invalid();
  return Object.freeze({ stopReason: stopReason as EventAcknowledgmentIntentRecoveryPassResult['stopReason'], attempts,
    confirmed, unconfirmed, parked, superseded, empty, claimLost, interrupted, failed, commitUnknown, cleanupFailures });
}

interface Clock { readonly wall: number; readonly local: number }
function clock(): Clock {
  const wall = Date.now(), local = performance.now();
  if (!Number.isSafeInteger(wall) || wall < -62135596800000 || wall > 253402300799999) invalid();
  workerMetricAge(local);
  return { wall, local };
}
function age(now: Clock, at: Clock): number { return Math.max(0, now.wall - at.wall, now.local - at.local) / 1000; }
function clockValid(now: Clock, at: Clock): boolean { return now.wall >= at.wall && now.local >= at.local; }
function passError(error: unknown): boolean {
  try { return error instanceof EventAcknowledgmentIntentRecoveryPassError || error instanceof EventAcknowledgmentIntentRecoveryPassCancelled
    || error instanceof EventAcknowledgmentIntentRecoveryPassCommitUnknownError; } catch { return false; }
}

/** Fixed memory for one owner/pass stream on one event-loop thread. Construction
 * starts only the first-pass idle observation; no I/O, tasks, listeners or timers.
 * Only the monitored wrapper can record activity. Snapshots are recursively frozen. */
export class EventAcknowledgmentIntentRecoveryWorkerMetrics {
  readonly #account: string;
  readonly #app: string;
  readonly #maxIdleMs: number;
  readonly #maxPassMs: number;
  readonly #enabled: Clock;
  #activity: Clock;
  #lastStarted?: Clock;
  #lastPass?: { started: Clock; completed: Clock; result?: Readonly<EventAcknowledgmentIntentRecoveryPassResult> };
  #inFlight = false;
  #clockValid = true;
  constructor(accountId: string, appId: string, options: EventAcknowledgmentIntentRecoveryWorkerMetricsOptions = {}) {
    try {
      [this.#account, this.#app] = intentScope(accountId, appId);
      const data = workerMetricData(options, ['maxIdleMs', 'maxPassDurationMs'], ['maxIdleMs', 'maxPassDurationMs']);
      this.#maxIdleMs = data.maxIdleMs === undefined ? 60000 : workerMetricCount(data.maxIdleMs, 86400000);
      this.#maxPassMs = data.maxPassDurationMs === undefined ? 300000 : workerMetricCount(data.maxPassDurationMs, 86400000);
      if (this.#maxIdleMs < 100 || this.#maxPassMs < 1000) invalid();
      this.#enabled = this.#activity = clock();
    } catch { invalid(); }
    Object.freeze(this);
  }
  #checkClock(now: Clock): void {
    this.#clockValid &&= clockValid(now, this.#activity) && clockValid(now, this.#enabled);
  }
  #finish(value: unknown): void {
    let now: Clock;
    try { now = clock(); } catch { this.#clockValid = false; return; }
    this.#checkClock(now);
    let result: Readonly<EventAcknowledgmentIntentRecoveryPassResult> | undefined;
    try { result = workerMetricResult(value); } catch { /* Missing outcomes remain unknown, with no raw cause retained. */ }
    this.#lastPass = { started: this.#lastStarted!, completed: now, result };
    this.#activity = now; this.#inFlight = false;
  }
  snapshot(): Readonly<EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot> {
    const now = clock(); this.#checkClock(now);
    const activityAgeSeconds = age(now, this.#activity), saved = this.#lastPass;
    return Object.freeze({ clockValid: this.#clockValid, inFlight: this.#inFlight,
      overdue: this.#clockValid && activityAgeSeconds > (this.#inFlight ? this.#maxPassMs : this.#maxIdleMs) / 1000,
      maxIdleSeconds: this.#maxIdleMs / 1000, maxPassSeconds: this.#maxPassMs / 1000,
      activityAt: new Date(this.#activity.wall).toISOString(), activityAgeSeconds,
      ...(this.#lastStarted === undefined ? {} : { lastStartedAt: new Date(this.#lastStarted.wall).toISOString() }),
      ...(saved === undefined ? {} : { lastPass: Object.freeze({ startedAt: new Date(saved.started.wall).toISOString(),
        completedAt: new Date(saved.completed.wall).toISOString(), durationSeconds: Math.max(0, saved.completed.local - saved.started.local) / 1000,
        completionAgeSeconds: age(now, saved.completed), ...(saved.result === undefined ? {} : { result: Object.freeze({ ...saved.result }) }) }) }),
    });
  }
  /** Actual recovery only; applications normally use recoverEventAcknowledgmentIntentsWithMetrics. */
  static async recover(pool: EventConsumerPool, accountId: string, appId: string, metrics: EventAcknowledgmentIntentRecoveryWorkerMetrics,
    options: EventAcknowledgmentIntentRecoveryPassOptions = {}): Promise<EventAcknowledgmentIntentRecoveryPassResult> {
    let copied: EventAcknowledgmentIntentRecoveryPassOptions, account: string, app: string;
    try {
      if (!metrics || typeof metrics !== 'object' || !(#inFlight in metrics)) invalid();
      const policy = recoveryPassPolicy(options);
      intentPool(pool); [account, app] = intentScope(accountId, appId);
      if (account !== metrics.#account || app !== metrics.#app || metrics.#inFlight) invalid();
      copied = Object.freeze({ maxAttempts: policy.maxAttempts, concurrency: policy.concurrency, claimDurationMs: policy.claimDurationMs,
        ...policy.retry, callbackOptions: policy.callbackOptions, signal: policy.signal });
    } catch { return invalid(); }
    // Preserve the existing native zero-attempt AbortError without recording activity.
    if (copied.signal?.aborted) return recoverEventAcknowledgmentIntents(pool, account, app, copied);
    const now = clock(); metrics.#checkClock(now);
    metrics.#activity = metrics.#lastStarted = now; metrics.#inFlight = true;
    let result: EventAcknowledgmentIntentRecoveryPassResult;
    try { result = await recoverEventAcknowledgmentIntents(pool, account, app, copied); }
    catch (error) {
      if (passError(error)) {
        let progress: unknown;
        try { const field = Object.getOwnPropertyDescriptor(error, 'result'); if (field && Object.hasOwn(field, 'value')) progress = field.value; } catch { /* No arbitrary accessors. */ }
        metrics.#finish(progress); // SDK pass exceptions are raised only after admitted work joins.
      }
      // Unknown rejection leaves activity in flight; never manufacture a joined return.
      throw error;
    }
    metrics.#finish(result);
    return result;
  }
}

/** Explicit bounded recovery, preserving the original result/exception and joined
 * cancellation. Validation/sole-cache ownership precede recovery I/O; no scheduler. */
export function recoverEventAcknowledgmentIntentsWithMetrics(pool: EventConsumerPool, accountId: string, appId: string,
  metrics: EventAcknowledgmentIntentRecoveryWorkerMetrics, options: EventAcknowledgmentIntentRecoveryPassOptions = {}): Promise<EventAcknowledgmentIntentRecoveryPassResult> {
  return EventAcknowledgmentIntentRecoveryWorkerMetrics.recover(pool, accountId, appId, metrics, options);
}
