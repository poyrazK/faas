/** Portable application outbox metadata; payloads and claim capabilities stay private. */
import { createHash } from 'node:crypto';
import { EventContract } from './event-contract.js';
import { jobSnapshot } from './job-runtime.js';
import { eventPipelineScope } from './event-pipeline-state.js';
import type { PipelineScope } from './pipeline-state.js';

export type EventOutboxCode = 'invalid_request' | 'invalid_transaction' | 'scope_mismatch' | 'store_failed' | 'commit_unknown' | 'conflict' | 'claim_lost' | 'retention_mismatch' | 'not_found' | 'busy' | 'unverified' | 'deadline' | 'cancelled' | 'progress_failed';
export class EventOutboxError extends Error {
  constructor(readonly code: EventOutboxCode) { super('SDK event outbox stopped: ' + code); this.name = 'EventOutboxError'; }
}
export interface EventOutboxOptions { timeoutMs: number; signal?: AbortSignal; }
export interface EventPublicationOptions { key: string; time?: string; schemaVersion?: string; environment?: string; }
export interface EventOutboxRetryPolicy { initialBackoffMs?: number; maxBackoffMs?: number; maxAttempts?: number; }
export interface ResolvedEventOutboxPolicy { initialBackoffMs: number; maxBackoffMs: number; maxAttempts: number; }
export interface EventOutboxStatus {
  readonly producer_key: string; readonly app_slug: string;
  readonly state: 'pending' | 'unconfirmed' | 'published' | 'blocked'; readonly attempts: number;
  readonly next_attempt_at: string; readonly retry_until: string; readonly claim_expires_at: string | null;
  readonly accepted_at: string | null; readonly last_code: string; readonly created_at: string; readonly updated_at: string;
}
export interface EventPublicationReceipt {
  readonly app_id: string; readonly source: string; readonly event_id: string; readonly account_id: string;
  readonly accepted_at: string; readonly duplicate: boolean;
}
export type EventPublicationOutcome = { readonly state: 'published'; readonly receipt: EventPublicationReceipt }
  | { readonly state: 'unconfirmed' | 'blocked'; readonly code: 'unconfirmed' | 'publication_rejected'; readonly httpStatus?: number };
