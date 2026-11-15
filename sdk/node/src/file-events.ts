/** Typed owned storage events. Parsing is distinct from authenticating delivery. */
import { createHash } from 'node:crypto';
import { jobSnapshot, jobValueHash } from './job-runtime.js';
export type FilePipelineCode = 'invalid_input' | 'invalid_event' | 'unverified_delivery' | 'scope_mismatch' | 'authorization_denied' | 'source_unavailable' | 'source_changed' | 'version_required' | 'conflict' | 'store_failed' | 'acceptance_unknown' | 'deadline' | 'cancelled' | 'limit' | 'callback_failed';
export class FilePipelineError extends Error {
    constructor(readonly code: FilePipelineCode, readonly runId?: string) { super(`SDK file pipeline stopped: ${code}`); this.name = 'FilePipelineError'; }
}
export interface FilePipelineScope {
    readonly base_url: string;
    readonly account_id: string;
    readonly app_id: string;
    readonly app_slug: string;
    readonly bucket: string;
}
export interface FileCreatedEvent {
    readonly version: 1;
    readonly account_id: string;
    readonly app_id: string;
    readonly bucket: string;
    readonly event_id: string;
    readonly receipt_id: string;
    readonly key: string;
    readonly size_bytes: number;
    readonly etag: string;
    readonly version_id: string | null;
    readonly operation: 'put' | 'copy' | 'upload' | 'complete_multipart_upload';
}
export interface FileDelivery {
    readonly body: Uint8Array;
    readonly invocationId: string;
}
export type FileEventVerifier = (delivery: FileDelivery, signal: AbortSignal) => Promise<FileCreatedEvent>;
export const fileEventUUID = (v: unknown): v is string => typeof v === 'string' && /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(v) && v !== '00000000-0000-0000-0000-000000000000';
export const fileEventHash = (v: unknown): v is string => typeof v === 'string' && /^[a-f0-9]{64}$/.test(v);
export function filePipelineScope(scope: FilePipelineScope): FilePipelineScope {
    try {
        const u = new URL(scope.base_url);
        if (!['https:', 'http:'].includes(u.protocol) || u.username || u.password || u.search || u.hash || u.href.replace(/\/+$/, '') !== scope.base_url || !fileEventUUID(scope.account_id) || !fileEventUUID(scope.app_id) || !fileEventUUID(scope.bucket) || !/^[a-z0-9][a-z0-9-]{0,62}$/.test(scope.app_slug))
            throw new Error();
        return jobSnapshot({ base_url: scope.base_url, account_id: scope.account_id, app_id: scope.app_id, app_slug: scope.app_slug, bucket: scope.bucket });
    }
    catch {
        throw new FilePipelineError('invalid_input');
    }
}
export function filePipelineIdentity(name: string, scope: FilePipelineScope, receiptId: string): string {
    filePipelineScope(scope);
    if (typeof name !== 'string' || !/^[A-Za-z0-9_.:-]{1,128}$/.test(name) || !fileEventUUID(receiptId))
        throw new FilePipelineError('invalid_input');
    return jobValueHash([name, scope.account_id, scope.app_id, scope.bucket, receiptId]);
}
function object(v: unknown): v is Record<string, any> { return !!v && typeof v === 'object' && !Array.isArray(v); }
function tag(v: unknown): string {
    if (typeof v !== 'string')
        throw new Error();
    const s = v.replace(/^"+|"+$/g, '');
    if (!s || s.length > 1024 || /["\x00-\x1f\x7f]/.test(s))
        throw new Error();
    return '"' + s + '"';
}
/** Native S3 Records use form encoding; CloudEvents keys are already decoded. */
export function parseFileCreatedEvent(value: unknown, eventId: string, scope: FilePipelineScope): FileCreatedEvent {
    try {
        filePipelineScope(scope);
        if (!object(value))
            throw new Error();
        const match = /^(write|multipart):([a-f0-9-]{36})$/.exec(eventId);
        if (!match || !fileEventUUID(match[2]))
            throw new Error();
        let key: unknown, size: unknown, etag: unknown, version: unknown, operation: FileCreatedEvent['operation'];
        if (Array.isArray(value.Records)) {
            if (value.Records.length !== 1)
                throw new Error();
            const r = value.Records[0], s = r?.s3, b = s?.bucket, o = s?.object;
            if (r?.eventSource !== 'aws:s3' || r.eventVersion !== '2.1' || s?.s3SchemaVersion !== '1.0' || !object(b) || !object(o))
                throw new Error();
            const arn = typeof b.arn === 'string' ? /^arn:gregale:s3:([a-z0-9-]+):([a-f0-9-]{36}):bucket:([a-f0-9-]{36})$/.exec(b.arn) : null;
            if (!arn || arn[1] !== r.awsRegion || arn[2] !== scope.account_id || arn[3] !== scope.bucket || b.ownerIdentity?.principalId !== scope.account_id)
                throw new FilePipelineError('scope_mismatch');
            if (typeof o.key !== 'string')
                throw new Error();
            key = decodeURIComponent(o.key.replace(/\+/g, ' '));
            size = o.size;
            etag = o.eTag;
            version = o.versionId;
            const operations: Record<string, FileCreatedEvent['operation']> = { 'ObjectCreated:Put': 'put', 'ObjectCreated:Copy': 'copy', 'ObjectCreated:CompleteMultipartUpload': 'complete_multipart_upload' };
            operation = operations[r.eventName]!;
            if (!operation)
                throw new Error();
        }
        else {
            const d = value.data;
            if (value.specversion !== '1.0' || value.source !== 'gregale.storage' || value.type !== 'object.created' || value.id !== eventId || !object(d))
                throw new Error();
            if (value.accountid !== scope.account_id || d.app_id !== scope.app_id || d.bucket_id !== scope.bucket)
                throw new FilePipelineError('scope_mismatch');
            if (d.receipt_id !== match[2] || d.cause !== 'customer' || d.delete_marker === true || !['put', 'copy', 'upload', 'complete_multipart_upload'].includes(d.operation))
                throw new Error();
            key = d.key;
            size = d.size_bytes;
            etag = d.etag;
            version = d.version_id;
            operation = d.operation === 'upload' ? 'put' : d.operation;
        }
        if ((operation === 'complete_multipart_upload' ? 'multipart' : 'write') !== match[1] || typeof key !== 'string' || !key || Buffer.byteLength(key) > 1024 || /[\x00-\x1f\x7f]/.test(key) || Buffer.from(key).toString() !== key || !Number.isSafeInteger(size) || Number(size) < 0 || Number(size) > 1024 ** 4 || version !== undefined && version !== '' && version !== null && version !== 'null' && !fileEventUUID(version))
            throw new Error();
        return jobSnapshot({ version: 1, ...{ account_id: scope.account_id, app_id: scope.app_id, bucket: scope.bucket }, event_id: eventId, receipt_id: match[2]!, key, size_bytes: Number(size), etag: tag(etag), version_id: version && version !== 'null' ? version : null, operation });
    }
    catch (e) {
        if (e instanceof FilePipelineError)
            throw e;
        throw new FilePipelineError('invalid_event');
    }
}
/** Validate and copy only the owned event fields, including for custom verifiers. */
export function captureFileCreatedEvent(value: FileCreatedEvent, scope: FilePipelineScope): FileCreatedEvent {
    if (value.version !== 1)
        throw new FilePipelineError('invalid_event');
    return parseFileCreatedEvent({ specversion: '1.0', source: 'gregale.storage', type: 'object.created', id: value.event_id, accountid: value.account_id, data: { receipt_id: value.receipt_id, app_id: value.app_id, bucket_id: value.bucket, key: value.key, size_bytes: value.size_bytes, etag: value.etag, version_id: value.version_id, operation: value.operation, cause: 'customer' } }, value.event_id, scope);
}
/** Matches state.PublishedEventInvocationID, including Go JSON HTML escaping. */
export function fileEventInvocationId(account: string, event: string, subscription: string): string {
    const json = JSON.stringify([account, 'gregale.storage', event, subscription]).replace(/[<>&\u2028\u2029]/g, c => '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0'));
    const digest = createHash('sha1').update(Buffer.from('6ba7b8119dad11d180b400c04fd430c8', 'hex')).update(json).digest();
    digest[6] = (digest[6]! & 15) | 80;
    digest[8] = (digest[8]! & 63) | 128;
    const h = digest.subarray(0, 16).toString('hex');
    return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}
/** Read the account-scoped invocation through an authenticated SDK client.
 * Public caller-supplied event headers are never proof. Replays must match the
 * retained original native delivery. Missing/pruned proof fails closed. */
export function createNativeFileVerifier(scope: FilePipelineScope, lookup: (id: string, signal: AbortSignal) => PromiseLike<unknown>): FileEventVerifier {
    scope = filePipelineScope(scope);
    return async (delivery, signal) => {
        try {
            filePipelineCheck(signal);
            if (!fileEventUUID(delivery.invocationId) || !(delivery.body instanceof Uint8Array) || !delivery.body.length || delivery.body.length > 1024 * 1024)
                throw new Error();
            const payload = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(delivery.body));
            const current = await lookup(delivery.invocationId, signal);
            filePipelineCheck(signal);
            if (!object(current) || current.id !== delivery.invocationId || current.account_id !== scope.account_id || current.app_id !== scope.app_id || !['async_invoke', 'queue', 'replay'].includes(current.source) || current.method !== 'POST' || current.path !== '/' || jobValueHash(current.payload) !== jobValueHash(payload) || !object(current.headers))
                throw new Error();
            const headers: Record<string, string> = {};
            for (const [key, value] of Object.entries(current.headers)) {
                const name = key.toLowerCase();
                if (typeof value !== 'string' || Object.hasOwn(headers, name))
                    throw new Error();
                headers[name] = value;
            }
            const event = headers['x-gregale-event-id'], subscription = headers['x-gregale-event-subscription-id'];
            if (headers['x-gregale-event-source'] !== 'gregale.storage' || headers['x-gregale-event-type'] !== 'object.created' || !event || !subscription || Buffer.byteLength(subscription) > 4096)
                throw new Error();
            const originalId = fileEventInvocationId(scope.account_id, event, subscription);
            const original = originalId === current.id ? current : await lookup(originalId, signal);
            filePipelineCheck(signal);
            if (!object(original) || original.id !== originalId || original.account_id !== scope.account_id || original.app_id !== scope.app_id || !['async_invoke', 'queue'].includes(original.source) || original.method !== 'POST' || original.path !== '/' || jobValueHash(original.payload) !== jobValueHash(payload) || !object(original.headers))
                throw new Error();
            for (const name of ['x-gregale-event-id', 'x-gregale-event-source', 'x-gregale-event-type', 'x-gregale-event-subscription-id']) {
                const values = Object.entries(original.headers).filter(([key]) => key.toLowerCase() === name);
                if (values.length !== 1 || values[0]![1] !== headers[name])
                    throw new Error();
            }
            return parseFileCreatedEvent(payload, event, scope);
        }
        catch (e) {
            filePipelineCheck(signal);
            if (e instanceof FilePipelineError)
                throw e;
            throw new FilePipelineError('unverified_delivery');
        }
    };
}
export interface FilePipelineOptions {
    readonly timeoutMs: number;
    readonly signal?: AbortSignal;
    readonly maxBytes?: number;
    readonly maxItems?: number;
}
const pipelineChecks = new WeakMap<AbortSignal, () => void>();
/** Also checks elapsed monotonic time when a callback has delayed timers. */
export function filePipelineCheck(signal: AbortSignal): void {
    const check = pipelineChecks.get(signal);
    if (check)
        check();
    else
        signal.throwIfAborted();
}
/** One scope fences late callback/store responses before any subsequent write. */
export async function filePipelineRun<T>(options: FilePipelineOptions, work: (signal: AbortSignal) => Promise<T>): Promise<T> {
    const { timeoutMs, signal } = options;
    if (!Number.isSafeInteger(timeoutMs) || timeoutMs <= 0 || timeoutMs > 2147483647 || !Number.isSafeInteger(options.maxBytes ?? 16 * 1024 * 1024) || (options.maxBytes ?? 0) < 0 || (options.maxBytes ?? 0) > 1024 ** 4 || !Number.isSafeInteger(options.maxItems ?? 1000) || (options.maxItems ?? 1000) < 1 || (options.maxItems ?? 1000) > 10000)
        throw new FilePipelineError('invalid_input');
    const controller = new AbortController(), cancel = () => controller.abort(new FilePipelineError('cancelled'));
    const deadline = performance.now() + timeoutMs;
    pipelineChecks.set(controller.signal, () => { if (performance.now() >= deadline && !controller.signal.aborted)
        controller.abort(new FilePipelineError('deadline')); controller.signal.throwIfAborted(); });
    if (signal?.aborted)
        cancel();
    else
        signal?.addEventListener('abort', cancel, { once: true });
    const timer = setTimeout(() => controller.abort(new FilePipelineError('deadline')), timeoutMs);
    try {
        filePipelineCheck(controller.signal);
        return await new Promise<T>((resolve, reject) => {
            const stop = () => reject(controller.signal.reason);
            controller.signal.addEventListener('abort', stop, { once: true });
            Promise.resolve().then(() => { filePipelineCheck(controller.signal); return work(controller.signal); }).then(value => {
                controller.signal.removeEventListener('abort', stop);
                try {
                    filePipelineCheck(controller.signal);
                    resolve(value);
                }
                catch (error) {
                    reject(error);
                }
            }, error => { controller.signal.removeEventListener('abort', stop); try {
                filePipelineCheck(controller.signal);
                reject(error instanceof FilePipelineError ? error : new FilePipelineError('callback_failed'));
            }
            catch (stopped) {
                reject(stopped);
            } });
        });
    }
    finally {
        pipelineChecks.delete(controller.signal);
        clearTimeout(timer);
        signal?.removeEventListener('abort', cancel);
        controller.abort(new FilePipelineError('cancelled'));
    }
}
