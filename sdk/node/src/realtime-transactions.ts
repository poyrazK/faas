import { randomUUID } from 'node:crypto';
import type { CommitTransaction } from './commit.js';

export interface RealtimeCommitEvent {
  id?: string;
  endpointId: string;
  channel: string;
  /** JSON encoded as a text realtime frame; at most 4096 UTF-8 bytes. */
  data: unknown;
  metadata?: Record<string, string>;
  /** Omit to inherit the transaction default; 0 disables expiry. */
  ttlSeconds?: number;
}

/** Queue only. Use the same client as your business write inside BEGIN/COMMIT. */
export async function insertRealtimeCommitEvent(tx: CommitTransaction, event: RealtimeCommitEvent): Promise<string> {
  const id = event.id ?? randomUUID();
  validateId(id);
  if (!/^[0-9a-f]{32}$/.test(event.endpointId) || typeof event.channel !== 'string'
      || event.channel.length === 0 || Buffer.byteLength(event.channel) > 256
      || /[\x00\r\n/?#]/.test(event.channel) || event.channel.trim() !== event.channel) {
    throw new TypeError('Invalid realtime endpoint or channel');
  }
  validateMetadata(event.metadata ?? {});
  const json = JSON.stringify(event.data, (_key, value: unknown) => {
    if (value === undefined || typeof value === 'function' || typeof value === 'symbol'
        || typeof value === 'bigint' || (typeof value === 'number' && !Number.isFinite(value))) {
      throw new TypeError('Realtime commit data must contain JSON values');
    }
    return value;
  });
  if (json === undefined || Buffer.byteLength(json) > 4096) throw new TypeError('Realtime commit data exceeds 4096 bytes');
  if (event.ttlSeconds !== undefined) validateTTL(event.ttlSeconds);
  if (event.ttlSeconds === undefined) {
    await tx.query('INSERT INTO public.gregale_realtime_outbox(event_id,endpoint_id,channel,data,metadata) VALUES($1::uuid,$2,$3,$4,$5::jsonb)',
      [id, event.endpointId, event.channel, Buffer.from(json), JSON.stringify(event.metadata ?? {})]);
  } else {
    await tx.query('INSERT INTO public.gregale_realtime_outbox(event_id,endpoint_id,channel,data,metadata,ttl_seconds) VALUES($1::uuid,$2,$3,$4,$5::jsonb,$6)',
      [id, event.endpointId, event.channel, Buffer.from(json), JSON.stringify(event.metadata ?? {}), event.ttlSeconds]);
  }
  return id.toLowerCase();
}

export interface RealtimeCommitDelivery {
  id: string;
  endpointId: string;
  channel: string;
  status: 'pending' | 'publishing' | 'blocked' | 'published';
  attempts: number;
  blockedCode: string | null;
  /** Decimal string: PostgreSQL sequences can exceed JavaScript's safe integer range. */
  sequence: string | null;
  createdAt: string;
  publishedAt: string | null;
}

export interface RealtimeOutboxReader {
  query(sql: string, values: unknown[]): Promise<{ rows: RealtimeCommitDelivery[] }>;
}

const deliveryColumns = `event_id::text AS id,endpoint_id AS "endpointId",channel,
    CASE WHEN published_at IS NOT NULL THEN 'published' WHEN blocked_code IS NOT NULL THEN 'blocked'
      WHEN lease_until>clock_timestamp() THEN 'publishing' ELSE 'pending' END AS status,
    attempts,blocked_code AS "blockedCode",sequence::text,created_at::text AS "createdAt",published_at::text AS "publishedAt"`;

/** Read metadata from the application's database; payloads are omitted. */
export async function listRealtimeCommitDeliveries(db: RealtimeOutboxReader, limit = 32): Promise<RealtimeCommitDelivery[]> {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 128) throw new TypeError('Limit must be 1-128');
  const result = await db.query(`SELECT ${deliveryColumns}
    FROM public.gregale_realtime_outbox ORDER BY created_at DESC,event_id DESC LIMIT $1`, [limit]);
  return result.rows;
}

/** Missing or cleaned-up rows return null; absence is not proof of publication. */
export async function getRealtimeCommitDelivery(db: RealtimeOutboxReader, id: string): Promise<RealtimeCommitDelivery | null> {
  validateId(id);
  const result = await db.query(`SELECT ${deliveryColumns} FROM public.gregale_realtime_outbox WHERE event_id=$1::uuid`, [id]);
  return result.rows[0] ?? null;
}

/** Retry a repaired blocked event without resetting its identity or attempts. */
export async function retryRealtimeCommitEvent(tx: CommitTransaction, id: string): Promise<void> {
  validateId(id);
  await tx.query(`UPDATE public.gregale_realtime_outbox SET blocked_code=NULL,next_attempt_at=clock_timestamp()
    WHERE event_id=$1::uuid AND published_at IS NULL AND blocked_code IS NOT NULL
    AND (lease_until IS NULL OR lease_until<=clock_timestamp())`, [id]);
}

function validateId(id: string): void {
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id)
      || id === '00000000-0000-0000-0000-000000000000') throw new TypeError('Realtime event ID must be a nonzero UUID');
}

function validateMetadata(metadata: Record<string,string>): void {
  if (metadata === null || typeof metadata !== 'object' || Array.isArray(metadata) || Object.keys(metadata).length > 8) throw new TypeError('Invalid realtime metadata');
  for (const [key,value] of Object.entries(metadata)) {
    if (!/^[a-z0-9_.-]{1,64}$/.test(key) || typeof value !== 'string' || Buffer.byteLength(value) > 128 || /[\x00\r\n]/.test(value)) throw new TypeError('Invalid realtime metadata');
  }
}

/** Set the default for subsequent events on this connection inside an active transaction.
 * Call after BEGIN; the setting clears on COMMIT/ROLLBACK. Requires the TTL outbox upgrade.
 */
export async function setRealtimeCommitTTL(tx: CommitTransaction, ttlSeconds: number): Promise<void> {
  validateTTL(ttlSeconds);
  await tx.query("SELECT set_config('gregale.realtime_ttl_seconds',$1,true)", [String(ttlSeconds)]);
}

function validateTTL(ttlSeconds: number): void {
  if (!Number.isSafeInteger(ttlSeconds) || ttlSeconds < 0 || ttlSeconds > 86400) {
    throw new TypeError('Realtime TTL must be an integer from 0 to 86400');
  }
}
