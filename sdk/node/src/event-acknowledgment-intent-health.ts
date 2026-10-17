// Read-only application-owned recovery diagnostics; keep classification aligned with the Go SDK.
import type { EventConsumerPool } from './event-consumer-transactions.js';
import { operationSignal, type EventAcknowledgmentWorkerOperationOptions } from './event-acknowledgment-protocol.js';
import { intentInstant, intentPool, intentScope, intentStoreError } from './event-acknowledgment-intent-store.js';

export interface EventAcknowledgmentIntentHealthOptions extends EventAcknowledgmentWorkerOperationOptions {
  /** Integer 1..10000; default 1000. Counts are sampled when more unfinished rows exist. */
  scanLimit?: number;
  /** Whole milliseconds, 1000..86400000; default 300000. Excludes live claims and future retries. */
  stallAfterMs?: number;
}
/** Retained local callback recovery only. Counts are lower bounds when complete is false.
 * No platform/claim tokens, event bodies, results or invocation identities are included. */
export interface EventAcknowledgmentIntentHealth {
  readonly accountId: string;
  readonly appId: string;
  readonly observedAt: string;
  readonly coverage: 'bounded_retained_unfinished_intents';
  readonly complete: boolean;
  readonly truncated: boolean;
  readonly scanLimit: number;
  readonly invalidRows: number;
  readonly outstanding: number;
  readonly pending: number;
  readonly unconfirmed: number;
  readonly due: number;
  readonly leased: number;
  readonly scheduled: number;
  readonly expiredClaims: number;
  readonly stalled: number;
  readonly stallAfterSeconds: number;
  readonly oldestOutstandingAt?: string;
  readonly oldestOutstandingAgeSeconds: number;
  readonly oldestEligibleAt?: string;
  readonly oldestOverdueAgeSeconds: number;
}
// A single statement/snapshot/clock, selecting metadata only. The extra row detects
// truncation; aggregate counts are bounded to scanLimit and fit PostgreSQL int4.
const healthQuery = `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS observed_at), sample AS MATERIALIZED (
 SELECT state,next_attempt_at,claim_expires_at,created_at,updated_at,invocation_id,replay_generation,attempt
 FROM public.gregale_event_acknowledgment_intents
 WHERE account_id=$1::uuid AND app_id=$2::uuid AND state IN ('pending','unconfirmed')
 ORDER BY next_attempt_at,invocation_id,replay_generation,attempt LIMIT $3::integer+1
 ), classified AS (
 SELECT *,greatest(created_at,next_attempt_at,coalesce(claim_expires_at,next_attempt_at)) AS eligible_at
 FROM sample ORDER BY next_attempt_at,invocation_id,replay_generation,attempt LIMIT $3::integer
 ) SELECT c.observed_at,(SELECT count(*) FROM sample)>$3::integer AS truncated,
 count(s.state)::integer AS outstanding,
 count(*) FILTER(WHERE s.state='pending')::integer AS pending,
 count(*) FILTER(WHERE s.state='unconfirmed')::integer AS unconfirmed,
 count(*) FILTER(WHERE s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::integer AS due,
 count(*) FILTER(WHERE s.claim_expires_at>c.observed_at)::integer AS leased,
 count(*) FILTER(WHERE s.eligible_at>c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::integer AS scheduled,
 count(*) FILTER(WHERE s.claim_expires_at<=c.observed_at)::integer AS expired_claims,
 count(*) FILTER(WHERE s.eligible_at<=c.observed_at-$4::double precision*interval '1 second' AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::integer AS stalled,
 count(*) FILTER(WHERE s.created_at>c.observed_at OR s.updated_at>c.observed_at OR s.updated_at<s.created_at OR NOT isfinite(s.created_at) OR NOT isfinite(s.updated_at) OR NOT isfinite(s.next_attempt_at) OR (s.claim_expires_at IS NOT NULL AND NOT isfinite(s.claim_expires_at)))::integer AS invalid_rows,
 min(s.created_at) FILTER(WHERE isfinite(s.created_at))::timestamptz AS oldest_outstanding_at,
 coalesce(greatest(0,extract(epoch FROM c.observed_at-min(s.created_at) FILTER(WHERE isfinite(s.created_at)))),0)::double precision AS oldest_outstanding_age_seconds,
 min(s.eligible_at) FILTER(WHERE isfinite(s.eligible_at) AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::timestamptz AS oldest_eligible_at,
 coalesce(greatest(0,extract(epoch FROM c.observed_at-min(s.eligible_at) FILTER(WHERE isfinite(s.eligible_at) AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at)))),0)::double precision AS oldest_overdue_age_seconds
 FROM clock c LEFT JOIN classified s ON true GROUP BY c.observed_at`;