export interface EventOutboxOutcome {
  readonly outcome: 'published' | 'unconfirmed' | 'blocked' | 'empty' | 'paused' | 'rate_limited' | 'claim_lost';
  readonly status?: EventOutboxStatus; readonly retry_after_ms?: number; readonly retry_at?: string;
}
export const OUTBOX_DAY_MS = 86400000;
export const outboxHash = (value: string | Uint8Array): string => createHash('sha256').update(value).digest('hex');
export const outboxKey = (v: unknown): v is string => typeof v === 'string' && /^[!-~]{1,256}$/.test(v);
export const outboxSlug = (v: unknown): v is string => typeof v === 'string' && v.length > 0 && Buffer.byteLength(v) <= 256 && v.trim() === v && !/[/\\?#\0]/.test(v);
export function outboxScope(value: PipelineScope): PipelineScope {
  try { return eventPipelineScope(value); } catch { throw new EventOutboxError('scope_mismatch'); }
}
export function outboxInteger(value: unknown, low: number, high: number): number {
  if (typeof value === 'string' && /^(0|[1-9][0-9]*)$/.test(value)) value = Number(value);
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < low || value > high) throw new EventOutboxError('invalid_request');
  return value;
}
export function outboxPolicy(value: EventOutboxRetryPolicy): ResolvedEventOutboxPolicy {
  const initialBackoffMs = outboxInteger(value.initialBackoffMs ?? 5000, 1, 3600000);
  return { initialBackoffMs, maxBackoffMs: outboxInteger(value.maxBackoffMs ?? 300000, initialBackoffMs, 3600000), maxAttempts: outboxInteger(value.maxAttempts ?? 0, 0, 2147483647) };
}
export function outboxPolicyDigest(p: ResolvedEventOutboxPolicy): string {
  return outboxHash('gregale-event-outbox-policy-v1\n' + JSON.stringify([1, p.initialBackoffMs, p.maxBackoffMs, p.maxAttempts]));
}
/** @internal Go v1 domain-separated full jitter; claim capabilities never escape. */
export function eventOutboxRetryDelay(token: Uint8Array, attempts: number, policy: ResolvedEventOutboxPolicy | undefined, backoff: number): number {
  if (!policy) return backoff;
  const cap = Math.min(policy.maxBackoffMs, policy.initialBackoffMs * 2 ** Math.min(Math.max(attempts - 1, 0), 32));
  const entropy = outboxHash(Buffer.concat([Buffer.from('gregale-event-outbox-jitter-v1\n'), token]));
  return Number(BigInt('0x' + entropy.slice(0, 16)) % BigInt(cap)) + 1;
}
/** Exact nanoseconds for comparisons; never round CAS timestamps through Date. */
export function outboxInstant(value: unknown): bigint {
  if (typeof value !== 'string') throw new EventOutboxError('invalid_request');
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!m) throw new EventOutboxError('invalid_request');
  const [year, month, day, hour, minute, second] = m.slice(1, 7).map(Number) as [number, number, number, number, number, number];
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (year < 1 || month < 1 || month > 12 || day < 1 || day > days[month - 1]! || hour > 23 || minute > 59 || second > 59) throw new EventOutboxError('invalid_request');
  const ms = Date.parse(value.replace(/\.\d+/, ''));
  if (!Number.isFinite(ms)) throw new EventOutboxError('invalid_request');
  return BigInt(ms) * 1000000n + BigInt((m[7] ?? '').padEnd(9, '0'));
}
export function parseEventOutboxStatus(value: unknown): EventOutboxStatus {
  try {
    const r = value as EventOutboxStatus;
    if (!r || !outboxKey(r.producer_key) || !outboxSlug(r.app_slug) || !['pending', 'unconfirmed', 'published', 'blocked'].includes(r.state)
      || !['', 'unconfirmed', 'publication_confirmed', 'publication_rejected', 'retry_window_expired', 'attempts_exhausted'].includes(r.last_code)) throw new Error();
    const attempts = outboxInteger(r.attempts, 0, 2147483647);
    for (const time of [r.next_attempt_at, r.retry_until, r.created_at, r.updated_at]) outboxInstant(time);
    if (r.claim_expires_at !== null) outboxInstant(r.claim_expires_at);
    if (r.accepted_at !== null) outboxInstant(r.accepted_at);
    if ((r.state === 'published') !== (r.accepted_at !== null) || outboxInstant(r.retry_until) <= outboxInstant(r.created_at)) throw new Error();
    return jobSnapshot({ producer_key: r.producer_key, app_slug: r.app_slug, state: r.state, attempts,
      next_attempt_at: r.next_attempt_at, retry_until: r.retry_until, claim_expires_at: r.claim_expires_at,
      accepted_at: r.accepted_at, last_code: r.last_code, created_at: r.created_at, updated_at: r.updated_at });
  } catch { throw new EventOutboxError('store_failed'); }
}
export interface OutboxRequest { expected_app_id: string; key: string; type: string; data: unknown; time?: string; schemaversion?: string; environment?: string; }
export function parseOutboxRequest(body: string, appId: string, key?: string): OutboxRequest {
  try {
    if (!body || Buffer.byteLength(body) > 1048576 || Buffer.from(body).toString('utf8') !== body) throw new Error();
    const r = JSON.parse(body) as OutboxRequest;
    if (!r || typeof r !== 'object' || Array.isArray(r) || Object.keys(r).some(k => !['expected_app_id', 'key', 'type', 'data', 'time', 'schemaversion', 'environment'].includes(k))
      || r.expected_app_id !== appId || !outboxKey(r.key) || key !== undefined && r.key !== key || typeof r.type !== 'string' || !r.type.trim() || Buffer.byteLength(r.type) > 256 || !Object.hasOwn(r, 'data')) throw new Error();
    if (r.time !== undefined) outboxInstant(r.time);
    if (r.schemaversion !== undefined && (typeof r.schemaversion !== 'string' || Buffer.byteLength(r.schemaversion) > 64)) throw new Error();
    if (r.environment !== undefined && (typeof r.environment !== 'string' || Buffer.byteLength(r.environment) > 40 || r.environment.trim() !== r.environment || /[\r\n\0]/.test(r.environment))) throw new Error();
    return r;
  } catch { throw new EventOutboxError('invalid_request'); }
}
export function prepareEventPublication<E>(contract: EventContract<E>, scope: PipelineScope, data: E, options: EventPublicationOptions): string {
  try {
    if (options.schemaVersion !== undefined && typeof options.schemaVersion !== 'string' || options.environment !== undefined && typeof options.environment !== 'string') throw new Error();
    const body = JSON.stringify({ expected_app_id: scope.app_id, key: options.key, type: contract.spec.type, data: contract.input(data),
      ...(options.time !== undefined ? { time: options.time } : {}), ...(options.schemaVersion ? { schemaversion: options.schemaVersion } : {}), ...(options.environment ? { environment: options.environment } : {}) })
      .replace(/[<>&\u2028\u2029]/g, c => '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0'));
    parseOutboxRequest(body, scope.app_id); return body;
  } catch { throw new EventOutboxError('invalid_request'); }
}
export function outboxReceipt(value: unknown, scope: PipelineScope, key: string): EventPublicationReceipt {
  const r = value as { app_id: string; source: string; duplicate: boolean; receipt: { account_id: string; id: string; accepted_at: string } };
  try {
    if (!r || r.app_id !== scope.app_id || r.source !== 'app.' + scope.app_id || typeof r.duplicate !== 'boolean'
      || r.receipt?.account_id !== scope.account_id || r.receipt.id !== 'key.' + outboxHash(key)
      || outboxInstant(r.receipt.accepted_at) === -62135596800000000000n) throw new Error();
    return jobSnapshot({ app_id: r.app_id, source: r.source, account_id: r.receipt.account_id, event_id: r.receipt.id, accepted_at: r.receipt.accepted_at, duplicate: r.duplicate });
  } catch { throw new EventOutboxError('unverified'); }
}
