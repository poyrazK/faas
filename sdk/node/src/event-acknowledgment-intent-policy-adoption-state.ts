import { timestamp } from './event-deadline-policy.js';
import { eventConsumerUUID } from './event-consumer-transactions.js';
import { intentHash, intentInteger, intentStatus, intentStoreError,
  type EventAcknowledgmentIntentStatus } from './event-acknowledgment-intent-store.js';
import { intentRetrySchedule, type EventAcknowledgmentIntentRetryPolicy } from './event-acknowledgment-intent-retry-policy.js';

export interface EventAcknowledgmentIntentAdoptRetryPolicyOptions {
  readonly commandId: string;
  /** Exact timestamp from getEventAcknowledgmentIntentPolicyAdoptionCandidate; never round through Date. */
  readonly expectedUpdatedAt: string;
  readonly expectedDeliveryAttempts: number;
  /** Omit for the v1 defaults (1000 ms initial cap, 300000 ms maximum). */
  readonly retryPolicy?: EventAcknowledgmentIntentRetryPolicy;
  readonly signal?: AbortSignal;
}
/** Historical local metadata, not current ownership or remote confirmation. */
export interface EventAcknowledgmentIntentPolicyAdoptionReceipt {
  readonly commandId: string;
  readonly invocationId: string;
  readonly replayGeneration: string;
  readonly attempt: number;
  readonly outcome: 'adopted' | 'conflict' | 'not_found';
  readonly recordedAt: string;
  readonly intent?: EventAcknowledgmentIntentStatus;
}
export interface AdoptionSelection { readonly invocation: string; readonly generation: string; readonly attempt: number }
export interface AdoptionRequest extends AdoptionSelection {
  readonly commandId: string; readonly expectedUpdatedAt: string; readonly expectedAt: bigint;
  readonly attempts: number; readonly initial: number; readonly maximum: number;
  readonly digest: Buffer; readonly signal?: AbortSignal;
}
export function invalidAdoption(): never { throw new TypeError('invalid acknowledgment policy adoption options'); }
function own(value: object, key: string): unknown {
  const property = Object.getOwnPropertyDescriptor(value, key);
  if (property === undefined) return undefined;
  if (!Object.hasOwn(property, 'value')) invalidAdoption();
  return property.value;
}
export function adoptionOptions(value: unknown, keys: readonly string[]): Record<string, unknown> {
  try {
    if (!value || typeof value !== 'object' || Array.isArray(value) || Reflect.ownKeys(value).some(key => typeof key !== 'string' || !keys.includes(key))) invalidAdoption();
    return Object.fromEntries(keys.map(key => [key, own(value, key)]));
  } catch { return invalidAdoption(); }
}
export function adoptionSignal(value: unknown): AbortSignal | undefined {
  if (value !== undefined && !(value instanceof AbortSignal)) invalidAdoption();
  return value as AbortSignal | undefined;
}
/** Canonical UTC RFC3339 at exact PostgreSQL precision, including pre-epoch dates. */
export function adoptionInstant(raw: unknown): { text: string; at: bigint } {
  if (typeof raw !== 'string' || !/^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,6})?(?:Z|[+-][0-9]{2}:[0-9]{2})$/.test(raw)) invalidAdoption();
  const at = timestamp(raw);
  if (at === undefined || at % 1000n !== 0n) invalidAdoption();
  let seconds = at / 1_000_000_000n, nanos = at % 1_000_000_000n;
  if (nanos < 0n) { seconds--; nanos += 1_000_000_000n; }
  const date = new Date(Number(seconds * 1000n));
  if (!Number.isFinite(date.getTime()) || date.getUTCFullYear() < 1 || date.getUTCFullYear() > 9999) invalidAdoption();
  const fraction = (nanos / 1000n).toString().padStart(6, '0').replace(/0+$/, '');
  const text = date.toISOString().slice(0, 19) + (fraction ? '.' + fraction : '') + 'Z';
  if (text === '0001-01-01T00:00:00Z') invalidAdoption();
  return { text, at };
}
export function adoptionSelection(invocationId: string, generation: string, attempt: number): AdoptionSelection {
  const invocation = eventConsumerUUID(invocationId);
  if (typeof generation !== 'string' || !/^(0|[1-9][0-9]{0,18})$/.test(generation) || BigInt(generation) > 9223372036854775807n
    || !Number.isInteger(attempt) || attempt < 1 || attempt > 2147483647) invalidAdoption();
  return Object.freeze({ invocation, generation, attempt });
}
export function adoptionRequest(selection: AdoptionSelection, options: EventAcknowledgmentIntentAdoptRetryPolicyOptions): AdoptionRequest {
  const fields = adoptionOptions(options, ['commandId', 'expectedUpdatedAt', 'expectedDeliveryAttempts', 'retryPolicy', 'signal']);
  const signal = adoptionSignal(fields.signal), commandId = eventConsumerUUID(fields.commandId as string);
  const expected = adoptionInstant(fields.expectedUpdatedAt), attempts = fields.expectedDeliveryAttempts;
  if (typeof attempts !== 'number' || !Number.isInteger(attempts) || attempts < 0 || attempts > 2147483647) invalidAdoption();
  const policy = intentRetrySchedule({ retryPolicy: fields.retryPolicy === undefined ? {} : fields.retryPolicy }).retryPolicy!;
  const body = JSON.stringify({ version: 1, action: 'adopt_retry_policy', invocation_id: selection.invocation,
    replay_generation: Number(selection.generation), attempt: selection.attempt, expected_updated_at: expected.text,
    expected_delivery_attempts: attempts, policy_version: 1, initial_backoff_ms: policy.initialBackoffMs, max_backoff_ms: policy.maxBackoffMs });
  return Object.freeze({ ...selection, commandId, expectedUpdatedAt: expected.text, expectedAt: expected.at, attempts,
    initial: policy.initialBackoffMs, maximum: policy.maxBackoffMs,
    digest: intentHash('gregale-event-ack-intent-policy-adoption-v1\n' + body), signal });
}

