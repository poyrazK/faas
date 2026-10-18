// Application-owned intent protocol shared with the Go SDK. Never expose SQL diagnostics.
import { createHash } from 'node:crypto';
import { inspect } from 'node:util';
import { eventConsumerDigests, eventConsumerUUID, EventConsumerCommitUnknownError, EventConsumerConflictError,
  type EventConsumerPool, type EventConsumerRequest, type EventConsumerTransaction,
  type EventConsumerTransactionResult } from './event-consumer-transactions.js';
import { EventAcknowledgmentWorkerError, operationSignal, reasonCode, type AcknowledgmentDelivery,
  type EventAcknowledgmentWorkerOperationOptions } from './event-acknowledgment-protocol.js';
import type { AcknowledgmentWorkerScope } from './event-acknowledgment-callbacks.js';
import type { EventAcknowledgmentIntentRetryPolicy, EventAcknowledgmentIntentRetryBudget, EventAcknowledgmentIntentStoredRetryPolicy } from './event-acknowledgment-intent-retry-policy.js';
export { eventAcknowledgmentIntentSchema, eventAcknowledgmentIntentStoredRetryPolicySchema } from './event-acknowledgment-intent-contract.js';

export interface EventAcknowledgmentIntentStagingOptions extends EventAcknowledgmentWorkerOperationOptions {
  /** Immutable binding for a new intent. Omit to preserve legacy unbound staging. */
  readonly retryPolicy?: EventAcknowledgmentIntentRetryPolicy;
  /** Mutually exclusive with retryPolicy. Defaults to unlimited when omitted. */
  readonly retryBudget?: EventAcknowledgmentIntentRetryBudget;
}

export type EventAcknowledgmentIntentErrorCode = 'event_acknowledgment_intent_conflict' | 'event_acknowledgment_intent_superseded'
  | 'event_acknowledgment_intent_store' | 'event_acknowledgment_intent_empty' | 'event_acknowledgment_intent_claim_lost'
  | 'event_acknowledgment_intent_parked' | 'event_acknowledgment_intent_unsupported_generation' | 'event_acknowledgment_intent_not_found';
