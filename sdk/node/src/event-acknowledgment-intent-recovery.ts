import { randomBytes } from 'node:crypto';
import { inspect } from 'node:util';
import type { EventConsumerPool } from './event-consumer-transactions.js';
import { eventConsumerResultMaxBytes } from './event-consumer-contract.js';
import { EventAcknowledgmentWorkerError, operationSignal, reasonCode, settings, terminal,
  type EventAcknowledgmentWorkerOperationOptions, type EventAcknowledgmentWorkerOptions } from './event-acknowledgment-protocol.js';
import { EventAcknowledgmentIntentError, EventAcknowledgmentIntentParkedError, intentBytes, intentHash, intentInteger, intentScope, intentStatus, intentStatusColumns,
  intentStoreError, intentTransaction, type EventAcknowledgmentIntentStatus } from './event-acknowledgment-intent-store.js';
import { intentRetrySchedule, intentRetryDelay,
  type EventAcknowledgmentIntentRetryPolicy } from './event-acknowledgment-intent-retry-policy.js';

export interface EventAcknowledgmentIntentClaimOptions extends EventAcknowledgmentWorkerOperationOptions {
  /** Whole milliseconds, 1000..300000; default 30000. Uses database time. */
  claimDurationMs?: number;
  /** Optional validation of the intended delivery budget before committing the claim. */
  callbackOptions?: EventAcknowledgmentWorkerOptions;
}
export interface EventAcknowledgmentIntentDeliveryOptions extends EventAcknowledgmentWorkerOperationOptions {
  /** Fixed delay after uncertainty, whole milliseconds 1..3600000; default 1000. Omit with retryPolicy. */
  backoffMs?: number;
  /** Optional capped exponential jitter based on the durable recovery-claim count. Copied before I/O. */
  retryPolicy?: EventAcknowledgmentIntentRetryPolicy;
  callbackOptions?: EventAcknowledgmentWorkerOptions;
}
export interface EventAcknowledgmentIntentRecoveryOptions extends EventAcknowledgmentIntentClaimOptions, EventAcknowledgmentIntentDeliveryOptions {}
interface SavedIntent {
  readonly account: string; readonly app: string; readonly invocation: string; readonly generation: string; readonly attempt: number;
  readonly claim: Buffer; readonly identity: Buffer; readonly token: string; readonly slug: string; readonly seconds: number;
  readonly deliveryAttempts: number;
  readonly deliveryAttemptLimit?: number;
  readonly retryPolicy?: Readonly<Required<EventAcknowledgmentIntentRetryPolicy>>;
  readonly action: 'complete' | 'retry' | 'dead-letter'; readonly reason: string; readonly body?: string;
}
function savedIntent(row: Record<string, unknown>, account: string, app: string, claim: Buffer): SavedIntent {
  const status = intentStatus(row);
  if (BigInt(status.replayGeneration) > BigInt(Number.MAX_SAFE_INTEGER)) throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_unsupported_generation');
  if (typeof row.token !== 'string' || !/^[0-9a-f]{64}$/.test(row.token)
    || typeof row.app_slug !== 'string' || !/^[a-z0-9][a-z0-9-]{0,252}$/.test(row.app_slug)
    || typeof row.lease_seconds !== 'number' || !Number.isInteger(row.lease_seconds) || row.lease_seconds < 1 || row.lease_seconds > 300
    || !(row.identity_digest instanceof Uint8Array) || row.identity_digest.byteLength !== 32
    || !(row.request_digest instanceof Uint8Array) || row.request_digest.byteLength !== 32) throw intentStoreError();
  let body: string | undefined;
  if (status.action === 'complete') {
    if (typeof row.result_body !== 'string' || !row.result_body || Buffer.byteLength(row.result_body) > eventConsumerResultMaxBytes
      || row.result_body.includes('\0') || Buffer.from(row.result_body).toString('utf8') !== row.result_body) throw intentStoreError();
    try { JSON.parse(row.result_body); } catch { throw intentStoreError(); }
    body = row.result_body;
  } else if (!reasonCode(status.reason) || row.result_body !== null) throw intentStoreError();
  if (!intentBytes(row.capability_digest, intentHash(row.token))
    || !intentBytes(row.action_digest, intentHash(status.action + '\n' + status.reason + '\n' + (body ?? '')))) throw intentStoreError();
  return Object.freeze({ account, app, invocation: status.invocationId, generation: status.replayGeneration, attempt: status.attempt,
    claim, identity: Buffer.from(row.identity_digest), token: row.token, slug: row.app_slug, seconds: row.lease_seconds,
    action: status.action, reason: status.reason, body, deliveryAttempts: status.deliveryAttempts, deliveryAttemptLimit: status.deliveryAttemptLimit,
    ...(status.retryPolicy === undefined ? {} : { retryPolicy: Object.freeze({ initialBackoffMs: status.retryPolicy.initialBackoffMs, maxBackoffMs: status.retryPolicy.maxBackoffMs }) }) });
}