function count(value: unknown, limit: number): number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 0 || value > limit) throw intentStoreError();
  return value;
}
function age(value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) throw intentStoreError();
  return value;
}
function summary(row: Record<string, unknown> | undefined, accountId: string, appId: string,
  scanLimit: number, stallAfterMs: number): EventAcknowledgmentIntentHealth {
  if (!row || typeof row.truncated !== 'boolean') throw intentStoreError();
  const outstanding = count(row.outstanding, scanLimit), pending = count(row.pending, scanLimit);
  const unconfirmed = count(row.unconfirmed, scanLimit), due = count(row.due, scanLimit);
  const leased = count(row.leased, scanLimit), scheduled = count(row.scheduled, scanLimit);
  const expiredClaims = count(row.expired_claims, scanLimit), stalled = count(row.stalled, scanLimit);
  const invalidRows = count(row.invalid_rows, scanLimit);
  if (pending + unconfirmed !== outstanding || due + leased + scheduled !== outstanding
    || expiredClaims > due + scheduled || stalled > due || invalidRows > outstanding
    || row.truncated && outstanding !== scanLimit) throw intentStoreError();
  const oldestOutstandingAt = row.oldest_outstanding_at === null ? undefined : intentInstant(row.oldest_outstanding_at);
  const oldestEligibleAt = row.oldest_eligible_at === null ? undefined : intentInstant(row.oldest_eligible_at);
  const oldestOutstandingAgeSeconds = age(row.oldest_outstanding_age_seconds), oldestOverdueAgeSeconds = age(row.oldest_overdue_age_seconds);
  const observedAt = intentInstant(row.observed_at), observed = Date.parse(observedAt);
  if (outstanding === 0 && (oldestOutstandingAt !== undefined || oldestOutstandingAgeSeconds !== 0)
    || due === 0 && (oldestEligibleAt !== undefined || oldestOverdueAgeSeconds !== 0)
    || oldestOutstandingAt === undefined && oldestOutstandingAgeSeconds !== 0
    || oldestEligibleAt === undefined && oldestOverdueAgeSeconds !== 0
    || oldestEligibleAt !== undefined && Date.parse(oldestEligibleAt) > observed
    || invalidRows === 0 && (outstanding > 0 && oldestOutstandingAt === undefined || due > 0 && oldestEligibleAt === undefined
      || oldestOutstandingAt !== undefined && Date.parse(oldestOutstandingAt) > observed
      || oldestOverdueAgeSeconds > oldestOutstandingAgeSeconds)) throw intentStoreError();
  return Object.freeze({ accountId, appId, observedAt, coverage: 'bounded_retained_unfinished_intents',
    complete: !row.truncated && invalidRows === 0, truncated: row.truncated, scanLimit, invalidRows,
    outstanding, pending, unconfirmed, due, leased, scheduled, expiredClaims, stalled, stallAfterSeconds: stallAfterMs / 1000,
    ...(oldestOutstandingAt === undefined ? {} : { oldestOutstandingAt }), oldestOutstandingAgeSeconds,
    ...(oldestEligibleAt === undefined ? {} : { oldestEligibleAt }), oldestOverdueAgeSeconds });
}
/** One read-only metadata query. Claims, callbacks and business work are untouched.
 * Cancellation is cooperative; configure pool acquisition/query/statement timeouts. */
export async function getEventAcknowledgmentIntentHealth(pool: EventConsumerPool, accountId: string, appId: string,
  options: EventAcknowledgmentIntentHealthOptions = {}): Promise<EventAcknowledgmentIntentHealth> {
  const signal = operationSignal(options), [account, app] = intentScope(accountId, appId);
  intentPool(pool);
  const { scanLimit, stallAfterMs } = intentHealthOptions(options);
  const connection = await pool.connect().catch(() => { throw intentStoreError(); });
  let discard = false;
  try {
    signal?.throwIfAborted();
    const result = await connection.query(healthQuery, [account, app, scanLimit, stallAfterMs / 1000]).catch(() => {
      discard = true; throw intentStoreError();
    });
    signal?.throwIfAborted();
    if (result.rows.length !== 1) throw intentStoreError();
    return summary(result.rows[0], account, app, scanLimit, stallAfterMs);
  } finally { connection.release(discard); }
}
/** Internal normalized bounds shared with the watch; excluded from package exports. */
export function intentHealthOptions(options: Pick<EventAcknowledgmentIntentHealthOptions, 'scanLimit' | 'stallAfterMs'>):
  Readonly<{ scanLimit: number; stallAfterMs: number }> {
  if (!options || typeof options !== 'object' || Array.isArray(options)) throw new TypeError('invalid acknowledgment intent health options');
  const scanLimit = options.scanLimit === undefined ? 1000 : options.scanLimit;
  const stallAfterMs = options.stallAfterMs === undefined ? 300000 : options.stallAfterMs;
  if (!Number.isInteger(scanLimit) || scanLimit < 1 || scanLimit > 10000
    || !Number.isInteger(stallAfterMs) || stallAfterMs < 1000 || stallAfterMs > 86400000) throw new TypeError('invalid acknowledgment intent health options');
  return Object.freeze({ scanLimit, stallAfterMs });
}
