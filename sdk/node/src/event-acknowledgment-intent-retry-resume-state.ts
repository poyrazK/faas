import { eventConsumerUUID } from './event-consumer-transactions.js';
import { intentHash, intentInteger, intentStoreError, type EventAcknowledgmentIntentStatus } from './event-acknowledgment-intent-store.js';
import { adoptionInstant, adoptionOptions, adoptionSignal, adoptionStatus, type AdoptionSelection } from './event-acknowledgment-intent-policy-adoption-state.js';

export interface EventAcknowledgmentIntentResumeRetryBudgetOptions {
  readonly commandId: string;
  /** Exact microsecond timestamp from getEventAcknowledgmentIntentRetryResumeCandidate. */
  readonly expectedUpdatedAt: string;
  readonly expectedDeliveryAttempts: number;
  readonly expectedDeliveryAttemptLimit: number;
  /** New total, strictly above both the old grant and consumed claims; at most 2147483647. */
  readonly newDeliveryAttemptLimit: number;
  readonly signal?: AbortSignal;
}
/** Historical local evidence, with no callback payload or recovery capability. */
export interface EventAcknowledgmentIntentRetryResumeReceipt {
  readonly commandId: string; readonly invocationId: string; readonly replayGeneration: string;
  readonly attempt: number; readonly outcome: 'resumed' | 'conflict' | 'not_found'; readonly recordedAt: string;
  readonly intent?: EventAcknowledgmentIntentStatus;
}
export interface ResumeRequest extends AdoptionSelection {
  readonly commandId: string; readonly expectedUpdatedAt: string; readonly expectedAt: bigint;
  readonly attempts: number; readonly previousLimit: number; readonly newLimit: number;
  readonly digest: Buffer; readonly signal?: AbortSignal;
}
function invalid(): never { throw new TypeError('invalid acknowledgment retry resume options'); }
function count(value: unknown, minimum: number): number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value < minimum || value > 2147483647) invalid();
  return value;
}
export function resumeRequest(selection: AdoptionSelection, options: EventAcknowledgmentIntentResumeRetryBudgetOptions): ResumeRequest {
  const fields = adoptionOptions(options, ['commandId', 'expectedUpdatedAt', 'expectedDeliveryAttempts', 'expectedDeliveryAttemptLimit', 'newDeliveryAttemptLimit', 'signal']);
  const signal = adoptionSignal(fields.signal), commandId = eventConsumerUUID(fields.commandId as string);
  const expected = adoptionInstant(fields.expectedUpdatedAt), attempts = count(fields.expectedDeliveryAttempts, 0);
  const previousLimit = count(fields.expectedDeliveryAttemptLimit, 1), newLimit = count(fields.newDeliveryAttemptLimit, 1);
  if (newLimit <= previousLimit || newLimit <= attempts) invalid();
  // Same field order, numeric generation and RFC3339 precision as Go. Caller rejects
  // unsupported generations before reaching this conversion or touching storage.
  const body = JSON.stringify({ version: 1, action: 'resume_retry_budget', invocation_id: selection.invocation,
    replay_generation: Number(selection.generation), attempt: selection.attempt, expected_updated_at: expected.text,
    expected_delivery_attempts: attempts, expected_delivery_attempt_limit: previousLimit, new_delivery_attempt_limit: newLimit });
  return Object.freeze({ ...selection, commandId, expectedUpdatedAt: expected.text, expectedAt: expected.at, attempts,
    previousLimit, newLimit, digest: intentHash('gregale-event-ack-intent-retry-resume-v1\n' + body), signal });
}
export interface ResumeHeader extends Omit<EventAcknowledgmentIntentRetryResumeReceipt, 'intent'> { readonly digest: Buffer }
export function resumeHeader(row: Record<string, unknown>): ResumeHeader {
  try {
    if (typeof row.command_id !== 'string' || eventConsumerUUID(row.command_id) !== row.command_id
      || typeof row.invocation_id !== 'string' || eventConsumerUUID(row.invocation_id) !== row.invocation_id
      || typeof row.attempt !== 'number' || !Number.isInteger(row.attempt) || row.attempt < 1 || row.attempt > 2147483647
      || !['resumed', 'conflict', 'not_found'].includes(row.outcome as string)
      || !(row.request_digest instanceof Uint8Array) || row.request_digest.byteLength !== 32) throw intentStoreError();
    return { commandId: row.command_id, invocationId: row.invocation_id,
      replayGeneration: intentInteger(row.replay_generation, 9223372036854775807n), attempt: row.attempt,
      outcome: row.outcome as ResumeHeader['outcome'], recordedAt: adoptionInstant(row.recorded_at).text, digest: Buffer.from(row.request_digest) };
  } catch { throw intentStoreError(); }
}
export function validateResumedStatus(s: EventAcknowledgmentIntentStatus, r: ResumeRequest): void {
  if (s.invocationId !== r.invocation || s.replayGeneration !== r.generation || s.attempt !== r.attempt
    || s.state !== 'unconfirmed' || s.lastCode !== 'unconfirmed' || s.claimExpiresAt !== undefined
    || s.deliveryAttempts < 1 || s.deliveryAttempts !== r.attempts || s.deliveryAttemptLimit !== r.newLimit
    || s.deliveryAttempts >= s.deliveryAttemptLimit || s.retryPolicy?.version !== 2
    || s.retryPolicy.maxDeliveryAttempts > r.previousLimit || adoptionInstant(s.updatedAt).at <= r.expectedAt) throw intentStoreError();
}
export function resumeSnapshot(s: EventAcknowledgmentIntentStatus): string {
  if (s.retryPolicy?.version !== 2) throw intentStoreError();
  return JSON.stringify({ invocation_id: s.invocationId, replay_generation: Number(s.replayGeneration), attempt: s.attempt,
    action: s.action, ...(s.reason ? { reason: s.reason } : {}), state: s.state, delivery_attempts: s.deliveryAttempts,
    delivery_attempt_limit: s.deliveryAttemptLimit, next_attempt_at: s.nextAttemptAt, last_code: s.lastCode,
    created_at: s.createdAt, updated_at: s.updatedAt,
    retry_policy: { version: 2, initial_backoff_ms: s.retryPolicy.initialBackoffMs, max_backoff_ms: s.retryPolicy.maxBackoffMs,
      max_delivery_attempts: s.retryPolicy.maxDeliveryAttempts } });
}
export function resumeReceipt(header: ResumeHeader, raw: unknown, request: ResumeRequest): EventAcknowledgmentIntentRetryResumeReceipt {
  try {
    if (header.commandId !== request.commandId || header.invocationId !== request.invocation || header.replayGeneration !== request.generation
      || header.attempt !== request.attempt || !header.digest.equals(request.digest)) throw intentStoreError();
    let intent: EventAcknowledgmentIntentStatus | undefined;
    if (header.outcome === 'resumed') {
      if (typeof raw !== 'string' || !raw || Buffer.byteLength(raw) > 8192) throw intentStoreError();
      const saved: unknown = JSON.parse(raw);
      if (!saved || typeof saved !== 'object' || Array.isArray(saved)) throw intentStoreError();
      const s = saved as Record<string, unknown>, p = s.retry_policy as Record<string, unknown> | undefined;
      if (typeof s.replay_generation !== 'number' || !Number.isSafeInteger(s.replay_generation)
        || typeof s.delivery_attempts !== 'number' || !Number.isInteger(s.delivery_attempts)
        || typeof s.delivery_attempt_limit !== 'number' || !Number.isInteger(s.delivery_attempt_limit)
        || !p || typeof p !== 'object' || Array.isArray(p) || p.version !== 2
        || typeof p.initial_backoff_ms !== 'number' || !Number.isInteger(p.initial_backoff_ms)
        || typeof p.max_backoff_ms !== 'number' || !Number.isInteger(p.max_backoff_ms)
        || typeof p.max_delivery_attempts !== 'number' || !Number.isInteger(p.max_delivery_attempts)) throw intentStoreError();
      intent = adoptionStatus({ ...s, replay_generation: String(s.replay_generation), delivery_attempts: String(s.delivery_attempts),
        delivery_attempt_limit: String(s.delivery_attempt_limit), reason: s.reason === undefined ? '' : s.reason,
        claim_expires_at: s.claim_expires_at === undefined ? null : s.claim_expires_at,
        retry_policy_version: 2, retry_initial_backoff_ms: String(p.initial_backoff_ms), retry_max_backoff_ms: String(p.max_backoff_ms),
        retry_max_delivery_attempts: String(p.max_delivery_attempts) });
      validateResumedStatus(intent, request);
    } else if (raw !== null) throw intentStoreError();
    return Object.freeze({ commandId: header.commandId, invocationId: header.invocationId, replayGeneration: header.replayGeneration,
      attempt: header.attempt, outcome: header.outcome, recordedAt: header.recordedAt, ...(intent === undefined ? {} : { intent }) });
  } catch { throw intentStoreError(); }
}
