/** Typed producers and caller-owned, bounded workers over the Go outbox protocol. */
import { randomBytes } from 'node:crypto';
import { EventContract } from './event-contract.js';
import { jobSnapshot } from './job-runtime.js';
import type { PipelineScope } from './pipeline-state.js';
import type { OperationTransaction } from './operation-receipt.js';
import { eventOutboxSchema, outboxSQL as SQL } from './event-outbox-protocol.js';
import { eventOutboxTransactionQuery, failEventOutboxTransaction, withEventOutboxTransaction, type EventOutboxPool, type EventOutboxTransaction } from './event-outbox-transaction.js';
import { eventOutboxOperation, eventPublicationTransport, eventPublicationOutcome, type EventOutboxHTTPResult } from './event-publication.js';
import { EventOutboxError, OUTBOX_DAY_MS, eventOutboxRetryDelay, outboxHash, outboxInteger, outboxScope, outboxKey, outboxSlug, outboxInstant, outboxPolicy, outboxPolicyDigest, outboxReceipt, parseEventOutboxStatus, parseOutboxRequest, prepareEventPublication,
  type EventOutboxOptions, type EventPublicationOptions, type EventPublicationOutcome, type EventOutboxStatus, type EventOutboxOutcome, type EventOutboxRetryPolicy, type ResolvedEventOutboxPolicy } from './event-outbox-state.js';
export * from './event-outbox-state.js';
export { eventOutboxSchema, withEventOutboxTransaction };
export type { EventOutboxPool, EventOutboxTransaction };

