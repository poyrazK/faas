import { intentCommandTransaction } from './event-acknowledgment-intent-command-transaction.js';
import { inspect } from 'node:util';
import { type EventConsumerPool, type EventConsumerTransaction } from './event-consumer-transactions.js';
import { EventAcknowledgmentIntentError, intentScope, intentStoreError,
  type EventAcknowledgmentIntentStatus } from './event-acknowledgment-intent-store.js';
import { adoptionHeader, adoptionOptions, adoptionReceipt, adoptionReceiptColumns, adoptionRequest,
  adoptionSelection, adoptionSignal, adoptionSnapshot, adoptionStatus, adoptionStatusColumns, validateAdoptedStatus,
  type AdoptionRequest, type EventAcknowledgmentIntentAdoptRetryPolicyOptions,
  type EventAcknowledgmentIntentPolicyAdoptionReceipt } from './event-acknowledgment-intent-policy-adoption-state.js';
export { eventAcknowledgmentIntentPolicyAdoptionSchema } from './event-acknowledgment-intent-contract.js';
export type { EventAcknowledgmentIntentAdoptRetryPolicyOptions, EventAcknowledgmentIntentPolicyAdoptionReceipt } from './event-acknowledgment-intent-policy-adoption-state.js';

/** Rejections expose a receipt only after its outcome is known committed or replayed. */
export class EventAcknowledgmentIntentPolicyAdoptionError extends EventAcknowledgmentIntentError {
  constructor(code: 'event_acknowledgment_intent_conflict' | 'event_acknowledgment_intent_not_found',
    readonly receipt?: EventAcknowledgmentIntentPolicyAdoptionReceipt) {
    super(code); this.name = 'EventAcknowledgmentIntentPolicyAdoptionError';
  }
  override toJSON(): object { return { name: this.name, code: this.code, ...(this.receipt === undefined ? {} : { receipt: this.receipt }) }; }
  override [inspect.custom](): object { return this.toJSON(); }
}

/** Exact microsecond metadata for an explicit adoption decision. This acquires no
 * claim or row lock. Ordinary list/staging timestamps retain their diagnostic format. */
export async function getEventAcknowledgmentIntentPolicyAdoptionCandidate(pool: EventConsumerPool, accountId: string, appId: string,
  invocationId: string, replayGeneration: string, attempt: number, options: { readonly signal?: AbortSignal } = {}): Promise<EventAcknowledgmentIntentStatus> {
  const signal = adoptionSignal(adoptionOptions(options, ['signal']).signal), [account, app] = intentScope(accountId, appId);
  const selection = adoptionSelection(invocationId, replayGeneration, attempt);
  return intentCommandTransaction(pool, signal, async tx => {
    const row = (await tx.query(`SELECT ${adoptionStatusColumns} FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4::bigint AND attempt=$5`, [account, app, selection.invocation, selection.generation, selection.attempt])).rows[0];
    if (!row) throw new EventAcknowledgmentIntentPolicyAdoptionError('event_acknowledgment_intent_not_found');
    return { value: adoptionStatus(row), write: false };
  });
}

/** Bind once without changing callback/state/count/due time. Save the command UUID
 * and original inputs durably before calling. Exact retries resolve uncertain
 * commits via a historical receipt, even after settlement/supersession/pruning.
 * Guard rejections throw with their retained receipt; changed requests under a
 * retained ID throw conflict without one. No HTTP or recovery capability is added. */
