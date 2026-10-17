/** Read-only evidence for one original consumer request. This never grants a processing capability. */
import { timestamp } from './event-deadline-policy.js';
import { processingStoreError } from './event-consumer-processing.js';
import { eventConsumerDigests, type EventConsumerPool, type EventConsumerRequest,
  type EventConsumerTransactionOptions } from './event-consumer-transactions.js';

export type EventConsumerProcessingInspectionState = 'completed' | 'active' | 'expired' | 'absent' | 'conflict';
export interface EventConsumerProcessingInspection {
  /** Conflict takes precedence, then matching receipt, then matching claim, then absence. */
  readonly state: EventConsumerProcessingInspectionState;
  readonly observedAt: string;
  /** Matching identity/input metadata only. The saved result is not read or validated. */
  readonly receipt: 'absent' | 'matching' | 'conflict';
  readonly claim: 'absent' | 'active' | 'expired' | 'conflict';
  /** Present only for a matching claim, even if a receipt is also present. */
  readonly expiresAt?: string;
  /** Nonnegative whole milliseconds, rounded down from the database observation; expired claims return zero. */
  readonly remainingLeaseMs?: number;
}
// One statement, MVCC snapshot and materialized database clock. Only equality
// evidence and bounded timestamps leave PostgreSQL; no token/result/body is selected.
const inspectionQuery = `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS observed_at),
 receipt AS MATERIALIZED (
  SELECT true AS present,account_id=$3::uuid AND app_id=$4::uuid AND subscription_id=$5::uuid
   AND acceptance_id=$6::bigint AND event_source=$7 AND event_id=$8 AND request_digest=$2 AS matching
  FROM public.gregale_event_consumer_inbox WHERE identity_digest=$1
 ), claim AS MATERIALIZED (
  SELECT true AS present,request_digest=$2 AS matching,expires_at
  FROM public.gregale_event_consumer_processing WHERE identity_digest=$1
 ) SELECT
 CASE WHEN c.observed_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz
  THEN to_char(c.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS observed_at,
 coalesce(r.present,false) AS receipt_present,r.matching AS receipt_matching,
 coalesce(p.present,false) AS claim_present,p.matching AS claim_matching,
 CASE WHEN p.matching AND p.expires_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz
  THEN to_char(p.expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS expires_at
 FROM clock c LEFT JOIN receipt r ON true LEFT JOIN claim p ON true`;

async function storage<T>(operation: () => Promise<T>): Promise<T> {
  try { return await operation(); } catch { throw processingStoreError(); }
}
function instant(raw: unknown): { text: string; at: bigint } {
  if (typeof raw !== 'string' || !/^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{6}Z$/.test(raw)
    || raw.startsWith('0000-')) throw processingStoreError();
  const at = timestamp(raw);
  if (at === undefined) throw processingStoreError();
  return { text: raw, at };
}
function evidence(present: unknown, matching: unknown): 'absent' | 'matching' | 'conflict' {
  if (typeof present !== 'boolean' || present && typeof matching !== 'boolean'
    || !present && matching !== null) throw processingStoreError();
  return present ? matching ? 'matching' : 'conflict' : 'absent';
}
function observation(row: Record<string, unknown>): EventConsumerProcessingInspection {
  const observed = instant(row.observed_at);
  const receipt = evidence(row.receipt_present, row.receipt_matching);
  const claimEvidence = evidence(row.claim_present, row.claim_matching);
  let claim: EventConsumerProcessingInspection['claim'];
  let lease: { expiresAt: string; remainingLeaseMs: number } | undefined;
  if (claimEvidence === 'matching') {
    const expires = instant(row.expires_at), remaining = expires.at - observed.at;
    claim = remaining > 0n ? 'active' : 'expired';
    lease = { expiresAt: expires.text, remainingLeaseMs: remaining > 0n ? Number(remaining / 1_000_000n) : 0 };
  } else {
    if (row.expires_at !== null) throw processingStoreError();
    claim = claimEvidence;
  }
  const state = receipt === 'conflict' || claim === 'conflict' ? 'conflict'
    : receipt === 'matching' ? 'completed' : claim;
  return Object.freeze({ state, observedAt: observed.text, receipt, claim, ...lease });
}
/** Authenticate/authorize first and construct the request with exact original bytes.
 * Uses no advisory/row lock, callbacks, lease mutations or receipt/result replay.
 * Cancellation is cooperative; bound acquisition/query/statement timeouts in the driver. */
export async function inspectEventConsumerProcessing(pool: EventConsumerPool, input: EventConsumerRequest,
  options: EventConsumerTransactionOptions = {}): Promise<EventConsumerProcessingInspection> {
  if (!options || typeof options !== 'object' || Array.isArray(options)
    || options.signal !== undefined && !(options.signal instanceof AbortSignal)) throw new TypeError('invalid processing inspection options');
  const signal = options.signal; signal?.throwIfAborted();
  const { identity, request } = eventConsumerDigests(input);
  if (!pool || typeof pool.connect !== 'function') throw new TypeError('invalid event consumer pool');
  const connection = await storage(() => pool.connect());
  let discard = false;
  try {
    signal?.throwIfAborted();
    const result = await storage(() => connection.query(inspectionQuery, [identity, request, input.accountId, input.appId,
      input.subscriptionId, input.acceptanceId, input.source, input.eventId])).catch(error => {
      discard = true; throw error;
    });
    signal?.throwIfAborted();
    if (result.rows.length !== 1) throw processingStoreError();
    return observation(result.rows[0]!);
  } finally {
    try { connection.release(discard); } catch { throw processingStoreError(); }
  }
}
