/** Native preview storage broker. Control-plane transport never handles file bytes. */
import { createHash, randomUUID } from 'node:crypto';
import { open, stat } from 'node:fs/promises';
import type { ClientConfig } from './client-instance.js';
import { scopedRequest } from './request-scope.js';
import { createFileBucket, FileError, type FileBucket, type FileBroker, type FileSource } from './files.js';
import { JOB_MAX_BYTES, JobError, type JobStore } from './job-runtime.js';
export interface ManagedFileBucket extends FileBucket {
    /** Resolve the owned app ID once; the adapter stores content-addressed JSON bytes. */
    jobStore(options: {
        timeoutMs: number;
        signal?: AbortSignal;
        prefix?: string;
    }): Promise<JobStore>;
}
export interface FileHelpers {
    files(options: {
        appSlug: string;
        bucket: string;
        dataFetch?: typeof fetch;
    }): ManagedFileBucket;
}
/** Bounded random access; each read closes its handle, including on cancellation. */
export async function pathFileSource(path: string): Promise<FileSource> {
    const info = await stat(path);
    if (!info.isFile())
        throw new FileError('invalid_input');
    return { size: info.size, async read(start, end, signal) {
            signal.throwIfAborted();
            const handle = await open(path, 'r');
            try {
                const bytes = new Uint8Array(end - start);
                let offset = 0;
                while (offset < bytes.length) {
                    signal.throwIfAborted();
                    const { bytesRead } = await handle.read(bytes, offset, bytes.length - offset, start + offset);
                    if (!bytesRead)
                        break;
                    offset += bytesRead;
                }
                signal.throwIfAborted();
                return bytes.subarray(0, offset);
            }
            finally {
                await handle.close();
            }
        } };
}
export function bindFiles(getConfig: () => ClientConfig): FileHelpers {
    return { files({ appSlug, bucket, dataFetch }) {
            // A bucket handle owns a configuration snapshot, including its credentials.
            const original = getConfig(), config: ClientConfig = { ...original, requestFetch: opts => {
                    const fetch = original.requestFetch(opts);
                    return async (url, init) => {
                        const response = await fetch(url, { ...init, redirect: 'error' }), reader = response.body?.getReader();
                        const chunks: Uint8Array[] = [];
                        let size = 0;
                        const cancel = () => { void reader?.cancel().catch(() => { }); };
                        init?.signal?.addEventListener('abort', cancel, { once: true });
                        try {
                            if (reader)
                                for (;;) {
                                    init?.signal?.throwIfAborted();
                                    const { done, value } = await reader.read();
                                    init?.signal?.throwIfAborted();
                                    if (done)
                                        break;
                                    size += value.length;
                                    if (size > 1024 * 1024)
                                        throw new FileError('limit');
                                    chunks.push(value);
                                }
                            return new Response([204, 205, 304].includes(response.status) ? null : Buffer.concat(chunks), { status: response.status, headers: response.headers });
                        }
                        finally {
                            init?.signal?.removeEventListener('abort', cancel);
                            void reader?.cancel().catch(() => { });
                        }
                    };
                } }, path = `/v1/apps/${encodeURIComponent(appSlug)}/buckets/${encodeURIComponent(bucket)}`;
            async function request<T>(suffix: string, method: 'GET' | 'POST' | 'DELETE', signal: AbortSignal, body?: unknown): Promise<T> {
                signal.throwIfAborted();
                try {
                    const result = await scopedRequest<T>(config, { method, url: path + suffix, ...(body === undefined ? {} : { body, mediaType: 'application/json' }), ...(method !== 'GET' ? { headers: { 'Idempotency-Key': randomUUID() } } : {}) }, { signal, timeoutMs: 0, retry: false });
                    signal.throwIfAborted();
                    return result;
                }
                catch (e) {
                    signal.throwIfAborted();
                    const status = (e as {
                        status?: number;
                    })?.status;
                    throw new FileError(method !== 'GET' && (!status || status >= 500) ? 'acceptance_unknown' : 'request_failed', status);
                }
            }
            const broker: FileBroker = {
                begin: (key, size, contentType, signal) => request('/multipart-uploads', 'POST', signal, { key, size_bytes: size, content_type: contentType }),
                session: (id, signal) => request('/multipart-uploads/' + encodeURIComponent(id), 'GET', signal),
                parts: (id, marker, signal) => request(`/multipart-uploads/${encodeURIComponent(id)}/parts?limit=1000${marker === undefined ? '' : '&part_number_marker=' + marker}`, 'GET', signal),
                signPart: (id, part, signal) => request(`/multipart-uploads/${encodeURIComponent(id)}/parts/${part}/signed-url`, 'POST', signal, {}),
                complete: (id, parts, signal) => request(`/multipart-uploads/${encodeURIComponent(id)}/complete`, 'POST', signal, { parts }),
                abort: (id, signal) => request('/multipart-uploads/' + encodeURIComponent(id), 'DELETE', signal),
                sign: (key, method, options, signal) => request('/signed-url', 'POST', signal, { key, method, ...options }),
                receipt: (id, signal) => request('/write-receipts/' + encodeURIComponent(id), 'GET', signal),
                list: (options, signal) => request('/objects?' + new URLSearchParams(Object.entries(options).map(([k, v]) => [k, String(v)])), 'GET', signal),
                delete: (key, signal) => request('/objects?' + new URLSearchParams({ key }), 'DELETE', signal),
            };
            const files = createFileBucket({ base_url: config.BASE, app_slug: appSlug, bucket }, broker, { dataFetch });
            return Object.freeze({ ...files, async jobStore(options) {
                    const { prefix = 'jobs/', timeoutMs, signal } = options;
                    if (!/^[A-Za-z0-9_-][A-Za-z0-9/_-]{0,127}\/$/.test(prefix))
                        throw new FileError('invalid_input');
                    const app = await scopedRequest<{
                        id: string;
                    }>(config, { method: 'GET', url: '/v1/apps/{slug}', path: { slug: appSlug } }, { timeoutMs, signal, retry: false });
                    if (!app || !/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(app.id))
                        throw new FileError('invalid_response');
                    const root = `obj://${app.id}/${bucket}/${prefix}`;
                    const digest = (b: Uint8Array) => createHash('sha256').update(b).digest('hex');
                    const store: JobStore = {
                        async put(bytes, sha, context) {
                            context.signal.throwIfAborted();
                            const data = new Uint8Array(bytes);
                            if (data.length > JOB_MAX_BYTES || digest(data) !== sha)
                                throw new JobError('integrity_failed');
                            try {
                                await files.put(prefix + sha, data, { timeoutMs, signal: context.signal, maxBytes: JOB_MAX_BYTES, ifNoneMatch: '*', contentType: 'application/json' });
                            }
                            catch (e) {
                                if (!(e instanceof FileError) || ![409, 412].includes(e.status ?? 0))
                                    throw e;
                            }
                            const stored = await files.download(prefix + sha, { timeoutMs, signal: context.signal, maxBytes: JOB_MAX_BYTES });
                            if (digest(stored) !== sha)
                                throw new JobError('integrity_failed');
                            return root + sha;
                        },
                        async get(uri, context) {
                            if (!uri.startsWith(root) || !/^[a-f0-9]{64}$/.test(uri.slice(root.length)))
                                throw new JobError('invalid_reference');
                            const sha = uri.slice(root.length), bytes = await files.download(prefix + sha, { timeoutMs, signal: context.signal, maxBytes: JOB_MAX_BYTES });
                            if (digest(bytes) !== sha)
                                throw new JobError('integrity_failed');
                            return bytes;
                        },
                    };
                    return Object.freeze(store);
                } } satisfies ManagedFileBucket);
        } };
}
