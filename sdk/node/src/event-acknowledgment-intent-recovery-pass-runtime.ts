/** Internal admission/option/pool ownership. Reuses the existing single-intent SQL protocol. */
import { EventConsumerCommitUnknownError, type EventConsumerPool } from './event-consumer-transactions.js';
import { EventAcknowledgmentIntentError, intentStoreError } from './event-acknowledgment-intent-store.js';
import { recoverNextEventAcknowledgmentIntent } from './event-acknowledgment-intent-recovery.js';
import { settings, type EventAcknowledgmentWorkerOptions } from './event-acknowledgment-protocol.js';
import type { EventAcknowledgmentIntentRecoveryPassOptions, EventAcknowledgmentIntentRecoveryPassResult } from './event-acknowledgment-intent-recovery-pass.js';

export interface RecoveryPassPolicy {
  readonly maxAttempts: number; readonly concurrency: number; readonly claimDurationMs: number; readonly backoffMs: number;
  readonly callbackOptions?: Readonly<EventAcknowledgmentWorkerOptions>; readonly signal?: AbortSignal;
}
function invalid(): never { throw new TypeError('invalid acknowledgment intent recovery pass options'); }
function dataObject(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid();
  const names = Reflect.ownKeys(value), data: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
  if (names.length > keys.length) invalid();
  for (const key of names) {
    if (typeof key !== 'string' || !keys.includes(key)) invalid();
    const property = Object.getOwnPropertyDescriptor(value, key);
    if (!property || !Object.hasOwn(property, 'value')) invalid();
    data[key] = property.value;
  }
  return data;
}
function integer(value: unknown, fallback: number, maximum: number, minimum = 1): number {
  if (value === undefined) return fallback;
  if (typeof value !== 'number' || !Number.isInteger(value) || value < minimum || value > maximum) invalid();
  return value;
}
export function recoveryPassPolicy(options: EventAcknowledgmentIntentRecoveryPassOptions): RecoveryPassPolicy {
  try {
    const data = dataObject(options, ['maxAttempts', 'concurrency', 'claimDurationMs', 'backoffMs', 'callbackOptions', 'signal']);
    if (data.signal !== undefined && !(data.signal instanceof AbortSignal)) invalid();
    let callbackOptions: Readonly<EventAcknowledgmentWorkerOptions> | undefined;
    if (data.callbackOptions !== undefined) {
      const callback = dataObject(data.callbackOptions, ['callbackAttempts', 'callbackTimeoutMs', 'retryBackoffMs']);
      callbackOptions = Object.freeze({
        callbackAttempts: callback.callbackAttempts as number | undefined,
        callbackTimeoutMs: callback.callbackTimeoutMs as number | undefined,
        retryBackoffMs: callback.retryBackoffMs as number | undefined,
      });
    }
    settings(callbackOptions ?? {}, 300); // Existing claim validates the selected row's actual lease too.
    return Object.freeze({ maxAttempts: integer(data.maxAttempts, 100, 10000), concurrency: integer(data.concurrency, 4, 32),
      claimDurationMs: integer(data.claimDurationMs, 30000, 300000, 1000), backoffMs: integer(data.backoffMs, 1000, 3600000),
      callbackOptions, signal: data.signal as AbortSignal | undefined });
  } catch { return invalid(); }
}

type Kind = 'confirmed' | 'unconfirmed' | 'superseded' | 'empty' | 'claimLost' | 'interrupted' | 'failed' | 'commitUnknown';
type Counts = { -readonly [Key in Exclude<keyof EventAcknowledgmentIntentRecoveryPassResult, 'stopReason'>]: number };
function kind(error: unknown): Kind {
  try {
    if (error instanceof EventConsumerCommitUnknownError) return 'commitUnknown';
    if (error instanceof EventAcknowledgmentIntentError) {
      if (error.code === 'event_acknowledgment_intent_empty') return 'empty';
      if (error.code === 'event_acknowledgment_intent_claim_lost') return 'claimLost';
    }
    if (error instanceof DOMException && error.name === 'AbortError') return 'interrupted';
  } catch { /* Arbitrary application error properties never escape. */ }
  return 'failed';
}

