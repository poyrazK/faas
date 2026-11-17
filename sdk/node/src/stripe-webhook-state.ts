/** Verified native Stripe snapshot deliveries and payload-free handoff receipts. */
import { createHash } from 'node:crypto';
import { jobSnapshot, jobValueHash } from './job-runtime.js';
import { eventConsumerUUID } from './event-consumer-transactions.js';
import { outboxInstant } from './event-outbox-state.js';
import type { PipelineScope } from './pipeline-state.js';
export type StripeWebhookCode = 'invalid_delivery' | 'unsupported_version' | 'unauthorized' | 'scope_mismatch' | 'endpoint_unavailable' | 'conflict' | 'store_failed' | 'commit_unknown' | 'deadline' | 'cancelled';
export class StripeWebhookError extends Error {
    constructor(readonly code: StripeWebhookCode, readonly identity?: string) { super('Stripe webhook: ' + code); this.name = 'StripeWebhookError'; }
}
export interface StripeWebhookDelivery {
    readonly method: string;
    readonly path: string;
    readonly headers: Readonly<Record<string, string | readonly string[] | undefined>>;
    readonly body: Uint8Array;
}
export interface StripeDeliveryContext {
    readonly account_id: string;
    readonly app_id: string;
    readonly endpoint_id: string;
    readonly receipt_id: string;
    readonly event_id: string;
    readonly event_type: string;
    readonly body_sha256: string;
}
export interface StripeWebhookEvent {
    readonly id: string;
    readonly type: string;
    readonly api_version: string;
    readonly livemode: boolean;
    readonly created: number;
    readonly account?: string;
    readonly data_object: Readonly<Record<string, unknown>>;
}
export type StripeWebhookIgnoreReason = 'not_applicable' | 'already_applied' | 'cancelled' | 'superseded';
export type StripeWebhookDecision<E> = {
    state: 'stage';
    input: E;
} | {
    state: 'ignore';
    reason: StripeWebhookIgnoreReason;
};
export interface StripeWebhookReceipt {
    readonly version: 1;
    readonly identity: string;
    readonly endpoint_id: string;
    readonly event_id: string;
    readonly state: 'staged' | 'ignored';
    readonly owner_id: string;
    readonly outbox_key: string | null;
    readonly reason: '' | StripeWebhookIgnoreReason;
    readonly created_at: string;
    readonly updated_at: string;
}
export interface StripeWebhookOutcome {
    readonly outcome: 'staged' | 'ignored';
    readonly replayed: boolean;
    readonly identity: string;
    readonly receipt: StripeWebhookReceipt | null;
    readonly reason?: 'event_type' | StripeWebhookIgnoreReason;
}
export const stripeHash = (v: string | Uint8Array) => createHash('sha256').update(v).digest('hex');
export function stripeReceiptId(endpoint: string, event: string): string { const digest = createHash('sha1').update(Buffer.from('6ba7b8119dad11d180b400c04fd430c8', 'hex')).update('gregale:inbound-webhook:' + eventConsumerUUID(endpoint) + '\0' + event).digest().subarray(0, 16); digest[6] = (digest[6]! & 15) | 80; digest[8] = (digest[8]! & 63) | 128; const h = digest.toString('hex'); return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`; }
export const stripeEventId = (v: unknown): v is string => typeof v === 'string' && /^evt_[A-Za-z0-9_-]{1,252}$/.test(v) && v.length <= 256;
export const stripeEventType = (v: unknown): v is string => typeof v === 'string' && /^[a-z][a-z0-9_.]{0,255}$/.test(v);
export const stripeVersion = (v: unknown): v is string => typeof v === 'string' && /^\d{4}-\d{2}-\d{2}(?:\.[a-z][a-z0-9_-]{0,63})?$/.test(v);
export function stripePath(v: unknown): v is string { return typeof v === 'string' && /^\/[A-Za-z0-9_./~-]*$/.test(v) && !v.startsWith('//') && v.length <= 256 && !v.split('/').some(s => s === '.' || s === '..'); }
export function captureStripeDelivery(d: StripeWebhookDelivery): StripeWebhookDelivery {
    try {
        if (d.method !== 'POST' || !stripePath(d.path) || !(d.body instanceof Uint8Array) || !d.body.length || d.body.length > 1048576)
            throw new Error();
        return Object.freeze({ method: 'POST', path: d.path, body: Uint8Array.from(d.body), headers: jobSnapshot(d.headers) });
    }
    catch {
        throw new StripeWebhookError('invalid_delivery');
    }
}
/** Authenticate the platform delivery boundary first. Header parsing alone is not authentication. */
export function stripeDeliveryContextFromHeaders(headers: StripeWebhookDelivery['headers'], body: Uint8Array, accountId: string, appId: string): StripeDeliveryContext {
    try {
        const read = (name: string) => {
            const entries = Object.entries(headers).filter(([k]) => k.toLowerCase() === name);
            if (entries.length !== 1 || typeof entries[0]![1] !== 'string' || !entries[0]![1])
                throw new Error();
            return entries[0]![1] as string;
        };
        const endpoint = eventConsumerUUID(read('x-gregale-webhook-endpoint-id')), id = read('x-gregale-webhook-event-id'), type = read('x-gregale-webhook-event-type'), hash = read('x-gregale-webhook-body-sha256');
        if (read('x-gregale-webhook-provider') !== 'stripe' || !stripeEventId(id) || !stripeEventType(type) || hash !== stripeHash(body))
            throw new Error();
        const receipt = stripeReceiptId(endpoint, id), invocation = eventConsumerUUID(read('x-faas-invocation-id')), source = read('x-faas-invocation-source');
        if (source === 'inbound_webhook') {
            if (invocation !== receipt)
                throw new Error();
        }
        else if (source === 'replay') {
            const gen = read('x-faas-invocation-replay-generation');
            if (!/^[1-9][0-9]{0,18}$/.test(gen) || BigInt(gen) > 9223372036854775807n || eventConsumerUUID(read('x-faas-invocation-replay-root')) !== receipt || invocation === receipt)
                throw new Error();
        }
        else
            throw new Error();
        return jobSnapshot({ account_id: eventConsumerUUID(accountId), app_id: eventConsumerUUID(appId), endpoint_id: endpoint, receipt_id: receipt, event_id: id, event_type: type, body_sha256: hash });
    }
    catch {
        throw new StripeWebhookError('invalid_delivery');
    }
}
function jsonSafe(v: unknown): void {
    if (typeof v === 'number') {
        if (!Number.isFinite(v) || Math.abs(v) > Number.MAX_SAFE_INTEGER || Number.isInteger(v) && !Number.isSafeInteger(v))
            throw new Error();
    }
    else if (typeof v === 'string') {
        if (Buffer.from(v).toString('utf8') !== v)
            throw new Error();
    }
    else if (Array.isArray(v))
        v.forEach(jsonSafe);
    else if (v && typeof v === 'object')
        for (const [k, x] of Object.entries(v)) {
            jsonSafe(k);
            jsonSafe(x);
        }
}
export function stripeSnapshot(body: Uint8Array, context: StripeDeliveryContext, version: string, live: boolean, account?: string): StripeWebhookEvent {
    try {
        const raw = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(body));
        jsonSafe(raw);
        if (!raw || raw.object !== 'event' || !stripeEventId(raw.id) || !stripeEventType(raw.type) || raw.id !== context.event_id || raw.type !== context.event_type || typeof raw.livemode !== 'boolean' || raw.livemode !== live || (raw.account ?? undefined) !== account || !Number.isSafeInteger(raw.created) || raw.created < 0 || !raw.data || !raw.data.object || typeof raw.data.object !== 'object' || Array.isArray(raw.data.object) || typeof raw.data.object.id !== 'string' || !/^[A-Za-z0-9_-]{1,256}$/.test(raw.data.object.id) || typeof raw.data.object.object !== 'string')
            throw new Error();
        if (raw.api_version !== version)
            throw new StripeWebhookError('unsupported_version');
        return jobSnapshot({ id: raw.id, type: raw.type, api_version: raw.api_version, livemode: raw.livemode, created: raw.created, ...(account ? { account } : {}), data_object: raw.data.object });
    }
    catch (error) {
        if (error instanceof StripeWebhookError)
            throw error;
        throw new StripeWebhookError('invalid_delivery');
    }
}
export function stripeWebhookIdentity(scope: PipelineScope, endpoint: string, eventId: string): string { return jobValueHash(['gregale.stripe-webhook.v1', scope, endpoint, eventId]); }
export function stripeMetadata(row: Record<string, unknown>, identity: string, endpoint: string, eventId: string): StripeWebhookReceipt {
    try {
        if (row.identity !== identity || !['staged', 'ignored'].includes(String(row.state)) || typeof row.owner_id !== 'string' || !/^[A-Za-z0-9_.:-]{1,128}$/.test(row.owner_id) || row.state === 'staged' && (row.outbox_key !== 'stripe-webhook:' + identity || row.reason !== '') || row.state === 'ignored' && (row.outbox_key !== null || !['not_applicable', 'already_applied', 'cancelled', 'superseded'].includes(String(row.reason))))
            throw new Error();
        outboxInstant(row.created_at);
        outboxInstant(row.updated_at);
        return jobSnapshot({ version: 1, identity, endpoint_id: endpoint, event_id: eventId, state: row.state as 'staged' | 'ignored', owner_id: row.owner_id, outbox_key: row.outbox_key as string | null, reason: row.reason as StripeWebhookReceipt['reason'], created_at: row.created_at as string, updated_at: row.updated_at as string });
    }
    catch {
        throw new StripeWebhookError('store_failed', identity);
    }
}