const messages: Record<EventAcknowledgmentIntentErrorCode, string> = {
  event_acknowledgment_intent_conflict: 'immutable acknowledgment intent conflicts',
  event_acknowledgment_intent_superseded: 'acknowledgment intent has a newer claim',
  event_acknowledgment_intent_store: 'acknowledgment intent storage operation failed',
  event_acknowledgment_intent_empty: 'no acknowledgment intent is due',
  event_acknowledgment_intent_claim_lost: 'acknowledgment intent recovery lease expired or replaced',
  event_acknowledgment_intent_unsupported_generation: 'acknowledgment generation exceeds the Node safe-integer range',
  event_acknowledgment_intent_parked: 'acknowledgment intent retry budget exhausted',
  event_acknowledgment_intent_not_found: 'acknowledgment intent was not found',
};
export class EventAcknowledgmentIntentError extends Error {
  constructor(readonly code: EventAcknowledgmentIntentErrorCode) { super(messages[code]); this.name = 'EventAcknowledgmentIntentError'; }
  toJSON(): object { return { name: this.name, code: this.code }; }
  [inspect.custom](): object { return this.toJSON(); }
}
/** Metadata only: no original event, completion result, platform token or recovery capability. */
export interface EventAcknowledgmentIntentStatus {
  readonly invocationId: string;
  /** Canonical decimal int64; diagnostics preserve generations above 2^53. */
  readonly replayGeneration: string;
  readonly attempt: number;
  readonly action: 'complete' | 'retry' | 'dead-letter';
  readonly reason: string;
  readonly state: 'pending' | 'unconfirmed' | 'parked' | 'confirmed' | 'superseded';
  readonly deliveryAttempts: number;
  /** Current total claim grant; absent for unbound/v1 rows. */
  readonly deliveryAttemptLimit?: number;
  readonly nextAttemptAt: string;
  readonly claimExpiresAt?: string;
  readonly lastCode: '' | 'unconfirmed' | 'lease_lost' | 'callback_confirmed' | 'claim_replaced' | 'receipt_committed' | 'attempts_exhausted';
  readonly createdAt: string;
  readonly updatedAt: string;
  readonly retryPolicy?: Readonly<EventAcknowledgmentIntentStoredRetryPolicy>;
}
export interface EventAcknowledgmentIntentResult {
  readonly receiptConfirmed: true;
  readonly receipt: Readonly<EventConsumerTransactionResult>;
  readonly intent: EventAcknowledgmentIntentStatus;
}
/** Known committed parking metadata; never a recovery capability or remote result. */
export class EventAcknowledgmentIntentParkedError extends EventAcknowledgmentIntentError {
  constructor(readonly intent: EventAcknowledgmentIntentStatus) {
    super('event_acknowledgment_intent_parked'); this.name = 'EventAcknowledgmentIntentParkedError';
  }
  override toJSON(): object { return { ...super.toJSON(), intent: this.intent }; }
}
export interface IntentInput {
  readonly input: EventConsumerRequest;
  readonly scope: AcknowledgmentWorkerScope;
  readonly delivery: AcknowledgmentDelivery;
  readonly seconds: number;
}
export const intentStatusColumns = `invocation_id::text,replay_generation::text,attempt,action,reason,state,delivery_attempts::text,next_attempt_at,claim_expires_at,last_code,created_at,updated_at,retry_policy_version,retry_initial_backoff_ms::text,retry_max_backoff_ms::text,retry_max_delivery_attempts::text,delivery_attempt_limit::text`;
export const intentStoreError = (): EventAcknowledgmentIntentError => new EventAcknowledgmentIntentError('event_acknowledgment_intent_store');
export function intentScope(accountId: string, appId: string): readonly [string, string] {
  return [eventConsumerUUID(accountId), eventConsumerUUID(appId)];
}
export function intentPool(pool: EventConsumerPool): void {
  if (!pool || typeof pool.connect !== 'function') throw new TypeError('invalid event consumer pool');
}
export function intentInteger(value: unknown, maximum: bigint): string {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]{0,18})$/.test(value) || BigInt(value) > maximum) throw intentStoreError();
  return value;
}
export function intentInstant(value: unknown): string {
  const date = value instanceof Date ? value : typeof value === 'string' && /^\d{4}-\d{2}-\d{2}T/.test(value) ? new Date(value) : undefined;
  if (!date || !Number.isFinite(date.getTime()) || date.getUTCFullYear() < 1 || date.getUTCFullYear() > 9999) throw intentStoreError();
  return date.toISOString();
}
export function intentStatus(row?: Record<string, unknown>): EventAcknowledgmentIntentStatus {
  if (!row || typeof row.invocation_id !== 'string' || eventConsumerUUID(row.invocation_id) !== row.invocation_id
    || typeof row.attempt !== 'number' || !Number.isInteger(row.attempt) || row.attempt < 1 || row.attempt > 2147483647
    || !['complete', 'retry', 'dead-letter'].includes(row.action as string)
    || typeof row.reason !== 'string' || (row.action === 'complete' ? row.reason !== '' : !reasonCode(row.reason))
    || !['pending', 'unconfirmed', 'parked', 'confirmed', 'superseded'].includes(row.state as string)
    || !['', 'unconfirmed', 'lease_lost', 'callback_confirmed', 'claim_replaced', 'receipt_committed', 'attempts_exhausted'].includes(row.last_code as string)) throw intentStoreError();
  const policy = intentStoredRetryPolicy(row);
  const deliveryAttempts = Number(intentInteger(row.delivery_attempts, 2147483647n));
  const deliveryAttemptLimit = policy?.version === 2 ? Number(intentInteger(row.delivery_attempt_limit, 2147483647n)) : undefined;
  if ((row.state === 'parked') !== (row.last_code === 'attempts_exhausted')
    || row.state === 'parked' && (deliveryAttemptLimit === undefined || deliveryAttempts < deliveryAttemptLimit || row.claim_expires_at !== null)) throw intentStoreError();
  return Object.freeze({ invocationId: row.invocation_id,
    replayGeneration: intentInteger(row.replay_generation, 9223372036854775807n), attempt: row.attempt,
    action: row.action as EventAcknowledgmentIntentStatus['action'], reason: row.reason,
    state: row.state as EventAcknowledgmentIntentStatus['state'],
    deliveryAttempts, ...(deliveryAttemptLimit === undefined ? {} : { deliveryAttemptLimit }), nextAttemptAt: intentInstant(row.next_attempt_at),
    ...(row.claim_expires_at == null ? {} : { claimExpiresAt: intentInstant(row.claim_expires_at) }),
    lastCode: row.last_code as EventAcknowledgmentIntentStatus['lastCode'], createdAt: intentInstant(row.created_at), updatedAt: intentInstant(row.updated_at),
    ...(policy === undefined ? {} : { retryPolicy: policy }) });
}
function intentStoredRetryPolicy(row: Record<string, unknown>): Readonly<EventAcknowledgmentIntentStoredRetryPolicy> | undefined {
  const version = row.retry_policy_version, initial = row.retry_initial_backoff_ms, maximum = row.retry_max_backoff_ms;
  const budget = row.retry_max_delivery_attempts, grant = row.delivery_attempt_limit;
  if (version === null && initial === null && maximum === null && budget === null && grant === null) return undefined;
  if (version !== 1 && version !== 2) throw intentStoreError();
  if (version === 1 && (budget !== null || grant !== null)) throw intentStoreError();
  const initialBackoffMs = Number(intentInteger(initial, 3600000n));
  const maxBackoffMs = Number(intentInteger(maximum, 3600000n));
  if (initialBackoffMs < 1 || maxBackoffMs < initialBackoffMs) throw intentStoreError();
  if (version === 1) return Object.freeze({ version: 1, initialBackoffMs, maxBackoffMs });
  const maxDeliveryAttempts = Number(intentInteger(budget, 2147483647n)), limit = Number(intentInteger(grant, 2147483647n));
  if (maxDeliveryAttempts < 1 || limit < maxDeliveryAttempts) throw intentStoreError();
  return Object.freeze({ version: 2, initialBackoffMs, maxBackoffMs, maxDeliveryAttempts });
}
export function intentHash(value: string): Buffer { return createHash('sha256').update(value).digest(); }
export function intentBytes(value: unknown, expected: Buffer): boolean {
  return value instanceof Uint8Array && expected.equals(Buffer.from(value));
}
/** Sanitizes storage errors and never returns a result after an uncertain commit. */
export async function intentTransaction<T>(pool: EventConsumerPool, signal: AbortSignal | undefined,
  operation: (tx: EventConsumerTransaction) => Promise<T>): Promise<T> {
  intentPool(pool); signal?.throwIfAborted();
  const storage = async <V>(operation: () => Promise<V>): Promise<V> => {
    try { return await operation(); } catch { throw intentStoreError(); }
  };
  const connection = await storage(() => pool.connect());
  const query: EventConsumerTransaction['query'] = async (sql, values) => {
    signal?.throwIfAborted();
    const result = await storage(() => connection.query(sql, values));
    signal?.throwIfAborted(); return result;
  };
  let discard = false, failed = false, failure: unknown, result: T | undefined;
  try {
    await query('BEGIN ISOLATION LEVEL READ COMMITTED');
    result = await operation(Object.freeze({ query }));
    signal?.throwIfAborted();
    try { await connection.query('COMMIT'); }
    catch { discard = true; throw new EventConsumerCommitUnknownError(intentStoreError()); }
  } catch (error) {
    failed = true; failure = error;
    try { await connection.query('ROLLBACK'); } catch { discard = true; }
  } finally {
    try { connection.release(discard); }
    catch { if (!failed) { failed = true; failure = intentStoreError(); } }
  }
  if (failed) throw failure;
  return result!;
}
export async function saveIntent(tx: EventConsumerTransaction, worker: IntentInput, action: EventAcknowledgmentIntentStatus['action'],
  reason: string, body?: string, policy?: Readonly<EventAcknowledgmentIntentStoredRetryPolicy>): Promise<EventAcknowledgmentIntentStatus> {
  const { input, scope, delivery, seconds } = worker;
  const key = [input.accountId, input.appId, delivery.invocationId];
  const { identity, request } = eventConsumerDigests(input);
  const capability = intentHash(scope.token), payload = intentHash(action + '\n' + reason + '\n' + (body ?? ''));
  await tx.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))', ['gregale.event-ack-intent.v1:' + key.join(':')]);
  const old = (await tx.query(`SELECT replay_generation::text,attempt,identity_digest,request_digest,capability_digest,action_digest,app_slug,lease_seconds FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid ORDER BY replay_generation DESC,attempt DESC LIMIT 1 FOR UPDATE`, key)).rows[0];
  if (old) {
    if (!intentBytes(old.identity_digest, identity) || !intentBytes(old.request_digest, request)) throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_conflict');
    const generation = BigInt(intentInteger(old.replay_generation, 9223372036854775807n));
    if (typeof old.attempt !== 'number' || !Number.isInteger(old.attempt) || old.attempt < 1 || old.attempt > 2147483647) throw intentStoreError();
    if (generation > BigInt(delivery.replayGeneration) || generation === BigInt(delivery.replayGeneration) && old.attempt > delivery.attempt) {
      throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_superseded');
    }
    if (generation === BigInt(delivery.replayGeneration) && old.attempt === delivery.attempt) {
      if (!intentBytes(old.capability_digest, capability) || !intentBytes(old.action_digest, payload)
        || old.app_slug !== scope.app || old.lease_seconds !== seconds) throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_conflict');
      const status = intentStatus((await tx.query(`SELECT ${intentStatusColumns} FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4::bigint AND attempt=$5`, [...key, delivery.replayGeneration, delivery.attempt])).rows[0]);
      if (status.state === 'superseded') throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_superseded');
      if (policy !== undefined && (status.retryPolicy === undefined || status.retryPolicy.version !== policy.version || status.retryPolicy.maxDeliveryAttempts !== policy.maxDeliveryAttempts || status.retryPolicy.initialBackoffMs !== policy.initialBackoffMs
        || status.retryPolicy.maxBackoffMs !== policy.maxBackoffMs)) throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_conflict');
      return status;
    }
  }
  await tx.query(`UPDATE public.gregale_event_acknowledgment_intents SET state='superseded',token=NULL,result_body=NULL,claim_token=NULL,claim_expires_at=NULL,last_code='claim_replaced',updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND state IN ('pending','unconfirmed','parked')`, key);
  return intentStatus((await tx.query(`INSERT INTO public.gregale_event_acknowledgment_intents(account_id,app_id,invocation_id,replay_generation,attempt,identity_digest,request_digest,capability_digest,action_digest,app_slug,lease_seconds,action,reason,token,result_body,state,retry_policy_version,retry_initial_backoff_ms,retry_max_backoff_ms,retry_max_delivery_attempts,delivery_attempt_limit) VALUES($1::uuid,$2::uuid,$3::uuid,$4::bigint,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'pending',$16,$17,$18,$19,$19) RETURNING ${intentStatusColumns}`,
    [...key, delivery.replayGeneration, delivery.attempt, identity, request, capability, payload, scope.app, seconds, action, reason, scope.token, body ?? null,
      policy?.version ?? null, policy?.initialBackoffMs ?? null, policy?.maxBackoffMs ?? null, policy?.maxDeliveryAttempts ?? null])).rows[0]);
}
export async function saveNegativeIntent(pool: EventConsumerPool, worker: IntentInput, action: 'retry' | 'dead-letter', reason: string,
  signal?: AbortSignal, policy?: Readonly<EventAcknowledgmentIntentStoredRetryPolicy>): Promise<EventAcknowledgmentIntentStatus> {
  if (!reasonCode(reason)) throw new TypeError('invalid event acknowledgment failure reason');
  return intentTransaction(pool, signal, async tx => {
    const { input } = worker, { identity, request } = eventConsumerDigests(input);
    await tx.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))', ['gregale.event-consumer-inbox.v1:' + identity.toString('hex')]);
    const row = (await tx.query(`SELECT account_id::text,app_id::text,subscription_id::text,acceptance_id::text,event_source,event_id,request_digest FROM public.gregale_event_consumer_inbox WHERE identity_digest=$1`, [identity])).rows[0];
    if (row) {
      if (!intentBytes(row.request_digest, request) || row.account_id !== input.accountId || row.app_id !== input.appId
        || row.subscription_id !== input.subscriptionId || row.acceptance_id !== input.acceptanceId
        || row.event_source !== input.source || row.event_id !== input.eventId) throw new EventConsumerConflictError();
      throw new EventAcknowledgmentWorkerError('event_consumer_receipt_committed', { receiptConfirmed: true });
    }
    return saveIntent(tx, worker, action, reason, undefined, policy);
  });
}
/** At most 100 retained metadata rows. Omit invocationId to inspect the owner scope. */
export async function listEventAcknowledgmentIntents(pool: EventConsumerPool, accountId: string, appId: string,
  options: EventAcknowledgmentWorkerOperationOptions & { invocationId?: string; limit?: number } = {}): Promise<readonly EventAcknowledgmentIntentStatus[]> {
  const signal = operationSignal(options), [account, app] = intentScope(accountId, appId);
  const invocation = options.invocationId === undefined || options.invocationId === '' ? '' : eventConsumerUUID(options.invocationId);
  const limit = options.limit === undefined ? 25 : options.limit;
  if (!Number.isInteger(limit) || limit < 1 || limit > 100) throw new TypeError('invalid acknowledgment intent diagnostic limit');
  return intentTransaction(pool, signal, async tx => Object.freeze((await tx.query(`SELECT ${intentStatusColumns} FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND ($3='' OR invocation_id::text=$3) ORDER BY updated_at DESC,invocation_id,replay_generation DESC,attempt DESC LIMIT $4`, [account, app, invocation, limit])).rows.map(row => intentStatus(row))));
}
