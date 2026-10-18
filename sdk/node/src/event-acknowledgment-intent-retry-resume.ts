import { intentCommandTransaction } from './event-acknowledgment-intent-command-transaction.js';
import type { EventConsumerPool, EventConsumerTransaction } from './event-consumer-transactions.js';
import { EventAcknowledgmentIntentError, intentScope, intentStoreError, type EventAcknowledgmentIntentStatus } from './event-acknowledgment-intent-store.js';
import { adoptionOptions, adoptionReceiptColumns, adoptionSelection, adoptionSignal, adoptionStatus, adoptionStatusColumns } from './event-acknowledgment-intent-policy-adoption-state.js';
import { resumeHeader, resumeReceipt, resumeRequest, resumeSnapshot, validateResumedStatus,
  type ResumeRequest, type EventAcknowledgmentIntentResumeRetryBudgetOptions,
  type EventAcknowledgmentIntentRetryResumeReceipt } from './event-acknowledgment-intent-retry-resume-state.js';
export { eventAcknowledgmentIntentRetryBudgetSchema, eventAcknowledgmentIntentRetryResumeSchema } from './event-acknowledgment-intent-contract.js';
export type { EventAcknowledgmentIntentResumeRetryBudgetOptions, EventAcknowledgmentIntentRetryResumeReceipt } from './event-acknowledgment-intent-retry-resume-state.js';

/** Known committed/replayed rejection. An ID reused for different inputs has no receipt. */
export class EventAcknowledgmentIntentRetryResumeError extends EventAcknowledgmentIntentError {
  constructor(code: 'event_acknowledgment_intent_conflict' | 'event_acknowledgment_intent_not_found',
    readonly receipt?: EventAcknowledgmentIntentRetryResumeReceipt) {
    super(code); this.name = 'EventAcknowledgmentIntentRetryResumeError';
  }
  override toJSON(): object { return { ...super.toJSON(), ...(this.receipt === undefined ? {} : { receipt: this.receipt }) }; }
}

/** Read exact microsecond fences for an explicit operator decision. Does not acquire
 * a claim/row lock. Diagnostic generations retain int64 text; mutation is safe-integer only. */
export async function getEventAcknowledgmentIntentRetryResumeCandidate(pool: EventConsumerPool, accountId: string, appId: string,
  invocationId: string, replayGeneration: string, attempt: number, options: { readonly signal?: AbortSignal } = {}): Promise<EventAcknowledgmentIntentStatus> {
  const signal = adoptionSignal(adoptionOptions(options, ['signal']).signal), [account, app] = intentScope(accountId, appId);
  const selection = adoptionSelection(invocationId, replayGeneration, attempt);
  return intentCommandTransaction(pool, signal, async tx => {
    const row = (await tx.query(`SELECT ${adoptionStatusColumns} FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4::bigint AND attempt=$5`, [account, app, selection.invocation, selection.generation, selection.attempt])).rows[0];
    if (!row) throw new EventAcknowledgmentIntentRetryResumeError('event_acknowledgment_intent_not_found');
    return { value: adoptionStatus(row), write: false };
  });
}

/** Save the command and original inputs durably before calling. Raises the total
 * grant on one parked v2 intent and retains a receipt atomically. Exact retries
 * replay historical evidence before current row checks; they send no COMMIT.
 * Unknown COMMIT returns no receipt: retry unchanged on a fresh connection.
 * No HTTP, business work, platform renewal or automatic scheduling is performed. */
