import { randomUUID } from 'node:crypto';
import type { ClientConfig } from './client-instance.js';
import { scopedRequest } from './request-scope.js';
import { JobContract, JobError, jobURI, jobScope, jobSnapshot, jobHash, jobInputId, inspectJobInput, prepareJobInput, decodeJobOutput, JOB_MAX_BYTES, type JobInput, type JobInputBinding, type JobStore, type JobArtifact } from './job-runtime.js';
export interface JobCheckpoint {
    readonly version: 1;
    readonly base_url: string;
    readonly job_name: string;
    readonly job_id: string;
    readonly account_id: string;
    readonly run_id: string;
    readonly contract_sha256: string;
    readonly input_digest: string;
    readonly inputs: readonly {
        readonly input_id: string;
        readonly binding_sha256: string;
        readonly source_task_index: number | null;
    }[];
    readonly source_run_id: string | null;
}
export interface JobProgress {
    readonly runId: string;
    readonly status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'dead_letter';
    readonly total: number;
    readonly succeeded: number;
    readonly failed: number;
    readonly cancelled: number;
    readonly running: number;
}
export interface JobRunOptions {
    readonly timeoutMs: number;
    readonly signal?: AbortSignal;
    readonly maxRequests?: number;
    readonly maxPages?: number;
    readonly maxItems?: number;
    readonly maxPolls?: number;
    readonly pollDelayMs?: number;
    readonly onProgress?: (progress: JobProgress) => void | Promise<void>;
}
export interface JobSubmitOptions extends JobRunOptions {
    /** Persist and reuse for the SAME submission within the server's 24h window. */
    readonly idempotencyKey: string;
    /** Optional guards for composed workflows; checked before preparing inputs or POST. */
    readonly expectedAccountId?: string;
    readonly expectedBaseURL?: string;
    /** Composition guard: reject a job handle defined with another contract. */
    readonly expectedContractSha256?: string;
    readonly parallelism?: number;
    readonly retryMax?: number;
    readonly failurePolicy?: 'continue' | 'fail_fast';
}
export type JobItemOutcome<Output> = {
    readonly inputId: string;
    readonly index: number;
    readonly attempt: number;
} & ({
    readonly outcome: 'succeeded';
    readonly result: Output;
    readonly code?: undefined;
} | {
    readonly outcome: 'failed' | 'cancelled' | 'pending' | 'invalid_result';
    readonly result?: undefined;
    readonly code?: string;
});
export interface TypedJobRun<Output> {
    readonly checkpoint: JobCheckpoint;
    status(options: JobRunOptions): Promise<JobProgress>;
    wait(options: JobRunOptions): Promise<JobProgress>;
    results(options: JobRunOptions): Promise<readonly JobItemOutcome<Output>[]>;
    cancel(options: JobRunOptions & {
        readonly idempotencyKey?: string;
    }): Promise<JobProgress>;
    retryFailed(options: JobRunOptions & {
        readonly idempotencyKey: string;
    }): Promise<TypedJobRun<Output>>;
}
export interface TypedJob<Input, Output> {
    submit(inputs: readonly JobInput<Input>[], options: JobSubmitOptions): Promise<TypedJobRun<Output>>;
    resume(checkpoint: JobCheckpoint): TypedJobRun<Output>;
    resumeById(runId: string, options: JobRunOptions): Promise<TypedJobRun<Output>>;
}
export interface TypedJobs {
    defineJob<Input, Output>(contract: JobContract<Input, Output>, options?: {
        readonly store?: JobStore;
        readonly artifactFetch?: typeof fetch;
    }): TypedJob<Input, Output>;
}
const uuid = (v: unknown): v is string => typeof v === 'string' && /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(v);
const hash = (v: unknown): v is string => typeof v === 'string' && /^[a-f0-9]{64}$/.test(v);
const positive = (v: unknown): v is number => Number.isSafeInteger(v) && Number(v) > 0;
const nonnegative = (v: unknown): v is number => Number.isSafeInteger(v) && Number(v) >= 0;
const nativeFetch = globalThis.fetch;
const terminal = new Set(['succeeded', 'failed', 'cancelled', 'dead_letter']);
const failed = new Set(['failed', 'timeout', 'oom', 'cancelled']);
function endpoint(v: string): string { const u = new URL(v); if (!['https:', 'http:'].includes(u.protocol) || u.username || u.password || u.search || u.hash)
    throw new JobError('invalid_receipt'); return u.href.replace(/\/+$/, ''); }
