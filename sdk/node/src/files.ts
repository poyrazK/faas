/** Preview file transfers. Browser safe; the broker belongs to the application. */
export type FileCode = 'invalid_input' | 'invalid_checkpoint' | 'invalid_response' | 'source_changed' | 'session_closed' | 'expired' | 'request_failed' | 'acceptance_unknown' | 'limit' | 'poll_limit' | 'deadline' | 'cancelled' | 'callback_failed' | 'integrity_failed';
export class FileError extends Error {
    constructor(readonly code: FileCode, readonly status?: number) { super(`SDK file operation stopped: ${code}`); this.name = 'FileError'; }
}
export interface FileScope {
    readonly base_url: string;
    readonly app_slug: string;
    readonly bucket: string;
}
export interface FileSource {
    readonly size: number;
    /** Return exactly [start,end). Keep the source stable until the operation ends. */
    read(start: number, end: number, signal: AbortSignal): Promise<Uint8Array>;
}
export const blobFileSource = (blob: Blob): FileSource => ({ size: blob.size, async read(start, end, signal) { signal.throwIfAborted(); const b = new Uint8Array(await blob.slice(start, end).arrayBuffer()); signal.throwIfAborted(); return b; } });
export interface FileSession {
    readonly id: string;
    readonly key: string;
    readonly size_bytes: number;
    readonly part_size_bytes: number;
    readonly part_count: number;
    readonly content_type: string;
    readonly state: string;
    readonly expires_at: string;
    readonly etag?: string;
    readonly version_id?: string | null;
}
export interface FilePart {
    readonly part_number: number;
    readonly etag: string;
    readonly size_bytes: number;
}
export interface FileSignedRequest {
    readonly url: string;
    readonly method: string;
    readonly headers: Readonly<Record<string, string>>;
    readonly expires_at: string;
    readonly upload_id?: string;
}
export interface FileWriteReceipt {
    readonly id: string;
    readonly bucket_id: string;
    readonly key: string;
    readonly operation: string;
    readonly bytes: number;
    readonly status: string;
    readonly etag: string;
}
/** Implement using authenticated application routes for browsers. Never expose a
 * generic control-plane proxy: authorize each file ID, session and operation. */