export async function resumeEventAcknowledgmentIntentRetryBudget(pool: EventConsumerPool, accountId: string, appId: string,
  invocationId: string, replayGeneration: string, attempt: number,
  options: EventAcknowledgmentIntentResumeRetryBudgetOptions): Promise<EventAcknowledgmentIntentRetryResumeReceipt> {
  const [account, app] = intentScope(accountId, appId), selection = adoptionSelection(invocationId, replayGeneration, attempt);
  if (BigInt(selection.generation) > BigInt(Number.MAX_SAFE_INTEGER)) throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_unsupported_generation');
  const request = resumeRequest(selection, options);
  const receipt = await intentCommandTransaction(pool, request.signal, async tx => {
    await tx.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))', ['gregale.event-ack-intent-retry-resume.v1:' + account + ':' + app + ':' + request.commandId]);
    const row = (await tx.query(`SELECT ${adoptionReceiptColumns} FROM public.gregale_event_ack_intent_retry_resumes WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, [account, app, request.commandId])).rows[0];
    if (row) {
      const saved = resumeHeader(row);
      if (saved.commandId !== request.commandId || saved.invocationId !== request.invocation || saved.replayGeneration !== request.generation
        || saved.attempt !== request.attempt || !saved.digest.equals(request.digest)) throw new EventAcknowledgmentIntentRetryResumeError('event_acknowledgment_intent_conflict');
      return { value: resumeReceipt(saved, row.intent_status, request), write: false };
    }
    return { value: await applyResume(tx, account, app, request), write: true };
  });
  if (receipt.outcome !== 'resumed') throw new EventAcknowledgmentIntentRetryResumeError(
    receipt.outcome === 'conflict' ? 'event_acknowledgment_intent_conflict' : 'event_acknowledgment_intent_not_found', receipt);
  return receipt;
}

async function applyResume(tx: EventConsumerTransaction, account: string, app: string, request: ResumeRequest): Promise<EventAcknowledgmentIntentRetryResumeReceipt> {
  const key = [account, app, request.invocation, request.generation, request.attempt];
  await tx.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))', ['gregale.event-ack-intent.v1:' + key.slice(0, 3).join(':')]);
  const existing = (await tx.query(`SELECT ${adoptionStatusColumns} FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4::bigint AND attempt=$5 FOR UPDATE`, key)).rows[0];
  let outcome: EventAcknowledgmentIntentRetryResumeReceipt['outcome'] = 'not_found', intent: EventAcknowledgmentIntentStatus | undefined;
  if (existing) {
    adoptionStatus(existing);
    const row = (await tx.query(`UPDATE public.gregale_event_acknowledgment_intents AS target SET state='unconfirmed',last_code='unconfirmed',delivery_attempt_limit=$9::bigint,updated_at=greatest(clock_timestamp(),target.updated_at+interval '1 microsecond') WHERE target.account_id=$1::uuid AND target.app_id=$2::uuid AND target.invocation_id=$3::uuid AND target.replay_generation=$4::bigint AND target.attempt=$5 AND target.updated_at=$6::timestamptz AND target.delivery_attempts=$7::bigint AND target.delivery_attempt_limit=$8::bigint AND target.state='parked' AND target.retry_policy_version=2 AND target.delivery_attempts>=target.delivery_attempt_limit AND $9::bigint>target.delivery_attempt_limit AND $9::bigint>target.delivery_attempts AND target.claim_token IS NULL AND target.claim_expires_at IS NULL AND NOT EXISTS(SELECT 1 FROM public.gregale_event_acknowledgment_intents newer WHERE newer.account_id=target.account_id AND newer.app_id=target.app_id AND newer.invocation_id=target.invocation_id AND (newer.replay_generation,newer.attempt)>(target.replay_generation,target.attempt)) RETURNING ${resumeStatusColumns}`, [...key, request.expectedUpdatedAt, request.attempts, request.previousLimit, request.newLimit])).rows[0];
    outcome = row ? 'resumed' : 'conflict';
    if (row) { intent = adoptionStatus(row); validateResumedStatus(intent, request); }
  }
  const body = intent === undefined ? null : resumeSnapshot(intent);
  if (body !== null && Buffer.byteLength(body) > 8192) throw intentStoreError();
  const saved = (await tx.query(`INSERT INTO public.gregale_event_ack_intent_retry_resumes(account_id,app_id,command_id,invocation_id,replay_generation,attempt,request_digest,outcome,intent_status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::bigint,$6,$7,$8,$9) RETURNING ${adoptionReceiptColumns}`, [account, app, request.commandId, request.invocation, request.generation, request.attempt, request.digest, outcome, body])).rows[0];
  if (!saved) throw intentStoreError();
  return resumeReceipt(resumeHeader(saved), saved.intent_status, request);
}

// Qualify every target reference: the newer-generation subquery shares these names.
const resumeStatusColumns = `target.invocation_id::text,target.replay_generation::text,target.attempt,target.action,target.reason,target.state,target.delivery_attempts::text,
CASE WHEN target.next_attempt_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz THEN to_char(target.next_attempt_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS next_attempt_at,
CASE WHEN target.claim_expires_at IS NULL THEN NULL WHEN target.claim_expires_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz THEN to_char(target.claim_expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ELSE 'invalid' END AS claim_expires_at,
target.last_code,
CASE WHEN target.created_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz THEN to_char(target.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS created_at,
CASE WHEN target.updated_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz THEN to_char(target.updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS updated_at,
target.retry_policy_version,target.retry_initial_backoff_ms::text,target.retry_max_backoff_ms::text,target.retry_max_delivery_attempts::text,target.delivery_attempt_limit::text`;