// Force text in SQL: driver Date parsers discard the microseconds needed by fences
// and Go-compatible receipt snapshots. CASE rejects infinities and BC/extended years.
export const adoptionStatusColumns = `invocation_id::text,replay_generation::text,attempt,action,reason,state,delivery_attempts::text,
CASE WHEN next_attempt_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz THEN to_char(next_attempt_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS next_attempt_at,
CASE WHEN claim_expires_at IS NULL THEN NULL WHEN claim_expires_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz THEN to_char(claim_expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ELSE 'invalid' END AS claim_expires_at,
last_code,
CASE WHEN created_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz THEN to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS created_at,
CASE WHEN updated_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz THEN to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS updated_at,
retry_policy_version,retry_initial_backoff_ms::text,retry_max_backoff_ms::text,retry_max_delivery_attempts::text,delivery_attempt_limit::text`;
export const adoptionReceiptColumns = `command_id::text,invocation_id::text,replay_generation::text,attempt,outcome,
CASE WHEN recorded_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz THEN to_char(recorded_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS recorded_at,intent_status,request_digest`;

export function adoptionStatus(row?: Record<string, unknown>): EventAcknowledgmentIntentStatus {
  try {
    const status = intentStatus(row);
    return Object.freeze({ ...status, nextAttemptAt: adoptionInstant(row!.next_attempt_at).text,
      ...(row!.claim_expires_at === null ? {} : { claimExpiresAt: adoptionInstant(row!.claim_expires_at).text }),
      createdAt: adoptionInstant(row!.created_at).text, updatedAt: adoptionInstant(row!.updated_at).text });
  } catch { throw intentStoreError(); }
}
export interface AdoptionHeader extends Omit<EventAcknowledgmentIntentPolicyAdoptionReceipt, 'intent'> { readonly digest: Buffer }
export function adoptionHeader(row: Record<string, unknown>): AdoptionHeader {
  try {
    if (typeof row.command_id !== 'string' || eventConsumerUUID(row.command_id) !== row.command_id
      || typeof row.invocation_id !== 'string' || eventConsumerUUID(row.invocation_id) !== row.invocation_id
      || typeof row.attempt !== 'number' || !Number.isInteger(row.attempt) || row.attempt < 1 || row.attempt > 2147483647
      || !['adopted', 'conflict', 'not_found'].includes(row.outcome as string)
      || !(row.request_digest instanceof Uint8Array) || row.request_digest.byteLength !== 32) throw intentStoreError();
    return { commandId: row.command_id, invocationId: row.invocation_id,
      replayGeneration: intentInteger(row.replay_generation, 9223372036854775807n), attempt: row.attempt,
      outcome: row.outcome as AdoptionHeader['outcome'], recordedAt: adoptionInstant(row.recorded_at).text, digest: Buffer.from(row.request_digest) };
  } catch { throw intentStoreError(); }
}
export function adoptionReceipt(header: AdoptionHeader, raw: unknown, request: AdoptionRequest): EventAcknowledgmentIntentPolicyAdoptionReceipt {
  try {
    if (header.commandId !== request.commandId || header.invocationId !== request.invocation || header.replayGeneration !== request.generation
      || header.attempt !== request.attempt || !header.digest.equals(request.digest)) throw intentStoreError();
    let intent: EventAcknowledgmentIntentStatus | undefined;
    if (header.outcome === 'adopted') {
      if (typeof raw !== 'string' || !raw || Buffer.byteLength(raw) > 8192) throw intentStoreError();
      const saved: unknown = JSON.parse(raw);
      if (!saved || typeof saved !== 'object' || Array.isArray(saved)) throw intentStoreError();
      const s = saved as Record<string, unknown>, p = s.retry_policy as Record<string, unknown> | undefined;
      if (typeof s.replay_generation !== 'number' || !Number.isSafeInteger(s.replay_generation)
        || typeof s.delivery_attempts !== 'number' || !Number.isInteger(s.delivery_attempts)
        || !p || typeof p !== 'object' || Array.isArray(p)
        || s.delivery_attempt_limit !== undefined || p.max_delivery_attempts !== undefined || p.version !== 1 || typeof p.initial_backoff_ms !== 'number' || !Number.isInteger(p.initial_backoff_ms)
        || typeof p.max_backoff_ms !== 'number' || !Number.isInteger(p.max_backoff_ms)) throw intentStoreError();
      intent = adoptionStatus({ ...s, replay_generation: String(s.replay_generation), delivery_attempts: String(s.delivery_attempts),
        reason: s.reason === undefined ? '' : s.reason, last_code: s.last_code === undefined ? '' : s.last_code,
        claim_expires_at: s.claim_expires_at === undefined ? null : s.claim_expires_at,
        retry_policy_version: p.version, retry_initial_backoff_ms: String(p.initial_backoff_ms), retry_max_backoff_ms: String(p.max_backoff_ms), retry_max_delivery_attempts: null, delivery_attempt_limit: null });
      validateAdoptedStatus(intent, request);
    } else if (raw !== null) throw intentStoreError();
    return Object.freeze({ commandId: header.commandId, invocationId: header.invocationId, replayGeneration: header.replayGeneration,
      attempt: header.attempt, outcome: header.outcome, recordedAt: header.recordedAt, ...(intent === undefined ? {} : { intent }) });
  } catch { throw intentStoreError(); }
}
export function validateAdoptedStatus(s: EventAcknowledgmentIntentStatus, r: AdoptionRequest): void {
  if (s.invocationId !== r.invocation || s.replayGeneration !== r.generation || s.attempt !== r.attempt
    || (s.state !== 'pending' && s.state !== 'unconfirmed') || (s.lastCode !== '' && s.lastCode !== 'unconfirmed')
    || s.deliveryAttempts !== r.attempts || adoptionInstant(s.updatedAt).at <= r.expectedAt
    || s.retryPolicy?.version !== 1 || s.deliveryAttemptLimit !== undefined || s.retryPolicy?.initialBackoffMs !== r.initial || s.retryPolicy?.maxBackoffMs !== r.maximum) throw intentStoreError();
}
export function adoptionSnapshot(s: EventAcknowledgmentIntentStatus): string {
  return JSON.stringify({ invocation_id: s.invocationId, replay_generation: Number(s.replayGeneration), attempt: s.attempt,
    action: s.action, ...(s.reason ? { reason: s.reason } : {}), state: s.state, delivery_attempts: s.deliveryAttempts,
    next_attempt_at: s.nextAttemptAt, ...(s.claimExpiresAt === undefined ? {} : { claim_expires_at: s.claimExpiresAt }),
    ...(s.lastCode ? { last_code: s.lastCode } : {}), created_at: s.createdAt, updated_at: s.updatedAt,
    retry_policy: { version: 1, initial_backoff_ms: s.retryPolicy!.initialBackoffMs, max_backoff_ms: s.retryPolicy!.maxBackoffMs } });
}