/** Process-local recovery capability. Private fields, formatting and JSON redact all secrets.
 * A claim sends only its stored callback; it cannot run business code or renew the platform lease. */
export class EventAcknowledgmentIntentClaim {
  readonly #saved: SavedIntent;
  #busy = false;
  private constructor(saved: SavedIntent) { this.#saved = saved; Object.freeze(this); }
  toJSON(): string { return this.toString(); }
  toString(): string { return 'EventAcknowledgmentIntentClaim{redacted}'; }
  [inspect.custom](): string { return this.toString(); }

  static async claim(pool: EventConsumerPool, accountId: string, appId: string,
    options: EventAcknowledgmentIntentClaimOptions = {}): Promise<EventAcknowledgmentIntentClaim> {
    const signal = operationSignal(options), [account, app] = intentScope(accountId, appId);
    const duration = options.claimDurationMs === undefined ? 30000 : options.claimDurationMs;
    if (!Number.isInteger(duration) || duration < 1000 || duration > 300000) throw new TypeError('invalid acknowledgment intent claim duration');
    const claim = randomBytes(32);
    const outcome = await intentTransaction(pool, signal, async tx => {
      const row = (await tx.query(`SELECT ${intentStatusColumns},token,app_slug,lease_seconds,result_body,identity_digest,request_digest,action_digest,capability_digest FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND state IN ('pending','unconfirmed') AND next_attempt_at<=clock_timestamp() AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) ORDER BY next_attempt_at,created_at,invocation_id,replay_generation,attempt LIMIT 1 FOR UPDATE SKIP LOCKED`, [account, app])).rows[0];
      if (!row) throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_empty');
      const saved = savedIntent(row, account, app, claim);
      if (saved.deliveryAttemptLimit !== undefined && saved.deliveryAttempts >= saved.deliveryAttemptLimit) {
        const parked = (await tx.query(`UPDATE public.gregale_event_acknowledgment_intents SET state='parked',last_code='attempts_exhausted',claim_token=NULL,claim_expires_at=NULL,updated_at=greatest(clock_timestamp(),updated_at+interval '1 microsecond') WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4::bigint AND attempt=$5 AND state IN ('pending','unconfirmed') AND retry_policy_version=2 AND delivery_attempts>=delivery_attempt_limit AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) RETURNING ${intentStatusColumns}`,
          [account, app, saved.invocation, saved.generation, saved.attempt])).rows[0];
        const status = intentStatus(parked);
        if (status.state !== 'parked') throw intentStoreError();
        return Object.freeze({ parked: status });
      }
      if (options.callbackOptions !== undefined) settings(options.callbackOptions, saved.seconds);
      const updated = (await tx.query(`UPDATE public.gregale_event_acknowledgment_intents SET claim_token=$6,claim_expires_at=clock_timestamp()+$7::bigint*interval '1 millisecond',delivery_attempts=least(delivery_attempts+1,2147483647),updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4::bigint AND attempt=$5 RETURNING delivery_attempts::text`,
        [account, app, saved.invocation, saved.generation, saved.attempt, claim, duration])).rows[0];
      const deliveryAttempts = Number(intentInteger(updated?.delivery_attempts, 2147483647n));
      if (deliveryAttempts < 1) throw intentStoreError();
      return Object.freeze({ saved: Object.freeze({ ...saved, deliveryAttempts }) });
    });
    if ('parked' in outcome) throw new EventAcknowledgmentIntentParkedError(outcome.parked!);
    return new EventAcknowledgmentIntentClaim(outcome.saved);
  }
  /** DB claim expiry bounds HTTP. Settlement rechecks the token and DB expiry. */
  async deliver(pool: EventConsumerPool, options: EventAcknowledgmentIntentDeliveryOptions = {}): Promise<EventAcknowledgmentIntentStatus> {
    const signal = operationSignal(options), schedule = intentRetrySchedule(options), s = this.#saved;
    const delay = intentRetryDelay(s.retryPolicy === undefined ? schedule : { retryPolicy: s.retryPolicy }, s.claim, s.deliveryAttempts);
    const budget = settings(options.callbackOptions ?? {}, s.seconds);
    if (this.#busy) throw new EventAcknowledgmentWorkerError('event_acknowledgment_worker_busy');
    this.#busy = true;
    try {
      const key = [s.account, s.app, s.invocation, s.generation, s.attempt, s.claim];
      const sent = performance.now();
      const evidence = await intentTransaction(pool, signal, async tx => {
        const row = (await tx.query(`SELECT extract(epoch FROM claim_expires_at-clock_timestamp())::double precision AS remaining FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4::bigint AND attempt=$5 AND state IN ('pending','unconfirmed') AND claim_token=$6 AND claim_expires_at>clock_timestamp()`, key)).rows[0];
        if (!row) throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_claim_lost');
        let committed = false;
        if (s.action !== 'complete') {
          const receipt = (await tx.query(`SELECT EXISTS(SELECT 1 FROM public.gregale_event_consumer_inbox WHERE identity_digest=$1 AND account_id=$2::uuid AND app_id=$3::uuid) AS committed`, [s.identity, s.account, s.app])).rows[0];
          if (typeof receipt?.committed !== 'boolean') throw intentStoreError();
          committed = receipt.committed;
        }
        return { remaining: row.remaining, committed };
      });
      if (typeof evidence.remaining !== 'number' || !Number.isFinite(evidence.remaining) || evidence.remaining <= 0 || evidence.remaining > 300) {
        throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_claim_lost');
      }
      // Subtract all elapsed local time, including DB round trips and commit.
      const remainingMs = Math.floor(evidence.remaining * 1000 - (performance.now() - sent));
      if (remainingMs <= 0) throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_claim_lost');
      const controller = new AbortController(), abort = () => controller.abort();
      signal?.addEventListener('abort', abort, { once: true });
      if (signal?.aborted) abort();
      const timer = setTimeout(abort, remainingMs);
      let error: EventAcknowledgmentWorkerError | undefined;
      try {
        if (evidence.committed) error = new EventAcknowledgmentWorkerError('event_consumer_receipt_committed', { receiptConfirmed: true });
        else {
          await terminal({ app: s.slug, invocationId: s.invocation, token: s.token }, budget,
            { invocationId: s.invocation, replayGeneration: s.generation, attempt: s.attempt }, s.action,
            s.action === 'complete' ? s.body! : s.reason, { receiptConfirmed: false }, controller.signal);
        }
      } catch (caught) {
        // All callback errors are already sanitized. Never retain an arbitrary cause.
        error = caught instanceof EventAcknowledgmentWorkerError ? caught : new EventAcknowledgmentWorkerError('event_acknowledgment_unconfirmed');
      } finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); controller.abort(); }
      const exhausted = s.deliveryAttemptLimit !== undefined && s.deliveryAttempts >= s.deliveryAttemptLimit;
      const state = !error ? 'confirmed' : error.code === 'event_acknowledgment_lease_lost' || error.code === 'event_consumer_receipt_committed' ? 'superseded' : exhausted ? 'parked' : 'unconfirmed';
      const code = !error ? 'callback_confirmed' : error.code === 'event_consumer_receipt_committed' ? 'receipt_committed' : error.code === 'event_acknowledgment_lease_lost' ? 'lease_lost' : exhausted ? 'attempts_exhausted' : 'unconfirmed';
      // A failed settlement is primary: the caller cannot assume the row changed.
      const status = await intentTransaction(pool, signal, async tx => {
        const row = (await tx.query(`UPDATE public.gregale_event_acknowledgment_intents SET state=$7,last_code=$8,token=CASE WHEN $7 IN ('confirmed','superseded') THEN NULL ELSE token END,result_body=CASE WHEN $7 IN ('confirmed','superseded') THEN NULL ELSE result_body END,claim_token=NULL,claim_expires_at=NULL,next_attempt_at=CASE WHEN $7='unconfirmed' THEN clock_timestamp()+$9::bigint*interval '1 millisecond' ELSE next_attempt_at END,updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4::bigint AND attempt=$5 AND state IN ('pending','unconfirmed') AND claim_token=$6 AND claim_expires_at>clock_timestamp() RETURNING ${intentStatusColumns}`, [...key, state, code, delay])).rows[0];
        if (!row) throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_claim_lost');
        return intentStatus(row);
      });
      // The state/code describe callback uncertainty without exposing its response.
      return status;
    } finally { this.#busy = false; }
  }
}
export const claimEventAcknowledgmentIntent = (pool: EventConsumerPool, accountId: string, appId: string,
  options: EventAcknowledgmentIntentClaimOptions = {}): Promise<EventAcknowledgmentIntentClaim> => EventAcknowledgmentIntentClaim.claim(pool, accountId, appId, options);
/** One bounded iteration; the host owns polling, concurrency, backoff and lifetime. */
export async function recoverNextEventAcknowledgmentIntent(pool: EventConsumerPool, accountId: string, appId: string,
  options: EventAcknowledgmentIntentRecoveryOptions = {}): Promise<EventAcknowledgmentIntentStatus> {
  const signal = operationSignal(options), schedule = intentRetrySchedule(options), callbackOptions = options.callbackOptions;
  try {
    const claim = await claimEventAcknowledgmentIntent(pool, accountId, appId, {
      signal, claimDurationMs: options.claimDurationMs, callbackOptions,
    });
    return await claim.deliver(pool, { signal, callbackOptions, ...schedule });
  } catch (error) {
    if (error instanceof EventAcknowledgmentIntentParkedError) return error.intent;
    throw error;
  }
}