export interface FileBroker {
    begin(key: string, size: number, contentType: string, signal: AbortSignal): Promise<FileSession>;
    session(id: string, signal: AbortSignal): Promise<FileSession>;
    parts(id: string, marker: number | undefined, signal: AbortSignal): Promise<{
        items: readonly FilePart[];
        next_part_number_marker?: number;
    }>;
    signPart(id: string, part: number, signal: AbortSignal): Promise<FileSignedRequest>;
    complete(id: string, parts: readonly {
        part_number: number;
        etag: string;
    }[], signal: AbortSignal): Promise<FileSession>;
    abort(id: string, signal: AbortSignal): Promise<void>;
    sign(key: string, method: 'GET' | 'HEAD' | 'PUT', options: {
        size_bytes?: number;
        content_type?: string;
        if_none_match?: '*';
        version_id?: string;
    }, signal: AbortSignal): Promise<FileSignedRequest>;
    receipt(id: string, signal: AbortSignal): Promise<FileWriteReceipt>;
    list(options: {
        prefix?: string;
        cursor?: string;
        limit: number;
    }, signal: AbortSignal): Promise<FilePage>;
    delete(key: string, signal: AbortSignal): Promise<void>;
}
export interface FileCheckpoint extends FileScope {
    readonly version: 1;
    readonly key: string;
    readonly size_bytes: number;
    /** SHA-256 of concatenated binary SHA-256 part digests, in part order. */
    readonly source_digest: string;
    readonly part_digests: readonly string[];
    readonly upload_id: string;
    readonly part_size_bytes: number;
    readonly part_count: number;
    readonly content_type: string;
    readonly parts: readonly FilePart[];
}
export interface FileProgress {
    readonly key: string;
    readonly confirmed_bytes: number;
    readonly total_bytes: number;
}
export interface FileOptions {
    readonly timeoutMs: number;
    readonly signal?: AbortSignal;
    readonly maxBytes?: number;
    readonly maxParts?: number;
    readonly maxPolls?: number;
    readonly pollDelayMs?: number;
    readonly onCheckpoint?: (checkpoint: FileCheckpoint) => void | Promise<void>;
    readonly onProgress?: (progress: FileProgress) => void | Promise<void>;
}
export interface FileUploadResult {
    readonly key: string;
    readonly size_bytes: number;
    readonly etag: string;
    readonly version_id?: string | null;
}
export interface FilePage {
    readonly items: readonly {
        key: string;
        size_bytes: number;
        last_modified: string;
    }[];
    readonly next_cursor?: string;
}
export interface FileBucket {
    readonly scope: FileScope;
    upload(key: string, source: FileSource, options: FileOptions & {
        contentType?: string;
    }): Promise<FileUploadResult>;
    resume(checkpoint: FileCheckpoint, source: FileSource, options: FileOptions): Promise<FileUploadResult>;
    abort(checkpoint: FileCheckpoint, options: FileOptions): Promise<void>;
    /** Save the receipt before dispatch to recover an uncertain PUT with confirm(). */
    put(key: string, bytes: Uint8Array, options: FileOptions & {
        contentType?: string;
        ifNoneMatch?: '*';
        onReceipt?: (id: string) => void | Promise<void>;
    }): Promise<FileUploadResult>;
    confirm(id: string, key: string, size: number, options: FileOptions): Promise<FileUploadResult>;
    downloadTo(key: string, sink: (chunk: Uint8Array) => void | Promise<void>, options: FileOptions & {
        versionId?: string;
    }): Promise<number>;
    download(key: string, options: FileOptions & {
        versionId?: string;
    }): Promise<Uint8Array>;
    metadata(key: string, options: FileOptions & {
        versionId?: string;
    }): Promise<{
        size_bytes: number;
        etag: string;
        content_type: string;
    }>;
    list(options: FileOptions & {
        prefix?: string;
        cursor?: string;
        limit?: number;
    }): Promise<FilePage>;
    delete(key: string, options: FileOptions): Promise<void>;
}
const nativeFetch = globalThis.fetch;
const BLOCK = 1024 * 1024, MAX = 1024 ** 4;
const integer = (n: unknown, min: number, max: number): n is number => Number.isSafeInteger(n) && Number(n) >= min && Number(n) <= max;
const uuid = (v: unknown): v is string => typeof v === 'string' && /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(v);
const hash = (v: unknown): v is string => typeof v === 'string' && /^[a-f0-9]{64}$/.test(v);
function validKey(key: string): void {
    if (typeof key !== 'string' || !key || new TextEncoder().encode(key).length > 1024 || /[\x00-\x1f\x7f]/.test(key))
        throw new FileError('invalid_input');
}
function contentType(v: string): string {
    if (!v || v.length > 255 || /[\x00-\x1f\x7f]/.test(v))
        throw new FileError('invalid_input');
    return v;
}
function scopeCopy(s: FileScope): FileScope {
    try {
        const u = new URL(s.base_url);
        if (!['https:', 'http:'].includes(u.protocol) || u.username || u.password || u.search || u.hash || u.href.replace(/\/+$/, '') !== s.base_url || !/^[a-z0-9][a-z0-9-]{0,62}$/.test(s.app_slug) || !uuid(s.bucket))
            throw new Error();
        return Object.freeze({ base_url: s.base_url, app_slug: s.app_slug, bucket: s.bucket });
    }
    catch {
        throw new FileError('invalid_input');
    }
}
function snapshot(c: FileCheckpoint): FileCheckpoint { return Object.freeze({ ...c, part_digests: Object.freeze([...c.part_digests]), parts: Object.freeze(c.parts.map(p => Object.freeze({ ...p }))) }); }
export function parseFileCheckpoint(value: unknown): FileCheckpoint {
    try {
        const c = value as FileCheckpoint;
        scopeCopy(c);
        validKey(c.key);
        contentType(c.content_type);
        if (Object.keys(c).sort().join() !== 'app_slug,base_url,bucket,content_type,key,part_count,part_digests,part_size_bytes,parts,size_bytes,source_digest,upload_id,version' || c.version !== 1 || !uuid(c.upload_id) || !hash(c.source_digest) || !integer(c.size_bytes, 1, MAX) || !integer(c.part_size_bytes, 1, 64 * BLOCK) || !integer(c.part_count, 1, 10000) || Math.ceil(c.size_bytes / c.part_size_bytes) !== c.part_count || !Array.isArray(c.part_digests) || c.part_digests.length !== c.part_count || c.part_digests.some(h => !hash(h)) || !Array.isArray(c.parts) || c.parts.length > c.part_count)
            throw new Error();
        let last = 0;
        for (const p of c.parts) {
            if (Object.keys(p).sort().join() !== 'etag,part_number,size_bytes' || !integer(p.part_number, last + 1, c.part_count) || p.size_bytes !== partSize(c, p.part_number) || !etag(p.etag))
                throw new Error();
            last = p.part_number;
        }
        return snapshot(c);
    }
    catch {
        throw new FileError('invalid_checkpoint');
    }
}
function etag(v: unknown): v is string { return typeof v === 'string' && v.length > 0 && v.length <= 1024 && !/[\x00-\x1f\x7f]/.test(v); }
function partSize(c: {
    size_bytes: number;
    part_size_bytes: number;
}, n: number): number { return Math.min(c.part_size_bytes, c.size_bytes - (n - 1) * c.part_size_bytes); }
async function sha(bytes: Uint8Array): Promise<Uint8Array> { return new Uint8Array(await crypto.subtle.digest('SHA-256', bytes as BufferSource)); }
const hex = (bytes: Uint8Array) => Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
async function read(source: FileSource, start: number, end: number, signal: AbortSignal): Promise<Uint8Array> {
    signal.throwIfAborted();
    const bytes = await source.read(start, end, signal);
    signal.throwIfAborted();
    if (!(bytes instanceof Uint8Array) || bytes.length !== end - start)
        throw new FileError('source_changed');
    return bytes;
}
/** Fixed block fingerprint shared with Python; memory remains bounded for large files. */
export async function fileSourceDigest(source: FileSource, signal: AbortSignal): Promise<string> {
    return (await fingerprint(source, signal, BLOCK)).digest;
}
async function fingerprint(source: FileSource, signal: AbortSignal, block: number): Promise<{
    digest: string;
    parts: string[];
}> {
    if (!integer(source.size, 0, MAX))
        throw new FileError('invalid_input');
    const blocks: Uint8Array[] = [];
    for (let i = 0; i < source.size; i += block)
        blocks.push(await sha(await read(source, i, Math.min(i + block, source.size), signal)));
    const all = new Uint8Array(blocks.length * 32);
    blocks.forEach((b, i) => all.set(b, i * 32));
    return { digest: hex(await sha(all)), parts: blocks.map(hex) };
}
async function callback<T>(fn: ((v: T) => void | Promise<void>) | undefined, v: T, signal: AbortSignal): Promise<void> {
    signal.throwIfAborted();
    if (fn)
        try {
            await fn(v);
        }
        catch {
            signal.throwIfAborted();
            throw new FileError('callback_failed');
        }
    signal.throwIfAborted();
}
async function operation<T>(options: FileOptions, work: (signal: AbortSignal) => Promise<T>): Promise<T> {
    if (!integer(options.timeoutMs, 1, 2147483647) || !integer(options.maxBytes ?? 100 * BLOCK, 0, MAX) || !integer(options.maxParts ?? 10000, 1, 10000) || !integer(options.maxPolls ?? 60, 1, 10000) || !integer(options.pollDelayMs ?? 1000, 0, 60000))
        throw new FileError('invalid_input');
    const controller = new AbortController(), stop = () => controller.abort(new FileError('cancelled'));
    if (options.signal?.aborted)
        stop();
    else
        options.signal?.addEventListener('abort', stop, { once: true });
    const timer = setTimeout(() => controller.abort(new FileError('deadline')), options.timeoutMs);
    try {
        controller.signal.throwIfAborted();
        return await new Promise<T>((resolve, reject) => {
            const cancel = () => reject(controller.signal.reason);
            controller.signal.addEventListener('abort', cancel, { once: true });
            Promise.resolve().then(() => { controller.signal.throwIfAborted(); return work(controller.signal); }).then(v => {
                controller.signal.removeEventListener('abort', cancel);
                if (controller.signal.aborted)
                    reject(controller.signal.reason);
                else
                    resolve(v);
            }, e => { controller.signal.removeEventListener('abort', cancel); reject(controller.signal.aborted ? controller.signal.reason : e instanceof FileError ? e : new FileError('request_failed')); });
        });
    }
    finally {
        clearTimeout(timer);
        options.signal?.removeEventListener('abort', stop);
        controller.abort(new FileError('cancelled'));
    }
}
async function pause(ms: number, signal: AbortSignal): Promise<void> {
    await new Promise<void>((resolve, reject) => {
        const cancel = () => { clearTimeout(timer); reject(signal.reason); };
        const timer = setTimeout(() => { signal.removeEventListener('abort', cancel); resolve(); }, ms);
        signal.addEventListener('abort', cancel, { once: true });
        if (signal.aborted)
            cancel();
    });
}
export function createFileBucket(scope: FileScope, broker: FileBroker, options: {
    dataFetch?: typeof fetch;
} = {}): FileBucket {
    const owned = scopeCopy(scope), dataFetch = options.dataFetch ?? nativeFetch;
    function bound(c: FileCheckpoint): FileCheckpoint {
        const p = parseFileCheckpoint(c);
        if (p.base_url !== owned.base_url || p.app_slug !== owned.app_slug || p.bucket !== owned.bucket)
            throw new FileError('invalid_checkpoint');
        return p;
    }
    function session(s: FileSession, key: string, size: number, c?: FileCheckpoint): FileSession {
        if (!s || !uuid(s.id) || s.key !== key || s.size_bytes !== size || !integer(s.part_size_bytes, 1, 64 * BLOCK) || !integer(s.part_count, 1, 10000) || s.part_count !== Math.ceil(size / s.part_size_bytes) || !['initiating', 'active', 'completing', 'completing_conditional', 'aborting', 'completed', 'aborted'].includes(s.state) || !Number.isFinite(Date.parse(s.expires_at)) || c && (s.id !== c.upload_id || s.part_size_bytes !== c.part_size_bytes || s.part_count !== c.part_count || s.content_type !== c.content_type))
            throw new FileError('invalid_response');
        contentType(s.content_type);
        if (s.state !== 'completed' && Date.parse(s.expires_at) <= Date.now())
            throw new FileError('expired');
        return s;
    }
    async function transfer(signed: FileSignedRequest, method: string, signal: AbortSignal, bytes?: Uint8Array): Promise<Response> {
        signal.throwIfAborted();
        let dispatched = false;
        try {
            const u = new URL(signed.url), headers = new Headers(signed.headers);
            if (signed.method !== method || u.username || u.password || u.hash || !(u.protocol === 'https:' || u.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(u.hostname)) || !Number.isFinite(Date.parse(signed.expires_at)) || Date.parse(signed.expires_at) <= Date.now() || ['authorization', 'cookie', 'proxy-authorization', 'host'].some(h => headers.has(h)))
                throw new FileError('invalid_response');
            if (bytes && headers.has('content-length') && headers.get('content-length') !== String(bytes.length))
                throw new FileError('invalid_response');
            headers.delete('content-length');
            dispatched = true;
            const response = await dataFetch(u, { method, headers, ...(bytes ? { body: new Blob([bytes as BlobPart]) } : {}), signal, redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer' });
            if (signal.aborted) {
                void response.body?.cancel().catch(() => { });
                signal.throwIfAborted();
            }
            if (!response.ok) {
                void response.body?.cancel().catch(() => { });
                throw new FileError(method === 'PUT' && response.status >= 500 ? 'acceptance_unknown' : 'request_failed', response.status);
            }
            return response;
        }
        catch (e) {
            signal.throwIfAborted();
            if (e instanceof FileError)
                throw e;
            throw new FileError(!dispatched ? 'invalid_response' : method === 'PUT' ? 'acceptance_unknown' : 'request_failed');
        }
    }
    async function confirmed(id: string, key: string, size: number, opt: FileOptions, signal: AbortSignal): Promise<FileUploadResult> {
        if (!uuid(id))
            throw new FileError('invalid_input');
        for (let i = 0; i < (opt.maxPolls ?? 60); i++) {
            signal.throwIfAborted();
            const r = await broker.receipt(id, signal);
            signal.throwIfAborted();
            if (r.id !== id || r.bucket_id !== owned.bucket || r.key !== key || r.bytes !== size || r.operation !== 'put' || !['pending', 'completed', 'failed'].includes(r.status))
                throw new FileError('invalid_response');
            if (r.status === 'failed')
                throw new FileError('request_failed');
            if (r.status === 'completed') {
                if (!etag(r.etag))
                    throw new FileError('invalid_response');
                return { key, size_bytes: size, etag: r.etag };
            }
            if (i + 1 < (opt.maxPolls ?? 60))
                await pause(opt.pollDelayMs ?? 1000, signal);
        }
        throw new FileError('poll_limit');
    }
    async function upload(key: string, source: FileSource, opt: FileOptions & {
        contentType?: string;
    }, signal: AbortSignal, prior?: FileCheckpoint): Promise<FileUploadResult> {
        validKey(key);
        if (!integer(source.size, 1, opt.maxBytes ?? 100 * BLOCK))
            throw new FileError('limit');
        let s = session(prior ? await broker.session(prior.upload_id, signal) : await broker.begin(key, source.size, contentType(opt.contentType ?? 'application/octet-stream'), signal), key, source.size, prior);
        for (let i = 0; s.state === 'initiating'; i++) {
            if (i >= (opt.maxPolls ?? 60) - 1)
                throw new FileError('poll_limit');
            await pause(opt.pollDelayMs ?? 1000, signal);
            s = session(await broker.session(s.id, signal), key, source.size, prior);
        }
        if (!prior && s.state !== 'active' || prior && ['completed', 'completing', 'completing_conditional'].includes(s.state) && prior.parts.length !== prior.part_count)
            throw new FileError('session_closed');
        if (s.part_count > (opt.maxParts ?? 10000))
            throw new FileError('limit');
        const fingerprinted = await fingerprint(source, signal, s.part_size_bytes);
        if (prior && (fingerprinted.digest !== prior.source_digest || fingerprinted.parts.some((h, i) => h !== prior.part_digests[i])))
            throw new FileError('source_changed');
        let c: FileCheckpoint = prior ?? snapshot({ ...owned, version: 1, key, size_bytes: source.size, source_digest: fingerprinted.digest, part_digests: fingerprinted.parts, upload_id: s.id, part_size_bytes: s.part_size_bytes, part_count: s.part_count, content_type: s.content_type, parts: [] });
        await callback(opt.onCheckpoint, c, signal);
        const finish = (s: FileSession): FileUploadResult => {
            if (!etag(s.etag))
                throw new FileError('invalid_response');
            return { key, size_bytes: source.size, etag: s.etag, ...(s.version_id !== undefined ? { version_id: s.version_id } : {}) };
        };
        if (s.state === 'completed')
            return finish(s);
        if (s.state === 'aborted' || s.state === 'aborting')
            throw new FileError('session_closed');
        if (s.state === 'active') {
            const known = new Map<number, FilePart>();
            let marker: number | undefined;
            for (let page = 0;; page++) {
                if (page >= Math.ceil((opt.maxParts ?? 10000) / 1000) + 1)
                    throw new FileError('limit');
                const p = await broker.parts(s.id, marker, signal);
                signal.throwIfAborted();
                if (!p || !Array.isArray(p.items) || p.items.length > 1000)
                    throw new FileError('invalid_response');
                let last = marker ?? 0;
                for (const part of p.items) {
                    if (!integer(part.part_number, last + 1, s.part_count) || part.size_bytes !== partSize(c, part.part_number) || !etag(part.etag))
                        throw new FileError('invalid_response');
                    known.set(part.part_number, part);
                    last = part.part_number;
                }
                if (p.next_part_number_marker === undefined)
                    break;
                if (!integer(p.next_part_number_marker, (marker ?? 0) + 1, s.part_count) || p.next_part_number_marker !== last)
                    throw new FileError('invalid_response');
                marker = p.next_part_number_marker;
            }
            const settled = c.parts.filter(p => known.get(p.part_number)?.etag === p.etag);
            c = snapshot({ ...c, parts: settled });
            await callback(opt.onCheckpoint, c, signal);
            for (let n = 1; n <= s.part_count; n++) {
                if (c.parts.some(p => p.part_number === n))
                    continue;
                const start = (n - 1) * s.part_size_bytes, bytes = await read(source, start, start + partSize(c, n), signal);
                if (hex(await sha(bytes)) !== c.part_digests[n - 1])
                    throw new FileError('source_changed');
                const signed = await broker.signPart(s.id, n, signal), response = await transfer(signed, 'PUT', signal, bytes);
                const tag = response.headers.get('etag');
                void response.body?.cancel().catch(() => { });
                if (!etag(tag))
                    throw new FileError('invalid_response');
                c = snapshot({ ...c, parts: [...c.parts, { part_number: n, etag: tag, size_bytes: bytes.length }].sort((a, b) => a.part_number - b.part_number) });
                await callback(opt.onCheckpoint, c, signal);
                await callback(opt.onProgress, { key, confirmed_bytes: c.parts.reduce((a, p) => a + p.size_bytes, 0), total_bytes: source.size }, signal);
            }
            signal.throwIfAborted();
            s = session(await broker.complete(s.id, c.parts.map(({ part_number, etag }) => ({ part_number, etag })), signal), key, source.size, c);
        }
        for (let i = 0; i < (opt.maxPolls ?? 60); i++) {
            signal.throwIfAborted();
            if (s.state === 'completed')
                return finish(s);
            if (!['initiating', 'completing', 'completing_conditional'].includes(s.state))
                throw new FileError('session_closed');
            if (i + 1 < (opt.maxPolls ?? 60)) {
                await pause(opt.pollDelayMs ?? 1000, signal);
                s = session(await broker.session(s.id, signal), key, source.size, c);
            }
        }
        throw new FileError('poll_limit');
    }
    async function downloadTo(key: string, sink: (chunk: Uint8Array) => void | Promise<void>, opt: FileOptions & {
        versionId?: string;
    }, signal: AbortSignal): Promise<number> {
        validKey(key);
        if (opt.versionId !== undefined && !uuid(opt.versionId))
            throw new FileError('invalid_input');
        const response = await transfer(await broker.sign(key, 'GET', { ...(opt.versionId ? { version_id: opt.versionId } : {}) }, signal), 'GET', signal);
        const reader = response.body?.getReader();
        let size = 0;
        const cancel = () => { void reader?.cancel().catch(() => { }); };
        signal.addEventListener('abort', cancel, { once: true });
        try {
            const length = response.headers.get('content-length');
            if (length !== null && (!/^\d+$/.test(length) || Number(length) > (opt.maxBytes ?? 100 * BLOCK)))
                throw new FileError('limit');
            if (reader)
                for (;;) {
                    signal.throwIfAborted();
                    const { done, value } = await reader.read();
                    signal.throwIfAborted();
                    if (done)
                        break;
                    size += value.length;
                    if (size > (opt.maxBytes ?? 100 * BLOCK))
                        throw new FileError('limit');
                    await callback(sink, value, signal);
                }
            if (length !== null && size !== Number(length))
                throw new FileError('integrity_failed');
            return size;
        }
        finally {
            signal.removeEventListener('abort', cancel);
            void reader?.cancel().catch(() => { });
        }
    }
    return Object.freeze({ scope: owned,
        upload: (key, source, opt) => operation(opt, signal => upload(key, source, opt, signal)),
        resume: (value, source, opt) => operation(opt, signal => {
            const c = bound(value);
            if (source.size !== c.size_bytes)
                throw new FileError('source_changed');
            return upload(c.key, source, opt, signal, c);
        }),
        abort: (value, opt) => operation(opt, signal => broker.abort(bound(value).upload_id, signal)),
        put: (key, bytes, opt) => {
            const data = new Uint8Array(bytes);
            return operation(opt, async (signal) => {
                validKey(key);
                if (opt.ifNoneMatch !== undefined && opt.ifNoneMatch !== '*')
                    throw new FileError('invalid_input');
                if (data.length > (opt.maxBytes ?? 100 * BLOCK))
                    throw new FileError('limit');
                const signed = await broker.sign(key, 'PUT', { size_bytes: data.length, content_type: contentType(opt.contentType ?? 'application/octet-stream'), ...(opt.ifNoneMatch ? { if_none_match: opt.ifNoneMatch } : {}) }, signal);
                if (!uuid(signed.upload_id))
                    throw new FileError('invalid_response');
                await callback(opt.onReceipt, signed.upload_id, signal);
                const response = await transfer(signed, 'PUT', signal, data);
                void response.body?.cancel().catch(() => { });
                return confirmed(signed.upload_id, key, data.length, opt, signal);
            });
        },
        confirm: (id, key, size, opt) => operation(opt, signal => {
            validKey(key);
            if (!integer(size, 0, opt.maxBytes ?? 100 * BLOCK))
                throw new FileError('limit');
            return confirmed(id, key, size, opt, signal);
        }),
        downloadTo: (key, sink, opt) => operation(opt, signal => downloadTo(key, sink, opt, signal)),
        download: (key, opt) => operation(opt, async (signal) => {
            const chunks: Uint8Array[] = [];
            const size = await downloadTo(key, b => { chunks.push(b); }, { ...opt, maxBytes: opt.maxBytes ?? 100 * BLOCK }, signal);
            const bytes = new Uint8Array(size);
            let offset = 0;
            for (const c of chunks) {
                bytes.set(c, offset);
                offset += c.length;
            }
            return bytes;
        }),
        metadata: (key, opt) => operation(opt, async (signal) => {
            validKey(key);
            if (opt.versionId !== undefined && !uuid(opt.versionId))
                throw new FileError('invalid_input');
            const r = await transfer(await broker.sign(key, 'HEAD', { ...(opt.versionId ? { version_id: opt.versionId } : {}) }, signal), 'HEAD', signal);
            void r.body?.cancel().catch(() => { });
            const n = r.headers.get('content-length'), tag = r.headers.get('etag');
            if (n === null || !/^\d+$/.test(n) || !integer(Number(n), 0, MAX) || !etag(tag))
                throw new FileError('invalid_response');
            return { size_bytes: Number(n), etag: tag, content_type: r.headers.get('content-type') ?? 'application/octet-stream' };
        }),
        list: opt => operation(opt, async (signal) => {
            if (!integer(opt.limit ?? 100, 1, 1000) || opt.cursor !== undefined && (typeof opt.cursor !== 'string' || opt.cursor.length > 8192))
                throw new FileError('invalid_input');
            const p = await broker.list({ limit: opt.limit ?? 100, ...(opt.prefix !== undefined ? { prefix: opt.prefix } : {}), ...(opt.cursor !== undefined ? { cursor: opt.cursor } : {}) }, signal);
            signal.throwIfAborted();
            if (!p || !Array.isArray(p.items) || p.items.length > (opt.limit ?? 100) || p.items.some(i => typeof i.key !== 'string' || !integer(i.size_bytes, 0, MAX) || !Number.isFinite(Date.parse(i.last_modified))) || p.next_cursor !== undefined && (typeof p.next_cursor !== 'string' || !p.next_cursor || p.next_cursor.length > 8192))
                throw new FileError('invalid_response');
            return structuredClone(p);
        }),
        delete: (key, opt) => operation(opt, signal => { validKey(key); return broker.delete(key, signal); }),
    } satisfies FileBucket);
}