export interface EventPublisherDefinition { scope: PipelineScope; token?: string; fetch?: typeof fetch; receiptRetentionMs?: number; }
export interface EventStageOptions extends EventPublicationOptions { retryWindowMs?: number; retryPolicy?: EventOutboxRetryPolicy; }
export interface TypedEventPublisher<E> {
  stage(tx: EventOutboxTransaction, input: E, options: EventStageOptions): Promise<EventOutboxStatus>;
  publish(input: E, options: EventPublicationOptions & EventOutboxOptions): Promise<EventPublicationOutcome>;
}
export function createEventPublisher<E>(contract: EventContract<E>, definition: EventPublisherDefinition): TypedEventPublisher<E> {
  const scope = outboxScope(definition.scope), retention = outboxInteger(definition.receiptRetentionMs ?? OUTBOX_DAY_MS, 1000, 14 * OUTBOX_DAY_MS);
  const token = definition.token, fetcher = definition.fetch;
  return Object.freeze({
    async stage(tx, input, options) {
      try {
        const query = eventOutboxTransactionQuery(tx);
        const body = prepareEventPublication(contract, scope, input, options);
        const window = outboxInteger(options.retryWindowMs ?? OUTBOX_DAY_MS, 1000, Math.min(7 * OUTBOX_DAY_MS, retention));
        const args = [scope.account_id, scope.app_id, options.key];
        const digest = Buffer.from(outboxHash(body), 'hex');
        await query(SQL.stage, [...args, scope.app_slug, digest, body, window]);
        const old = (await query(SQL.stage_check, args)).rows;
        if (old.length !== 1 || old[0]!.app_slug !== scope.app_slug || !byteHash(old[0]!.request_digest, digest)) throw new EventOutboxError('conflict');
        const rows = (await query(SQL.status, args)).rows;
        if (rows.length !== 1) throw new EventOutboxError('store_failed');
        const status = parseEventOutboxStatus(rows[0]);
        if (options.retryPolicy !== undefined) {
          const policy = outboxPolicy(options.retryPolicy), hash = Buffer.from(outboxPolicyDigest(policy), 'hex');
          await query(SQL.policy_stage, [...args, policy.initialBackoffMs, policy.maxBackoffMs, policy.maxAttempts, hash]);
          const saved = (await query(SQL.policy, args)).rows;
          if (saved.length !== 1) throw new EventOutboxError('conflict');
          storedPolicy(saved[0]!);
          if (!byteHash(saved[0]!.initial_digest, hash)) throw new EventOutboxError('conflict');
        }
        return status;
      } catch (error) {
        const stopped = error instanceof EventOutboxError ? error : new EventOutboxError('store_failed');
        failEventOutboxTransaction(tx, stopped); throw stopped;
      }
    },
    async publish(input, options) {
      const body = prepareEventPublication(contract, scope, input, options);
      if (!token) throw new EventOutboxError('invalid_request');
      return eventPublicationOutcome(await eventPublicationTransport(scope, token, fetcher)(scope.app_slug, body, false, options), scope, options.key);
    },
  } satisfies TypedEventPublisher<E>);
}
function byteHash(value: unknown, expected: Uint8Array): boolean { return value instanceof Uint8Array && Buffer.from(value).equals(Buffer.from(expected)); }
function storedPolicy(row: Record<string, unknown>): ResolvedEventOutboxPolicy {
  try {
    if (row.version !== 1 || typeof row.revision !== 'string' || !/^[1-9][0-9]{0,18}$/.test(row.revision) || BigInt(row.revision) > 9223372036854775807n || !(row.initial_digest instanceof Uint8Array) || row.initial_digest.length !== 32) throw new Error();
    return outboxPolicy({ initialBackoffMs: outboxInteger(row.initial_backoff_ms, 1, 3600000), maxBackoffMs: outboxInteger(row.max_backoff_ms, 1, 3600000), maxAttempts: outboxInteger(row.max_attempts, 0, 2147483647) });
  } catch { throw new EventOutboxError('store_failed'); }
}
type Query = OperationTransaction['query'];
async function using<T>(pool: EventOutboxPool, signal: AbortSignal, work: (query: Query) => Promise<T>): Promise<T> {
  let connection: Awaited<ReturnType<EventOutboxPool['connect']>> | undefined, released = false;
  const cancel = () => { if (connection && !released) { released = true; try { connection.release(true); } catch {} } };
  signal.addEventListener('abort', cancel, { once: true });
  try {
    signal.throwIfAborted(); connection = await pool.connect(); signal.throwIfAborted();
    return await work(async (sql, values = []) => {
      signal.throwIfAborted();
      try { const result = await connection!.query(sql, values); signal.throwIfAborted(); return result; }
      catch { signal.throwIfAborted(); throw new EventOutboxError('store_failed'); }
    });
  } catch (error) { signal.throwIfAborted(); if (error instanceof EventOutboxError) throw error; throw new EventOutboxError('store_failed'); }
  finally { signal.removeEventListener('abort', cancel); if (connection && !released) { released = true; try { connection.release(signal.aborted); } catch {} } }
}
export interface EventOutboxRecoveryOptions extends EventOutboxOptions {
  claimDurationMs?: number; requestTimeoutMs?: number; backoffMs?: number; retryPolicy?: EventOutboxRetryPolicy;
}
export interface EventOutboxBatchOptions extends EventOutboxRecoveryOptions {
  maxEvents: number; concurrency?: number; onProgress?: (outcome: EventOutboxOutcome) => void | Promise<void>;
}
export interface EventOutbox {
  status(key: string, options: EventOutboxOptions): Promise<EventOutboxStatus | null>;
  recoverNext(options: EventOutboxRecoveryOptions): Promise<EventOutboxOutcome>;
  recover(options: EventOutboxBatchOptions): Promise<readonly EventOutboxOutcome[]>;
  reconcile(key: string, options: EventOutboxOptions & { appSlug?: string }): Promise<{ outcome: 'published' | 'already_published' | 'unavailable' | 'unverified' | 'conflict'; publication: EventOutboxStatus }>;
}
export function createEventOutbox(pool: EventOutboxPool, definition: EventPublisherDefinition & { token: string }): EventOutbox {
  const scope = outboxScope(definition.scope), retention = outboxInteger(definition.receiptRetentionMs ?? OUTBOX_DAY_MS, 1000, 14 * OUTBOX_DAY_MS);
  const send = eventPublicationTransport(scope, definition.token, definition.fetch), account = [scope.account_id, scope.app_id];
  type Claim = { key: string; slug: string; body: string; token: Buffer; attempts: number; policy?: ResolvedEventOutboxPolicy };
  const readStatus = (rows: Record<string, unknown>[]) => { if (rows.length !== 1) throw new EventOutboxError('claim_lost'); return parseEventOutboxStatus(rows[0]); };
  const api: EventOutbox = {
    status(key, options) {
      if (!outboxKey(key)) throw new EventOutboxError('invalid_request');
      return eventOutboxOperation(options, signal => using(pool, signal, async query => {
        const rows = (await query(SQL.status, [...account, key])).rows;
        if (rows.length > 1) throw new EventOutboxError('store_failed'); return rows.length ? parseEventOutboxStatus(rows[0]) : null;
      }));
    },
    recoverNext(options) {
      const duration = outboxInteger(options.claimDurationMs ?? 30000, 1000, 300000);
      const requestTimeout = outboxInteger(options.requestTimeoutMs ?? 10000, 1, duration - 1);
      if (options.retryPolicy !== undefined && options.backoffMs !== undefined) throw new EventOutboxError('invalid_request');
      const defaultPolicy = options.retryPolicy === undefined ? undefined : outboxPolicy(options.retryPolicy);
      const backoff = outboxInteger(options.backoffMs ?? 5000, 1, 3600000);
      return eventOutboxOperation(options, async (signal, remaining) => {
        const selected = await withEventOutboxTransaction<Claim | EventOutboxOutcome>(pool, async tx => {
          const query = eventOutboxTransactionQuery(tx);
          await query(SQL.publishing_lock, ['gregale.event-outbox-publishing.v1:' + scope.account_id + ':' + scope.app_id]);
          const paused = (await query(SQL.paused, account)).rows;
          if (paused.length !== 1 || typeof paused[0]!.paused !== 'boolean') throw new EventOutboxError('store_failed');
          if (paused[0]!.paused) return { outcome: 'paused' };
          const rates = (await query(SQL.rate, account)).rows;
          if (rates.length > 1) throw new EventOutboxError('store_failed');
          let rate: { credit: number; interval: number; anchor: string } | undefined;
          if (rates.length) {
            try {
              const r = rates[0]!;
              if (r.version !== 1 || typeof r.enabled !== 'boolean' || r.future === true || typeof r.revision !== 'string' || !/^(0|[1-9][0-9]{0,18})$/.test(r.revision) || BigInt(r.revision) > 9223372036854775807n) throw new Error();
              const interval = outboxInteger(r.interval_ms, 1, 3600000), burst = outboxInteger(r.burst, 1, 10000);
              outboxInteger(r.credit_ms, 0, interval * burst); outboxInteger(r.elapsed_ms, 0, interval * burst);
              const credit = outboxInteger(r.credit, 0, interval * burst);
              outboxInstant(r.anchor);
              if (r.enabled && credit < interval) { outboxInstant(r.retry_at); return { outcome: 'rate_limited', retry_after_ms: outboxInteger(r.retry_after_ms, 1, 3600000), retry_at: String(r.retry_at) }; }
              if (r.enabled) rate = { credit, interval, anchor: String(r.anchor) };
            } catch { throw new EventOutboxError('store_failed'); }
          }
          const candidates = (await query(SQL.next, account)).rows;
          if (!candidates.length) return { outcome: 'empty' };
          if (candidates.length !== 1) throw new EventOutboxError('store_failed');
          const r = candidates[0]!, key = r.producer_key;
          if (!outboxKey(key) || !outboxSlug(r.app_slug) || typeof r.expired !== 'boolean' || typeof r.retry_window !== 'number' || !Number.isFinite(r.retry_window) || r.retry_window <= 0) throw new EventOutboxError('store_failed');
          if (r.retry_window * 1000 > retention + 0.001) throw new EventOutboxError('retention_mismatch');
          const attempts = outboxInteger(r.attempts, 0, 2147483647), args = [...account, key];
          const policies = (await query(SQL.policy, args)).rows;
          if (policies.length > 1) throw new EventOutboxError('store_failed');
          const policy = policies.length ? storedPolicy(policies[0]!) : defaultPolicy;
          let code = r.expired ? 'retry_window_expired' : policy?.maxAttempts && attempts >= policy.maxAttempts ? 'attempts_exhausted' : '';
          try { if (typeof r.request_body !== 'string' || !byteHash(r.request_digest, Buffer.from(outboxHash(r.request_body), 'hex'))) throw new Error(); parseOutboxRequest(r.request_body, scope.app_id, key); }
          catch { code ||= 'publication_rejected'; }
          if (code) return { outcome: 'blocked', status: readStatus((await query(SQL.block, [...args, code])).rows) };
          const token = randomBytes(32);
          const claimed = (await query(SQL.claim, [...args, token, duration])).rows;
          if (claimed.length !== 1) throw new EventOutboxError('claim_lost');
          if (rate) await query(SQL.rate_consume, [...account, rate.credit - rate.interval, rate.anchor]);
          return { key, slug: String(r.app_slug), body: String(r.request_body), token, attempts: outboxInteger(claimed[0]!.attempts, 1, 2147483647), ...(policy ? { policy } : {}) };
        }, { timeoutMs: remaining(), signal });
        if ('outcome' in selected) return jobSnapshot(selected);
        const c = selected, started = performance.now();
        const lease = await using(pool, signal, async query => (await query(SQL.lease, [...account, c.key, c.token])).rows);
        if (lease.length !== 1) return { outcome: 'claim_lost' };
        const seconds = lease[0]!.remaining;
        if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0 || seconds > 300) return { outcome: 'claim_lost' };
        const budget = Math.min(requestTimeout, remaining(), Math.floor(seconds * 1000 - (performance.now() - started)));
        if (budget < 1) return { outcome: 'claim_lost' };
        let response: EventOutboxHTTPResult;
        try { response = await send(c.slug, c.body, false, { timeoutMs: budget, signal }); }
        catch (error) { signal.throwIfAborted(); remaining(); if (!(error instanceof EventOutboxError) || error.code !== 'deadline') throw error; response = { status: 0, value: null, retryAfter: null }; }
        const publication = eventPublicationOutcome(response, scope, c.key);
        let state = publication.state, code = state === 'published' ? 'publication_confirmed' : state === 'blocked' ? 'publication_rejected' : 'unconfirmed';
        if (state === 'unconfirmed' && c.policy?.maxAttempts && c.attempts >= c.policy.maxAttempts) { state = 'blocked'; code = 'attempts_exhausted'; }
        const guidance = state === 'unconfirmed' && c.policy ? retryGuidance(response) : { milliseconds: 0, at: null };
        const delay = eventOutboxRetryDelay(c.token, c.attempts, c.policy, backoff);
        const rows = await using(pool, signal, async query => (await query(SQL.settle, [...account, c.key, c.token, state, code, publication.state === 'published' ? publication.receipt.accepted_at : null, delay, guidance.milliseconds, guidance.at])).rows);
        return rows.length ? jobSnapshot({ outcome: state, status: readStatus(rows) }) : { outcome: 'claim_lost' };
      });
    },
    recover(options) {
      const maximum = outboxInteger(options.maxEvents, 1, 10000), concurrency = outboxInteger(options.concurrency ?? 1, 1, 32);
      return eventOutboxOperation(options, async (signal, remaining) => {
        let started = 0; const outcomes: EventOutboxOutcome[] = [];
        await Promise.all(Array.from({ length: Math.min(concurrency, maximum) }, async () => {
          while (started < maximum) {
            signal.throwIfAborted(); started++;
            const result = await api.recoverNext({ ...options, timeoutMs: remaining(), signal }); signal.throwIfAborted(); outcomes.push(result);
            if (options.onProgress) { try { await options.onProgress(result); signal.throwIfAborted(); } catch { signal.throwIfAborted(); throw new EventOutboxError('progress_failed'); } }
            if (['empty', 'paused', 'rate_limited', 'claim_lost'].includes(result.outcome)) break;
          }
        }));
        return jobSnapshot(outcomes);
      });
    },
    reconcile(key, options) {
      if (!outboxKey(key) || options.appSlug !== undefined && !outboxSlug(options.appSlug)) throw new EventOutboxError('invalid_request');
      return eventOutboxOperation(options, async (signal, remaining) => using(pool, signal, async query => {
        const row = (await query(SQL.reconcile_read, [...account, key])).rows;
        if (row.length !== 1) throw new EventOutboxError('not_found');
        const raw = row[0]!, status = parseEventOutboxStatus(raw);
        if (status.state === 'published') return { outcome: 'already_published' as const, publication: status };
        if (raw.live !== false) throw new EventOutboxError('busy');
        if (typeof raw.request_body !== 'string' || !byteHash(raw.request_digest, Buffer.from(outboxHash(raw.request_body), 'hex'))) throw new EventOutboxError('conflict');
        parseOutboxRequest(raw.request_body, scope.app_id, key);
        const response = await send(options.appSlug ?? status.app_slug, raw.request_body, true, { timeoutMs: Math.min(30000, remaining()), signal }); signal.throwIfAborted();
        const v = response.value as Record<string, any>;
        const unverified = { outcome: 'unverified' as const, publication: status };
        if (response.status < 200 || response.status >= 300 || !v || v.app_id !== scope.app_id || v.source !== 'app.' + scope.app_id || v.event_id !== 'key.' + outboxHash(key)) return unverified;
        try { outboxInstant(v.observed_at); } catch { return unverified; }
        if (v.status === 'unavailable') return { outcome: 'unavailable' as const, publication: status };
        if (v.status === 'conflict') return { outcome: 'conflict' as const, publication: status };
        try {
          const receipt = outboxReceipt({ ...v, duplicate: false }, scope, key), accepted = outboxInstant(receipt.accepted_at);
          if (v.status !== 'match' || accepted < outboxInstant(status.created_at) || accepted > outboxInstant(status.retry_until) || accepted > outboxInstant(v.observed_at)) return unverified;
          const rows = (await query(SQL.reconcile_settle, [...account, key, status.state, status.updated_at, status.attempts, raw.request_digest, status.app_slug, receipt.accepted_at, status.claim_expires_at])).rows;
          if (!rows.length) throw new EventOutboxError('conflict');
          return { outcome: 'published' as const, publication: readStatus(rows) };
        } catch (error) { if (error instanceof EventOutboxError && ['conflict', 'store_failed', 'claim_lost'].includes(error.code)) throw error; return unverified; }
      }));
    },
  };
  return Object.freeze(api);
}

function retryGuidance(response: EventOutboxHTTPResult): { milliseconds: number; at: string | null } {
  if (![429, 503].includes(response.status)) return { milliseconds: 0, at: null };
  const raw = response.retryAfter?.trim();
  if (raw && raw.length <= 256) {
    if (/^[0-9]+$/.test(raw)) return { milliseconds: Number(BigInt(raw) > 604800n ? 604800n : BigInt(raw)) * 1000, at: null };
    const time = Date.parse(raw);
    if (Number.isFinite(time) && new Date(time).getUTCFullYear() >= 1 && new Date(time).getUTCFullYear() <= 9999) return { milliseconds: 0, at: new Date(time).toISOString() };
  }
  const seconds = (response.value as { retry_after_seconds?: unknown } | null)?.retry_after_seconds;
  return { milliseconds: typeof seconds === 'number' && Number.isSafeInteger(seconds) && seconds >= 0 ? Math.min(seconds, 604800) * 1000 : 0, at: null };
}