export async function adoptEventAcknowledgmentIntentRetryPolicy(pool: EventConsumerPool, accountId: string, appId: string,
  invocationId: string, replayGeneration: string, attempt: number,
  options: EventAcknowledgmentIntentAdoptRetryPolicyOptions): Promise<EventAcknowledgmentIntentPolicyAdoptionReceipt> {
  const [account, app] = intentScope(accountId, appId), selection = adoptionSelection(invocationId, replayGeneration, attempt);
  if (BigInt(selection.generation) > BigInt(Number.MAX_SAFE_INTEGER)) throw new EventAcknowledgmentIntentError('event_acknowledgment_intent_unsupported_generation');
  const request = adoptionRequest(selection, options);
  const receipt = await intentCommandTransaction(pool, request.signal, async tx => {
    await tx.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))', ['gregale.event-ack-intent-policy-adoption.v1:' + account + ':' + app + ':' + request.commandId]);
    const row = (await tx.query(`SELECT ${adoptionReceiptColumns} FROM public.gregale_event_ack_intent_policy_adoptions WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, [account, app, request.commandId])).rows[0];
    if (row) {
      const saved = adoptionHeader(row);
      if (saved.commandId !== request.commandId || saved.invocationId !== request.invocation || saved.replayGeneration !== request.generation
        || saved.attempt !== request.attempt || !saved.digest.equals(request.digest)) throw new EventAcknowledgmentIntentPolicyAdoptionError('event_acknowledgment_intent_conflict');
      return { value: adoptionReceipt(saved, row.intent_status, request), write: false };
    }
    return { value: await applyAdoption(tx, account, app, request), write: true };
  });
  if (receipt.outcome !== 'adopted') throw new EventAcknowledgmentIntentPolicyAdoptionError(
    receipt.outcome === 'conflict' ? 'event_acknowledgment_intent_conflict' : 'event_acknowledgment_intent_not_found', receipt);
  return receipt;
}

async function applyAdoption(tx: EventConsumerTransaction, account: string, app: string, request: AdoptionRequest): Promise<EventAcknowledgmentIntentPolicyAdoptionReceipt> {
  const key = [account, app, request.invocation, request.generation, request.attempt];
  await tx.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))', ['gregale.event-ack-intent.v1:' + key.slice(0, 3).join(':')]);
  const existing = (await tx.query(`SELECT ${adoptionStatusColumns} FROM public.gregale_event_acknowledgment_intents WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4::bigint AND attempt=$5 FOR UPDATE`, key)).rows[0];
  let outcome: EventAcknowledgmentIntentPolicyAdoptionReceipt['outcome'] = 'not_found', intent: EventAcknowledgmentIntentStatus | undefined;
  if (existing) {
    adoptionStatus(existing); // Validate existing policy metadata before any binding.
    const row = (await tx.query(`UPDATE public.gregale_event_acknowledgment_intents SET retry_policy_version=1,retry_initial_backoff_ms=$8::bigint,retry_max_backoff_ms=$9::bigint,updated_at=greatest(clock_timestamp(),updated_at+interval '1 microsecond') WHERE account_id=$1::uuid AND app_id=$2::uuid AND invocation_id=$3::uuid AND replay_generation=$4::bigint AND attempt=$5 AND updated_at=$6::timestamptz AND delivery_attempts=$7::bigint AND state IN ('pending','unconfirmed') AND retry_policy_version IS NULL AND retry_initial_backoff_ms IS NULL AND retry_max_backoff_ms IS NULL AND retry_max_delivery_attempts IS NULL AND delivery_attempt_limit IS NULL AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) RETURNING ${adoptionStatusColumns}`, [...key, request.expectedUpdatedAt, request.attempts, request.initial, request.maximum])).rows[0];
    outcome = row ? 'adopted' : 'conflict';
    if (row) { intent = adoptionStatus(row); validateAdoptedStatus(intent, request); }
  }
  const body = intent === undefined ? null : adoptionSnapshot(intent);
  if (body !== null && Buffer.byteLength(body) > 8192) throw intentStoreError();
  const saved = (await tx.query(`INSERT INTO public.gregale_event_ack_intent_policy_adoptions(account_id,app_id,command_id,invocation_id,replay_generation,attempt,request_digest,outcome,intent_status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::bigint,$6,$7,$8,$9) RETURNING ${adoptionReceiptColumns}`, [account, app, request.commandId, request.invocation, request.generation, request.attempt, request.digest, outcome, body])).rows[0];
  if (!saved) throw intentStoreError();
  // Decode the exact retained representation before COMMIT, just as on replay.
  return adoptionReceipt(adoptionHeader(saved), saved.intent_status, request);
}