export function parseJobCheckpoint(value: unknown): JobCheckpoint {
    try {
        const c = value as JobCheckpoint;
        if (Object.keys(c).sort().join() !== 'account_id,base_url,contract_sha256,input_digest,inputs,job_id,job_name,run_id,source_run_id,version' || c.version !== 1 || endpoint(c.base_url) !== c.base_url || !/^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/.test(c.job_name) || !uuid(c.job_id) || !uuid(c.account_id) || !uuid(c.run_id) || !hash(c.contract_sha256) || !hash(c.input_digest) || c.source_run_id !== null && !uuid(c.source_run_id) || !Array.isArray(c.inputs) || !c.inputs.length || c.inputs.length > 10000 || new Set(c.inputs.map(i => i.input_id)).size !== c.inputs.length || c.inputs.some(i => !i || Object.keys(i).sort().join() !== 'binding_sha256,input_id,source_task_index' || !jobInputId(i.input_id) || !hash(i.binding_sha256) || i.source_task_index !== null && !nonnegative(i.source_task_index) || c.source_run_id === null && i.source_task_index !== null || c.source_run_id !== null && i.source_task_index === null))
            throw new Error();
        return jobSnapshot(c);
    }
    catch {
        throw new JobError('invalid_receipt');
    }
}
function key(value: string): string { if (typeof value !== 'string' || !/^[\x21-\x7e]{1,255}$/.test(value))
    throw new JobError('invalid_input'); return value; }