export async function runRecoveryPass(pool: EventConsumerPool, account: string, app: string, policy: RecoveryPassPolicy):
Promise<{ result: EventAcknowledgmentIntentRecoveryPassResult; unsupportedGeneration: boolean }> {
  const counts: Counts = { attempts: 0, confirmed: 0, unconfirmed: 0, superseded: 0, empty: 0, claimLost: 0,
    interrupted: 0, failed: 0, commitUnknown: 0, cleanupFailures: 0 };
  let closed = false, interrupted = false, unexpected = false, unsupportedGeneration = false;
  const connect = pool.connect.bind(pool);
  const work = new AbortController(), abort = () => work.abort();
  const parent = policy.signal;
  parent?.addEventListener('abort', abort, { once: true });
  if (parent?.aborted) abort();

  async function attempt(): Promise<void> {
    const local = new AbortController(), stop = () => local.abort();
    work.signal.addEventListener('abort', stop, { once: true });
    if (work.signal.aborted) stop();
    let cleanupFailed = false, unknown = false;
    const releaseFailed = (error: unknown): void => {
      cleanupFailed = closed = true;
      const outcome = kind(error);
      unknown ||= outcome === 'commitUnknown';
      interrupted ||= outcome === 'interrupted';
      local.abort(); // Prevent later phases of this admission after invalid pool cleanup.
    };
    const guarded: EventConsumerPool = { connect: async () => {
      if (cleanupFailed) throw intentStoreError();
      const connection = await connect(); // Let the primitive own/release even an acquisition finishing after abort.
      let release: ((discard?: boolean) => void) | undefined;
      try {
        release = connection.release.bind(connection);
        const query = connection.query.bind(connection);
        const delegate = release;
        return Object.freeze({ query, release: (discard?: boolean): void => {
          try { delegate(discard); } catch (error) { releaseFailed(error); }
        } });
      } catch {
        // A malformed connection is a pool contract failure; discard once when possible.
        if (release) {
          try { release(true); } catch (error) { releaseFailed(error); }
        }
        throw intentStoreError();
      }
    } };
    let outcome: Kind = 'failed';
    try {
      const status = await recoverNextEventAcknowledgmentIntent(guarded, account, app, {
        signal: local.signal, claimDurationMs: policy.claimDurationMs, backoffMs: policy.backoffMs,
        callbackOptions: policy.callbackOptions,
      });
      if (status.state !== 'confirmed' && status.state !== 'unconfirmed' && status.state !== 'superseded') throw intentStoreError();
      outcome = status.state;
    } catch (error) {
      outcome = kind(error);
      if (cleanupFailed && outcome === 'interrupted') outcome = 'failed';
      try {
        unsupportedGeneration ||= error instanceof EventAcknowledgmentIntentError
          && error.code === 'event_acknowledgment_intent_unsupported_generation';
      } catch { /* Retain only the fixed compatibility flag. */ }
    } finally { work.signal.removeEventListener('abort', stop); }
    if (outcome !== 'commitUnknown') counts[outcome]++;
    counts.commitUnknown += Number(unknown || outcome === 'commitUnknown');
    counts.cleanupFailures += Number(cleanupFailed);
    interrupted ||= outcome === 'interrupted';
    if (cleanupFailed || outcome === 'empty' || outcome === 'failed' || outcome === 'commitUnknown' || outcome === 'interrupted') closed = true;
  }
  async function worker(): Promise<void> {
    try {
      while (!closed && !work.signal.aborted && counts.attempts < policy.maxAttempts) {
        counts.attempts++; // No await separates the limit check from reservation.
        await attempt();
      }
    } catch (error) { closed = true; throw error; }
  }
  try {
    const workers = Array.from({ length: Math.min(policy.concurrency, policy.maxAttempts) }, () => worker());
    for (const completion of await Promise.allSettled(workers)) {
      if (completion.status === 'rejected') {
        unexpected = true;
        const outcome = kind(completion.reason);
        counts.commitUnknown += Number(outcome === 'commitUnknown');
        interrupted ||= outcome === 'interrupted';
      }
    }
    const stopReason = counts.commitUnknown ? 'commit_unknown' : work.signal.aborted || interrupted ? 'interrupted'
      : counts.failed || counts.cleanupFailures || unexpected ? 'failed' : counts.empty ? 'empty' : 'limit';
    return { result: Object.freeze({ stopReason, ...counts }), unsupportedGeneration };
  } finally { parent?.removeEventListener('abort', abort); }
}
