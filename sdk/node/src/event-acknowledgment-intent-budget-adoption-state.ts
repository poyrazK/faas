import { eventConsumerUUID } from './event-consumer-transactions.js';
import { intentHash, intentStoreError, type EventAcknowledgmentIntentStatus } from './event-acknowledgment-intent-store.js';
import { intentStagingPolicy, type EventAcknowledgmentIntentRetryBudget } from './event-acknowledgment-intent-retry-policy.js';
import { adoptionInstant, adoptionOptions, adoptionSignal, adoptionStatus,
  type AdoptionHeader, type AdoptionSelection } from './event-acknowledgment-intent-policy-adoption-state.js';

export interface EventAcknowledgmentIntentAdoptRetryBudgetOptions {
  readonly commandId: string;
  /** Exact timestamp from getEventAcknowledgmentIntentBudgetAdoptionCandidate; never round through Date. */
  readonly expectedUpdatedAt: string;
  readonly expectedDeliveryAttempts: number;
  /** Omit/null requires unbound. Complete v1 metadata must retain its timing. */
  readonly expectedPolicy?: Readonly<{ version: 1; initialBackoffMs: number; maxBackoffMs: number; maxDeliveryAttempts?: 0 }> | null;
  /** Initial total, strictly above consumed claims; copied/validated before I/O. */
  readonly retryBudget: EventAcknowledgmentIntentRetryBudget;
  readonly signal?: AbortSignal;
}
/** Historical local evidence; no current claim, callback data or remote confirmation. */
export interface EventAcknowledgmentIntentBudgetAdoptionReceipt {
  readonly commandId: string; readonly invocationId: string; readonly replayGeneration: string;
  readonly attempt: number; readonly outcome: 'adopted' | 'conflict' | 'not_found'; readonly recordedAt: string;
  readonly intent?: EventAcknowledgmentIntentStatus;
}
export interface BudgetAdoptionRequest extends AdoptionSelection {
  readonly commandId: string; readonly expectedUpdatedAt: string; readonly expectedAt: bigint;
  readonly attempts: number; readonly expectedVersion: number; readonly expectedInitial: number; readonly expectedMaximum: number;
  readonly initial: number; readonly maximum: number; readonly total: number;
  readonly digest: Buffer; readonly signal?: AbortSignal;
}
function invalid(): never { throw new TypeError('invalid acknowledgment budget adoption options'); }
function integer(value: unknown, minimum: number, maximum: number): number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value < minimum || value > maximum) invalid();
  return value;
}
export function budgetAdoptionRequest(selection: AdoptionSelection, options: EventAcknowledgmentIntentAdoptRetryBudgetOptions): BudgetAdoptionRequest {
  const fields = adoptionOptions(options, ['commandId', 'expectedUpdatedAt', 'expectedDeliveryAttempts', 'expectedPolicy', 'retryBudget', 'signal']);
  const signal = adoptionSignal(fields.signal), commandId = eventConsumerUUID(fields.commandId as string);
  const expected = adoptionInstant(fields.expectedUpdatedAt), attempts = integer(fields.expectedDeliveryAttempts, 0, 2147483647);
  const budget = intentStagingPolicy({ retryBudget: fields.retryBudget });
  if (budget?.version !== 2 || budget.maxDeliveryAttempts <= attempts) invalid();
  let expectedVersion = 0, expectedInitial = 0, expectedMaximum = 0;
  if (fields.expectedPolicy !== undefined && fields.expectedPolicy !== null) {
    const policy = adoptionOptions(fields.expectedPolicy, ['version', 'initialBackoffMs', 'maxBackoffMs', 'maxDeliveryAttempts']);
    if (policy.version !== 1 || policy.maxDeliveryAttempts !== undefined && policy.maxDeliveryAttempts !== 0) invalid();
    expectedVersion = 1;
    expectedInitial = integer(policy.initialBackoffMs, 1, 3600000);
    expectedMaximum = integer(policy.maxBackoffMs, expectedInitial, 3600000);
    if (expectedInitial !== budget.initialBackoffMs || expectedMaximum !== budget.maxBackoffMs) invalid();
  }
  // Go's exact ordered framing. The public mutation rejects unsafe generations
  // before I/O and before this Number conversion; diagnostics retain int64 text.
  const body = JSON.stringify({ version: 1, action: 'adopt_retry_budget', invocation_id: selection.invocation,
    replay_generation: Number(selection.generation), attempt: selection.attempt, expected_updated_at: expected.text,
    expected_delivery_attempts: attempts, expected_policy_version: expectedVersion,
    expected_initial_backoff_ms: expectedInitial, expected_max_backoff_ms: expectedMaximum, policy_version: 2,
    initial_backoff_ms: budget.initialBackoffMs, max_backoff_ms: budget.maxBackoffMs, max_delivery_attempts: budget.maxDeliveryAttempts });
  return Object.freeze({ ...selection, commandId, expectedUpdatedAt: expected.text, expectedAt: expected.at, attempts,
    expectedVersion, expectedInitial, expectedMaximum, initial: budget.initialBackoffMs, maximum: budget.maxBackoffMs,
    total: budget.maxDeliveryAttempts, digest: intentHash('gregale-event-ack-intent-budget-adoption-v1\n' + body), signal });
}
export function validateBudgetAdoptionStatus(s: EventAcknowledgmentIntentStatus, r: BudgetAdoptionRequest): void {
  if (s.invocationId !== r.invocation || s.replayGeneration !== r.generation || s.attempt !== r.attempt
    || (s.state !== 'pending' && s.state !== 'unconfirmed') || (s.lastCode !== '' && s.lastCode !== 'unconfirmed')
    || s.deliveryAttempts !== r.attempts || s.deliveryAttemptLimit !== r.total || s.deliveryAttempts >= s.deliveryAttemptLimit
    || s.retryPolicy?.version !== 2 || s.retryPolicy.initialBackoffMs !== r.initial || s.retryPolicy.maxBackoffMs !== r.maximum
    || s.retryPolicy.maxDeliveryAttempts !== r.total || adoptionInstant(s.updatedAt).at <= r.expectedAt) throw intentStoreError();
}
export function budgetAdoptionSnapshot(s: EventAcknowledgmentIntentStatus): string {
  if (s.retryPolicy?.version !== 2) throw intentStoreError();
  return JSON.stringify({ invocation_id: s.invocationId, replay_generation: Number(s.replayGeneration), attempt: s.attempt,
    action: s.action, ...(s.reason ? { reason: s.reason } : {}), state: s.state, delivery_attempts: s.deliveryAttempts,
    next_attempt_at: s.nextAttemptAt, ...(s.claimExpiresAt === undefined ? {} : { claim_expires_at: s.claimExpiresAt }),
    ...(s.lastCode ? { last_code: s.lastCode } : {}), created_at: s.createdAt, updated_at: s.updatedAt,
    retry_policy: { version: 2, initial_backoff_ms: s.retryPolicy.initialBackoffMs, max_backoff_ms: s.retryPolicy.maxBackoffMs,
      max_delivery_attempts: s.retryPolicy.maxDeliveryAttempts }, delivery_attempt_limit: s.deliveryAttemptLimit });
}
export function budgetAdoptionReceipt(header: AdoptionHeader, raw: unknown, request: BudgetAdoptionRequest): EventAcknowledgmentIntentBudgetAdoptionReceipt {
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
        || typeof s.delivery_attempt_limit !== 'number' || !Number.isInteger(s.delivery_attempt_limit)
        || !p || typeof p !== 'object' || Array.isArray(p) || p.version !== 2
        || typeof p.initial_backoff_ms !== 'number' || !Number.isInteger(p.initial_backoff_ms)
        || typeof p.max_backoff_ms !== 'number' || !Number.isInteger(p.max_backoff_ms)
        || typeof p.max_delivery_attempts !== 'number' || !Number.isInteger(p.max_delivery_attempts)) throw intentStoreError();
      intent = adoptionStatus({ ...s, replay_generation: String(s.replay_generation), delivery_attempts: String(s.delivery_attempts),
        delivery_attempt_limit: String(s.delivery_attempt_limit), reason: s.reason === undefined ? '' : s.reason,
        last_code: s.last_code === undefined ? '' : s.last_code,
        claim_expires_at: s.claim_expires_at === undefined ? null : s.claim_expires_at,
        retry_policy_version: 2, retry_initial_backoff_ms: String(p.initial_backoff_ms), retry_max_backoff_ms: String(p.max_backoff_ms),
        retry_max_delivery_attempts: String(p.max_delivery_attempts) });
      validateBudgetAdoptionStatus(intent, request);
    } else if (raw !== null) throw intentStoreError();
    return Object.freeze({ commandId: header.commandId, invocationId: header.invocationId, replayGeneration: header.replayGeneration,
      attempt: header.attempt, outcome: header.outcome, recordedAt: header.recordedAt, ...(intent === undefined ? {} : { intent }) });
  } catch { throw intentStoreError(); }
}