function limits(options: JobRunOptions) {
    const limit = (v: number | undefined, d: number, min: number, max: number) => { const n = v ?? d; if (!Number.isSafeInteger(n) || n < min || n > max)
        throw new JobError('limit'); return n; };
    return { requests: limit(options.maxRequests, 256, 1, 10000), pages: limit(options.maxPages, 100, 1, 1000), items: limit(options.maxItems, 1000, 1, 10000), polls: limit(options.maxPolls, 100, 1, 10000), delay: limit(options.pollDelayMs, 1000, 0, 60000) };
}
async function boundedBody(response: Response, signal: AbortSignal, maximum: number): Promise<Uint8Array> {
    const reader = response.body?.getReader();
    if (!reader)
        return new Uint8Array();
    let size = 0;
    const chunks: Uint8Array[] = [];
    const cancel = () => { void reader.cancel().catch(() => { }); };
    signal.addEventListener('abort', cancel, { once: true });
    try {
        for (;;) {
            signal.throwIfAborted();
            const { done, value } = await reader.read();
            signal.throwIfAborted();
            if (done)
                break;
            size += value.length;
            if (size > maximum)
                throw new JobError('limit');
            chunks.push(value);
        }
        return Buffer.concat(chunks);
    }
    finally {
        signal.removeEventListener('abort', cancel);
        void reader.cancel().catch(() => { });
    }
}
class Operation {
    count = 0;
    readonly caps: ReturnType<typeof limits>;
    constructor(readonly config: ClientConfig, readonly options: JobRunOptions, readonly signal: AbortSignal) { this.caps = limits(options); }
    async request(path: string, method: 'GET' | 'POST' = 'GET', body?: unknown, idempotencyKey?: string): Promise<Record<string, any>> {
        this.signal.throwIfAborted();
        if (this.count++ >= this.caps.requests)
            throw new JobError('limit');
        const requestFetch = this.config.requestFetch;
        const owned = { ...this.config, requestFetch: (opts: Parameters<ClientConfig['requestFetch']>[0]) => { const fetch = requestFetch(opts); return async (url: Parameters<typeof fetch>[0], init?: Parameters<typeof fetch>[1]) => { const response = await fetch(url, { ...init, redirect: 'error' }); const bytes = await boundedBody(response, this.signal, JOB_MAX_BYTES); return new Response(bytes, { status: response.status, headers: response.headers }); }; } };
        try {
            const value: unknown = await scopedRequest(owned, { method, url: path, ...(body === undefined ? {} : { body, mediaType: 'application/json' }), ...(method === 'POST' ? { headers: { 'Idempotency-Key': idempotencyKey ?? randomUUID() } } : {}) }, { signal: this.signal, timeoutMs: 0, retry: false });
            this.signal.throwIfAborted();
            if (!value || typeof value !== 'object' || Array.isArray(value))
                throw new JobError('invalid_response');
            return value;
        }
        catch (e) {
            this.signal.throwIfAborted();
            if (e instanceof JobError)
                throw method === 'POST' ? new JobError('acceptance_unknown') : e;
            const status = (e as {
                status?: unknown;
            })?.status;
            throw new JobError(method === 'POST' && (!Number.isInteger(status) || Number(status) >= 500) ? 'acceptance_unknown' : 'request_failed', Number.isInteger(status) ? Number(status) : undefined);
        }
    }
}
const path = (name: string, id?: string) => `/v1/jobs/${encodeURIComponent(name)}${id ? `/runs/${id}` : ''}`;
function progress(run: Record<string, any>, c: JobCheckpoint): JobProgress {
    if (run.id !== c.run_id || run.job_id !== c.job_id || run.account_id !== c.account_id || run.input_digest !== c.input_digest || run.input_manifest_version !== 1 || run.tasks !== c.inputs.length || (run.source_run_id ?? null) !== c.source_run_id || !['queued', 'running', ...terminal].includes(run.aggregate_status) || !['tasks_succeeded', 'tasks_failed', 'tasks_cancelled', 'tasks_running'].every(k => nonnegative(run[k])) || run.tasks_succeeded + run.tasks_failed + run.tasks_cancelled + run.tasks_running > run.tasks)
        throw new JobError('invalid_response');
    if (terminal.has(run.aggregate_status) && (run.tasks_running !== 0 || run.tasks_succeeded + run.tasks_failed + run.tasks_cancelled !== run.tasks) || run.aggregate_status === 'succeeded' && run.tasks_succeeded !== run.tasks)
        throw new JobError('invalid_response');
    return jobSnapshot({ runId: run.id, status: run.aggregate_status, total: run.tasks, succeeded: run.tasks_succeeded, failed: run.tasks_failed, cancelled: run.tasks_cancelled, running: run.tasks_running });
}
async function taskList(operation: Operation, c: JobCheckpoint, contract: JobContract<unknown, unknown>): Promise<Record<string, any>[]> {
    let offset = 0;
    const tasks: Record<string, any>[] = [];
    const seen = new Set<number>();
    for (let page = 0; page < operation.caps.pages; page++) {
        const data = await operation.request(path(c.job_name, c.run_id) + `/tasks?limit=200&offset=${offset}`);
        if (!Array.isArray(data.tasks) || data.tasks.length > 200 || data.offset !== offset || data.total !== c.inputs.length || !Number.isSafeInteger(data.next_offset))
            throw new JobError('invalid_response');
        for (const task of data.tasks) {
            const expected = c.inputs[task?.task_index];
            if (!task || task.run_id !== c.run_id || !nonnegative(task.task_index) || seen.has(task.task_index) || !expected || task.input_id !== expected.input_id || typeof task.input_ref !== 'string' || jobHash(Buffer.from(task.input_ref)) !== expected.binding_sha256 || (task.source_task_index ?? null) !== expected.source_task_index || !['queued', 'claimed', 'succeeded', ...failed].includes(task.status) || !nonnegative(task.attempt))
                throw new JobError('invalid_response');
            inspectJobInput(contract, { input_id: task.input_id, input_ref: task.input_ref });
            seen.add(task.task_index);
            tasks.push(task);
            if (tasks.length > operation.caps.items)
                throw new JobError('limit');
        }
        if (data.next_offset === -1) {
            if (tasks.length !== c.inputs.length)
                throw new JobError('invalid_response');
            return tasks.sort((a, b) => a.task_index - b.task_index);
        }
        if (data.next_offset !== offset + data.tasks.length || data.next_offset <= offset || data.next_offset >= c.inputs.length)
            throw new JobError('invalid_response');
        offset = data.next_offset;
    }
    throw new JobError('limit');
}
export function bindTypedJobs(getConfig: () => ClientConfig): TypedJobs {
    return { defineJob<Input, Output>(contract: JobContract<Input, Output>, settings: {
            readonly store?: JobStore;
            readonly artifactFetch?: typeof fetch;
        } = {}) {
            if (!(contract instanceof JobContract))
                throw new JobError('invalid_contract');
            const store = settings.store, artifactFetch = settings.artifactFetch ?? nativeFetch;
            const make = (receipt: JobCheckpoint): TypedJobRun<Output> => {
                const c = parseJobCheckpoint(receipt);
                if (c.job_name !== contract.spec.name || c.contract_sha256 !== contract.sha256)
                    throw new JobError('invalid_receipt');
                const invoke = <T>(options: JobRunOptions, work: (op: Operation) => Promise<T>): Promise<T> => { const config = getConfig(); if (endpoint(config.BASE) !== c.base_url)
                    throw new JobError('invalid_receipt'); return jobScope(options.timeoutMs, options.signal, signal => work(new Operation(config, { ...options }, signal))); };
                const status = (op: Operation) => op.request(path(c.job_name, c.run_id)).then(r => progress(r, c));
                return Object.freeze({ checkpoint: c,
                    status(options: JobRunOptions) { return invoke(options, status); },
                    wait(options: JobRunOptions) {
                        const callback = options.onProgress;
                        return invoke(options, async (op) => {
                            for (let poll = 0; poll < op.caps.polls; poll++) {
                                const state = await status(op);
                                if (callback) {
                                    try {
                                        await callback(state);
                                    }
                                    catch {
                                        op.signal.throwIfAborted();
                                        throw new JobError('progress_failed');
                                    }
                                }
                                op.signal.throwIfAborted();
                                if (terminal.has(state.status))
                                    return state;
                                if (poll + 1 < op.caps.polls)
                                    await new Promise<void>(resolve => { const timer = setTimeout(done, op.caps.delay); function done() { clearTimeout(timer); op.signal.removeEventListener('abort', done); resolve(); } op.signal.addEventListener('abort', done, { once: true }); });
                            }
                            throw new JobError('poll_limit');
                        });
                    },
                    cancel(options: JobRunOptions & {
                        readonly idempotencyKey?: string;
                    }) { const idem = key(options.idempotencyKey ?? randomUUID()); return invoke(options, async (op) => { await status(op); const data = await op.request(path(c.job_name, c.run_id) + '/cancel', 'POST', undefined, idem); try {
                        return progress(data.run, c);
                    }
                    catch {
                        throw new JobError('acceptance_unknown');
                    } }); },
                    results(options: JobRunOptions) {
                        return invoke(options, async (op) => {
                            const state = await status(op);
                            if (!terminal.has(state.status))
                                throw new JobError('not_terminal');
                            const tasks = await taskList(op, c, contract), outcomes: JobItemOutcome<Output>[] = [];
                            for (const t of tasks) {
                                op.signal.throwIfAborted();
                                if (t.status !== 'succeeded') {
                                    outcomes.push({ inputId: t.input_id, index: t.task_index, attempt: t.attempt, outcome: t.status === 'cancelled' ? 'cancelled' : failed.has(t.status) ? 'failed' : 'pending', ...(typeof t.outcome_code === 'string' && /^[a-z0-9_.-]{1,64}$/.test(t.outcome_code) ? { code: t.outcome_code } : {}) });
                                    continue;
                                }
                                try {
                                    const manifest = t.output_manifest;
                                    if (manifest?.version !== 1 || !Array.isArray(manifest.artifacts))
                                        throw new JobError('invalid_output');
                                    const candidates = manifest.artifacts.filter((a: JobArtifact) => a?.name === 'result');
                                    if (candidates.length !== 1)
                                        throw new JobError('invalid_output');
                                    const a = candidates[0] as JobArtifact, sha = a.sha256?.replace(/^sha256:/, '');
                                    if (!jobURI(a.uri) || !hash(sha) || !nonnegative(a.size_bytes) || a.size_bytes > JOB_MAX_BYTES)
                                        throw new JobError('invalid_output');
                                    let bytes: Uint8Array;
                                    if (a.uri.startsWith('obj://')) {
                                        const signed = await op.request(path(c.job_name, c.run_id) + `/tasks/${t.task_index}/artifacts/result/download`);
                                        if (signed.name !== 'result' || signed.size_bytes !== a.size_bytes || signed.sha256 !== a.sha256 || signed.download?.method !== 'GET' || typeof signed.download.url !== 'string' || Date.parse(signed.download.expires_at) <= Date.now() || !Number.isFinite(Date.parse(signed.download.expires_at)))
                                            throw new JobError('invalid_output');
                                        const u = new URL(signed.download.url);
                                        if (!['https:', 'http:'].includes(u.protocol) || u.username || u.password || u.hash)
                                            throw new JobError('invalid_output');
                                        const headers = new Headers(signed.download.headers);
                                        if (headers.has('cookie') || headers.has('proxy-authorization'))
                                            throw new JobError('invalid_output');
                                        const response = await artifactFetch(u, { method: 'GET', headers, signal: op.signal, redirect: 'error', credentials: 'omit' });
                                        if (!response.ok) {
                                            void response.body?.cancel().catch(() => { });
                                            throw new JobError('request_failed');
                                        }
                                        bytes = await boundedBody(response, op.signal, Math.min(JOB_MAX_BYTES, a.size_bytes));
                                    }
                                    else {
                                        if (!store)
                                            throw new JobError('store_required');
                                        bytes = await store.get(a.uri, { inputId: t.input_id, attempt: t.attempt, signal: op.signal });
                                    }
                                    op.signal.throwIfAborted();
                                    if (bytes.length !== a.size_bytes || jobHash(bytes) !== sha)
                                        throw new JobError('integrity_failed');
                                    const result = decodeJobOutput(contract, t.input_id, bytes);
                                    outcomes.push({ inputId: t.input_id, index: t.task_index, attempt: t.attempt, outcome: 'succeeded', result });
                                }
                                catch (e) {
                                    op.signal.throwIfAborted();
                                    outcomes.push({ inputId: t.input_id, index: t.task_index, attempt: t.attempt, outcome: 'invalid_result', code: e instanceof JobError ? e.code : 'invalid_output' });
                                }
                            }
                            return jobSnapshot(outcomes);
                        });
                    },
                    retryFailed(options: JobRunOptions & {
                        readonly idempotencyKey: string;
                    }) { const idem = key(options.idempotencyKey); return invoke(options, async (op) => { const state = await status(op); if (!terminal.has(state.status))
                        throw new JobError('not_terminal'); const tasks = (await taskList(op, c, contract)).filter(t => failed.has(t.status)); if (!tasks.length)
                        throw new JobError('no_failed_inputs'); const refs = tasks.map(t => ({ input_id: t.input_id, input_ref: t.input_ref })); const run = await op.request(path(c.job_name, c.run_id) + '/replay-failed', 'POST', undefined, idem); try {
                        const checkpoint = parseJobCheckpoint({ ...c, run_id: run.id, source_run_id: c.run_id, input_digest: jobHash(Buffer.from(JSON.stringify(refs))), inputs: tasks.map(t => ({ input_id: t.input_id, binding_sha256: jobHash(Buffer.from(t.input_ref)), source_task_index: t.task_index })) });
                        progress(run, checkpoint);
                        return make(checkpoint);
                    }
                    catch {
                        throw new JobError('acceptance_unknown');
                    } }); },
                });
            };
            return Object.freeze({ resume: make,
                resumeById(runId:string,options:JobRunOptions) {
                    if(!uuid(runId))throw new JobError('invalid_receipt');const config=getConfig();
                    return jobScope(options.timeoutMs,options.signal,async signal=>{
                        const op=new Operation(config,{...options},signal),job=await op.request(path(contract.spec.name));
                        if(!uuid(job.id)||!uuid(job.account_id)||job.name!==contract.spec.name)throw new JobError('invalid_response');
                        const run=await op.request(path(contract.spec.name,runId));
                        if(run.id!==runId||run.job_id!==job.id||run.account_id!==job.account_id||!positive(run.tasks)||!hash(run.input_digest)||run.input_manifest_version!==1)throw new JobError('invalid_response');
                        if(run.tasks>op.caps.items)throw new JobError('limit');
                        let offset=0;const tasks:Record<string,any>[]=[],seen=new Set<number>();
                        for(let page=0;page<op.caps.pages;page++){
                            const data=await op.request(path(contract.spec.name,runId)+`/tasks?limit=200&offset=${offset}`);
                            if(!Array.isArray(data.tasks)||data.tasks.length>200||data.offset!==offset||data.total!==run.tasks||!Number.isSafeInteger(data.next_offset))throw new JobError('invalid_response');
                            for(const task of data.tasks){if(!task||task.run_id!==runId||!nonnegative(task.task_index)||task.task_index>=run.tasks||seen.has(task.task_index))throw new JobError('invalid_response');
                                try{inspectJobInput(contract,{input_id:task.input_id,input_ref:task.input_ref});}catch{throw new JobError('invalid_response');}seen.add(task.task_index);tasks.push(task);}
                            if(data.next_offset===-1){if(tasks.length!==run.tasks)throw new JobError('invalid_response');break;}
                            if(data.next_offset!==offset+data.tasks.length||data.next_offset<=offset||data.next_offset>=run.tasks)throw new JobError('invalid_response');offset=data.next_offset;
                        }
                        if(tasks.length!==run.tasks)throw new JobError('limit');tasks.sort((a,b)=>a.task_index-b.task_index);
                        const refs=tasks.map(t=>({input_id:t.input_id,input_ref:t.input_ref}));
                        if(jobHash(Buffer.from(JSON.stringify(refs)))!==run.input_digest)throw new JobError('invalid_response');
                        try{const checkpoint=parseJobCheckpoint({version:1,base_url:endpoint(config.BASE),job_name:contract.spec.name,job_id:job.id,account_id:job.account_id,run_id:runId,contract_sha256:contract.sha256,input_digest:run.input_digest,source_run_id:run.source_run_id??null,inputs:tasks.map(t=>({input_id:t.input_id,binding_sha256:jobHash(Buffer.from(t.input_ref)),source_task_index:t.source_task_index??null}))});progress(run,checkpoint);return make(checkpoint);}catch{throw new JobError('invalid_response');}
                    });
                }, submit(inputs: readonly JobInput<Input>[], options: JobSubmitOptions) {
                    const caps=limits(options);if(!Array.isArray(inputs)||!inputs.length||inputs.length>caps.items)throw new JobError('invalid_input');
                    const config = getConfig(), idem = key(options.idempotencyKey), captured = inputs.map(i => ({ id: i.id, value: contract.input(i.value) }));
                    if(options.expectedAccountId!==undefined&&!uuid(options.expectedAccountId))throw new JobError('invalid_input');
                    if(options.expectedBaseURL!==undefined&&endpoint(config.BASE)!==endpoint(options.expectedBaseURL))throw new JobError('invalid_receipt');
                    if(options.expectedContractSha256!==undefined&&!hash(options.expectedContractSha256))throw new JobError('invalid_input');
                    if(options.expectedContractSha256!==undefined&&options.expectedContractSha256!==contract.sha256)throw new JobError('invalid_receipt');
                    if (!captured.length || captured.length > 10000 || new Set(captured.map(i => i.id)).size !== captured.length || captured.some(i => !jobInputId(i.id)))
                        throw new JobError('invalid_input');
                    for (const n of [options.parallelism, options.retryMax])
                        if (n !== undefined && !nonnegative(n))
                            throw new JobError('invalid_input');
                    if (options.parallelism === 0 || options.failurePolicy !== undefined && !['continue', 'fail_fast'].includes(options.failurePolicy))
                        throw new JobError('invalid_input');
                    const settings = { ...options };
                    return jobScope(options.timeoutMs, options.signal, async (signal) => {
                        const op = new Operation(config, settings, signal);
                        if (captured.length > op.caps.items)
                            throw new JobError('limit');
                        const job = await op.request(path(contract.spec.name));
                        if (!uuid(job.id) || !uuid(job.account_id) || job.name !== contract.spec.name || settings.expectedAccountId!==undefined&&job.account_id!==settings.expectedAccountId)
                            throw new JobError('invalid_response');
                        const refs: JobInputBinding[] = [];
                        for (const input of captured) {
                            signal.throwIfAborted();
                            refs.push(await prepareJobInput(contract, input, { inputId: input.id, attempt: 0, signal }, store));
                        }
                        const run = await op.request(path(contract.spec.name) + '/runs', 'POST', { inputs: refs, ...(settings.parallelism === undefined ? {} : { parallelism: settings.parallelism }), ...(settings.retryMax === undefined ? {} : { retry_max: settings.retryMax }), ...(settings.failurePolicy === undefined ? {} : { failure_policy: settings.failurePolicy }) }, idem);
                        try {
                            const checkpoint = parseJobCheckpoint({ version: 1, base_url: endpoint(config.BASE), job_name: contract.spec.name, job_id: job.id, account_id: job.account_id, run_id: run.id, contract_sha256: contract.sha256, input_digest: jobHash(Buffer.from(JSON.stringify(refs))), source_run_id: null, inputs: refs.map(r => ({ input_id: r.input_id, binding_sha256: jobHash(Buffer.from(r.input_ref)), source_task_index: null })) });
                            progress(run, checkpoint);
                            return make(checkpoint);
                        }
                        catch {
                            throw new JobError('acceptance_unknown');
                        }
                    });
                } });
        } };
}
