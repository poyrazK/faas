/** Portable typed job contracts and task execution. No control-plane credentials. */
import { createHash, randomUUID } from 'node:crypto';
import { mkdir, readFile, writeFile, rename, unlink } from 'node:fs/promises';
import { join, dirname } from 'node:path';
export type JobCode = 'invalid_contract' | 'invalid_input' | 'invalid_output' | 'invalid_reference' | 'integrity_failed' | 'store_required' | 'handler_failed' | 'deadline' | 'cancelled' | 'invalid_receipt' | 'invalid_response' | 'request_failed' | 'acceptance_unknown' | 'limit' | 'poll_limit' | 'progress_failed' | 'not_terminal' | 'no_failed_inputs';
export class JobError extends Error {
    constructor(readonly code: JobCode, readonly status?: number) { super(`SDK job stopped: ${code}`); this.name = 'JobError'; }
}
export interface JobSchema {
    readonly type: 'object' | 'array' | 'string' | 'integer' | 'number' | 'boolean' | 'null';
    readonly properties?: Readonly<Record<string, JobSchema>>;
    readonly required?: readonly string[];
    readonly additionalProperties?: false;
    readonly items?: JobSchema;
    readonly enum?: readonly (string | number | boolean | null)[];
    readonly minLength?: number;
    readonly maxLength?: number;
    readonly minItems?: number;
    readonly maxItems?: number;
    readonly minimum?: number;
    readonly maximum?: number;
}
export interface JobContractSpec {
    readonly version: 1;
    readonly name: string;
    readonly input: JobSchema;
    readonly output: JobSchema;
}
export interface JobContext {
    readonly inputId: string;
    readonly attempt: number;
    readonly signal: AbortSignal;
}
export interface JobStore {
    /** Store immutable bytes; return a credential-free obj://, s3:// or gs:// URI. */
    put(bytes: Uint8Array, sha256: string, context: JobContext): Promise<string>;
    get(uri: string, context: JobContext): Promise<Uint8Array>;
}
export interface JobInput<Input> {
    readonly id: string;
    readonly value: Input;
}
export interface JobInputBinding {
    readonly input_id: string;
    readonly input_ref: string;
}
export interface JobArtifact {
    readonly name: string;
    readonly uri: string;
    readonly size_bytes: number;
    readonly sha256: string;
}
export interface LocalJobOutcome<Output> {
    readonly inputId: string;
    readonly outcome: 'succeeded' | 'failed';
    readonly attempts: number;
    readonly result?: Output;
    readonly code?: JobCode;
}
export const JOB_MAX_BYTES = 1024 * 1024;
export const jobHash = (data: Uint8Array): string => createHash('sha256').update(data).digest('hex');
export const jobInputId = (id: unknown): id is string => typeof id === 'string' && /^[A-Za-z0-9_.:-]{1,128}$/.test(id);
export function jobSnapshot<T>(value: T): T {
    const copy: T = structuredClone(value);
    function freeze(v: unknown): void { if (v && typeof v === 'object') {
        for (const c of Object.values(v))
            freeze(c);
        Object.freeze(v);
    } }
    freeze(copy);
    return copy;
}
function stable(v: unknown): string {
    if (typeof v === 'number') {
        const b = Buffer.alloc(8);
        b.writeDoubleBE(v === 0 ? 0 : v);
        return 'n' + b.toString('hex');
    }
    if (Array.isArray(v))
        return '[' + v.map(stable).join(',') + ']';
    if (v && typeof v === 'object')
        return '{' + Object.keys(v).sort().map(k => JSON.stringify(k) + ':' + stable((v as Record<string, unknown>)[k])).join(',') + '}';
    return JSON.stringify(v);
}
/** @internal Canonical JSON value hash shared by durable SDK receipts. Validate values first. */
export const jobValueHash = (value: unknown): string => jobHash(Buffer.from(stable(value)));
function schemaCheck(s: JobSchema, depth = 0): void {
    if (!s || depth > 12 || !['object', 'array', 'string', 'integer', 'number', 'boolean', 'null'].includes(s.type))
        throw new JobError('invalid_contract');
    const fields = ['type', 'enum', ...(s.type === 'object' ? ['properties', 'required', 'additionalProperties'] : s.type === 'array' ? ['items', 'minItems', 'maxItems'] : s.type === 'string' ? ['minLength', 'maxLength'] : ['integer', 'number'].includes(s.type) ? ['minimum', 'maximum'] : [])];
    if (Object.keys(s).some(k => !fields.includes(k)))
        throw new JobError('invalid_contract');
    if (s.type === 'object') {
        if (!s.properties || typeof s.properties !== 'object' || Array.isArray(s.properties) || Object.keys(s.properties).length > 128 || s.additionalProperties !== false || !Array.isArray(s.required) || new Set(s.required).size !== s.required.length || s.required.some(k => !Object.hasOwn(s.properties!, k)))
            throw new JobError('invalid_contract');
        for (const [name, child] of Object.entries(s.properties)) {
            if (!/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(name) || ['constructor', 'prototype', '__proto__'].includes(name))
                throw new JobError('invalid_contract');
            schemaCheck(child, depth + 1);
        }
    }
    if (s.type === 'array')
        schemaCheck(s.items!, depth + 1);
    for (const [min, max] of [['minLength', 'maxLength'], ['minItems', 'maxItems'], ['minimum', 'maximum']] as const) {
        for (const key of [min, max])
            if (s[key] !== undefined && (!Number.isFinite(s[key]) || !['minimum', 'maximum'].includes(key) && (!Number.isSafeInteger(s[key]) || s[key]! < 0)))
                throw new JobError('invalid_contract');
        if (s[min] !== undefined && s[max] !== undefined && s[min]! > s[max]!)
            throw new JobError('invalid_contract');
    }
    if (s.enum !== undefined && (!Array.isArray(s.enum) || !s.enum.length || s.enum.length > 128 || s.type === 'object' || s.type === 'array'))
        throw new JobError('invalid_contract');
    if (s.enum)
        for (const v of s.enum) {
            if (typeof v === 'number' && !Number.isSafeInteger(v))
                throw new JobError('invalid_contract');
            const { enum: _, ...without } = s;
            validate(v, without as JobSchema, 'invalid_contract');
        }
}
function validate(v: unknown, s: JobSchema, code: JobCode): void {
    const bad = () => { throw new JobError(code); };
    if (s.type === 'object') {
        if (!v || typeof v !== 'object' || Array.isArray(v) || Object.getPrototypeOf(v) !== Object.prototype && Object.getPrototypeOf(v) !== null)
            bad();
        const value = v as Record<string, unknown>;
        if (s.required!.some(k => !Object.hasOwn(value, k)) || Object.keys(value).some(k => !Object.hasOwn(s.properties!, k)))
            bad();
        for (const [k, child] of Object.entries(s.properties!))
            if (Object.hasOwn(value, k))
                validate(value[k], child, code);
    }
    else if (s.type === 'array') {
        if (!Array.isArray(v))
            bad();
        const a = v as unknown[];
        if (a.length < (s.minItems ?? 0) || a.length > (s.maxItems ?? JOB_MAX_BYTES))
            bad();
        for (const child of a)
            validate(child, s.items!, code);
    }
    else if (s.type === 'string') {
        if (typeof v !== 'string' || [...v].length < (s.minLength ?? 0) || [...v].length > (s.maxLength ?? JOB_MAX_BYTES) || Buffer.from(v).toString() !== v)
            bad();
    }
    else if (s.type === 'number' || s.type === 'integer') {
        if (typeof v !== 'number' || !Number.isFinite(v) || s.type === 'integer' && !Number.isSafeInteger(v) || v < (s.minimum ?? -Infinity) || v > (s.maximum ?? Infinity))
            bad();
    }
    else if (s.type === 'boolean' ? typeof v !== 'boolean' : v !== null)
        bad();
    if (s.enum && !s.enum.some(e => e === v))
        bad();
}
export function jobBytes(value: unknown): Uint8Array {
    let data: Buffer;
    try {
        data = Buffer.from(JSON.stringify(value));
    }
    catch {
        throw new JobError('invalid_input');
    }
    if (data.length > JOB_MAX_BYTES)
        throw new JobError('limit');
    return data;
}
export function jobURI(uri: unknown): uri is string {
    if (typeof uri !== 'string' || Buffer.byteLength(uri) > 1024)
        return false;
    try {
        const u = new URL(uri);
        return ['obj:', 's3:', 'gs:'].includes(u.protocol) && !!u.hostname && u.pathname.length > 1 && !u.username && !u.password && !u.search && !u.hash;
    }
    catch {
        return false;
    }
}
/** One deadline also races cancellation-ignoring user code. */
export async function jobScope<T>(timeoutMs: number, signal: AbortSignal | undefined, work: (signal: AbortSignal) => Promise<T>): Promise<T> {
    if (!Number.isSafeInteger(timeoutMs) || timeoutMs <= 0 || timeoutMs > 2147483647)
        throw new TypeError('A positive bounded timeoutMs is required');
    const controller = new AbortController();
    const cancel = () => controller.abort(signal?.reason);
    if (signal?.aborted)
        cancel();
    else
        signal?.addEventListener('abort', cancel, { once: true });
    const timer = setTimeout(() => controller.abort(new JobError('deadline')), timeoutMs);
    const check = () => { if (controller.signal.aborted)
        throw controller.signal.reason; };
    try {
        check();
        return await new Promise<T>((resolve, reject) => {
            const stop = () => reject(controller.signal.reason);
            controller.signal.addEventListener('abort', stop, { once: true });
            Promise.resolve().then(() => { check(); return work(controller.signal); }).then(v => { controller.signal.removeEventListener('abort', stop); try {
                check();
                resolve(v);
            }
            catch (e) {
                reject(e);
            } }, e => { controller.signal.removeEventListener('abort', stop); reject(e); });
        });
    }
    finally {
        clearTimeout(timer);
        signal?.removeEventListener('abort', cancel);
        controller.abort(new JobError('cancelled'));
    }
}
export class JobContract<Input, Output> {
    readonly spec: JobContractSpec;
    readonly sha256: string;
    constructor(spec: JobContractSpec) {
        try {
            if (spec.version !== 1 || Object.keys(spec).sort().join() !== 'input,name,output,version' || !/^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/.test(spec.name))
                throw new Error();
            jobBytes(spec);
            schemaCheck(spec.input);
            schemaCheck(spec.output);
            this.spec = jobSnapshot(spec);
            this.sha256 = jobHash(Buffer.from(stable(spec)));
        }
        catch {
            throw new JobError('invalid_contract');
        }
        Object.freeze(this);
    }
    input(value: unknown): Input { validate(value, this.spec.input, 'invalid_input'); jobBytes(value); return jobSnapshot(value) as Input; }
    output(value: unknown): Output { validate(value, this.spec.output, 'invalid_output'); jobBytes(value); return jobSnapshot(value) as Output; }
}
interface Envelope {
    version: 1;
    contract: string;
    input_id: string;
    data?: unknown;
    ref?: string;
    sha256?: string;
    size?: number;
}
export async function prepareJobInput<Input, Output>(contract: JobContract<Input, Output>, input: JobInput<Input>, context: JobContext, store?: JobStore): Promise<JobInputBinding> {
    if (!jobInputId(input.id))
        throw new JobError('invalid_input');
    const data = jobBytes({ version: 1, contract: contract.sha256, input_id: input.id, data: contract.input(input.value) });
    let encoded = 'gregale-job:v1:' + Buffer.from(data).toString('base64url');
    if (Buffer.byteLength(encoded) > 2048) {
        if (!store)
            throw new JobError('store_required');
        context.signal.throwIfAborted();
        const digest = jobHash(data), ref = await store.put(data, digest, context);
        context.signal.throwIfAborted();
        if (!jobURI(ref))
            throw new JobError('invalid_reference');
        encoded = 'gregale-job:v1:' + Buffer.from(jobBytes({ version: 1, contract: contract.sha256, input_id: input.id, ref, sha256: digest, size: data.length })).toString('base64url');
    }
    if (Buffer.byteLength(encoded) > 2048)
        throw new JobError('limit');
    return { input_id: input.id, input_ref: encoded };
}
export function inspectJobInput(contract: JobContract<unknown, unknown>, binding: JobInputBinding): Envelope {
    try {
        if (!jobInputId(binding.input_id) || typeof binding.input_ref !== 'string' || binding.input_ref.length > 2048 || !/^gregale-job:v1:[A-Za-z0-9_-]+$/.test(binding.input_ref))
            throw new Error();
        const raw = Buffer.from(binding.input_ref.slice('gregale-job:v1:'.length), 'base64url');
        if ('gregale-job:v1:' + raw.toString('base64url') !== binding.input_ref)
            throw new Error();
        const e = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(raw)) as Envelope;
        if (e.version !== 1 || e.contract !== contract.sha256 || e.input_id !== binding.input_id)
            throw new Error();
        if (Object.hasOwn(e, 'data')) {
            if (Object.keys(e).sort().join() !== 'contract,data,input_id,version')
                throw new Error();
            contract.input(e.data);
        }
        else if (Object.keys(e).sort().join() !== 'contract,input_id,ref,sha256,size,version' || !jobURI(e.ref) || !/^[a-f0-9]{64}$/.test(e.sha256!) || !Number.isSafeInteger(e.size) || e.size! < 1 || e.size! > JOB_MAX_BYTES)
            throw new Error();
        return e;
    }
    catch {
        throw new JobError('invalid_reference');
    }
}
export async function readJobInput<Input, Output>(contract: JobContract<Input, Output>, binding: JobInputBinding, context: JobContext, store?: JobStore): Promise<Input> {
    const e = inspectJobInput(contract, binding);
    if (Object.hasOwn(e, 'data'))
        return contract.input(e.data);
    if (!store)
        throw new JobError('store_required');
    context.signal.throwIfAborted();
    const raw = await store.get(e.ref!, context);
    context.signal.throwIfAborted();
    if (raw.byteLength !== e.size || jobHash(raw) !== e.sha256)
        throw new JobError('integrity_failed');
    let nested: Envelope;
    try {
        nested = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(raw)) as Envelope;
    }
    catch {
        throw new JobError('invalid_input');
    }
    if (Object.keys(nested).sort().join() !== 'contract,data,input_id,version' || nested.contract !== contract.sha256 || nested.input_id !== binding.input_id || nested.version !== 1)
        throw new JobError('invalid_input');
    return contract.input(nested.data);
}
export function decodeJobOutput<Input, Output>(contract: JobContract<Input, Output>, inputId: string, raw: Uint8Array): Output {
    if (raw.byteLength > JOB_MAX_BYTES)
        throw new JobError('limit');
    try {
        const e = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(raw)) as Envelope;
        if (Object.keys(e).sort().join() !== 'contract,data,input_id,version' || e.version !== 1 || e.contract !== contract.sha256 || e.input_id !== inputId)
            throw new Error();
        return contract.output(e.data);
    }
    catch {
        throw new JobError('invalid_output');
    }
}
export type JobHandler<Input, Output> = (input: Input, context: JobContext) => Output | Promise<Output>;
export async function runLocalJobBatch<Input, Output>(contract: JobContract<Input, Output>, inputs: readonly JobInput<Input>[], handler: JobHandler<Input, Output>, options: {
    timeoutMs: number;
    signal?: AbortSignal;
    concurrency?: number;
    maxAttempts?: number;
}): Promise<readonly LocalJobOutcome<Output>[]> {
    const concurrency = options.concurrency ?? 1, attempts = options.maxAttempts ?? 1;
    if (!Number.isInteger(concurrency) || concurrency < 1 || concurrency > 64 || !Number.isInteger(attempts) || attempts < 1 || attempts > 10 || !inputs.length || inputs.length > 10000 || new Set(inputs.map(i => i.id)).size !== inputs.length || inputs.some(i => !jobInputId(i.id)))
        throw new JobError('invalid_input');
    const captured = inputs.map(i => ({ id: i.id, value: contract.input(i.value) }));
    return jobScope(options.timeoutMs, options.signal, async (signal) => {
        let next = 0;
        const outcomes: LocalJobOutcome<Output>[] = [];
        await Promise.all(Array.from({ length: Math.min(concurrency, captured.length) }, async () => {
            for (;;) {
                signal.throwIfAborted();
                const index = next++, input = captured[index];
                if (!input)
                    return;
                for (let attempt = 1; attempt <= attempts; attempt++) {
                    try {
                        const result = contract.output(await handler(input.value, { inputId: input.id, attempt, signal }));
                        signal.throwIfAborted();
                        outcomes[index] = { inputId: input.id, outcome: 'succeeded', attempts: attempt, result };
                        break;
                    }
                    catch (e) {
                        signal.throwIfAborted();
                        if (attempt === attempts)
                            outcomes[index] = { inputId: input.id, outcome: 'failed', attempts: attempt, code: e instanceof JobError ? e.code : 'handler_failed' };
                    }
                }
            }
        }));
        return jobSnapshot(outcomes);
    });
}
async function atomic(path: string, bytes: Uint8Array, signal: AbortSignal): Promise<void> { const temporary = join(dirname(path), '.gregale-job-' + randomUUID()); try {
    signal.throwIfAborted();
    await writeFile(temporary, bytes, { mode: 0o600, flag: 'wx' });
    signal.throwIfAborted();
    await rename(temporary, path);
}
finally {
    await unlink(temporary).catch(() => { });
} }
/** The supervisor publishes success only after the process exits successfully. */
export async function runJobTask<Input, Output>(contract: JobContract<Input, Output>, handler: JobHandler<Input, Output>, options: {
    timeoutMs: number;
    store: JobStore;
    signal?: AbortSignal;
    env?: Readonly<NodeJS.ProcessEnv>;
    manageSignals?: boolean;
}): Promise<Output> {
    const env = { ...(options.env ?? process.env) }, id = env.GREGALE_INPUT_ID, ref = env.GREGALE_INPUT_REF, path = env.GREGALE_OUTPUT_MANIFEST_PATH, attempt = Number(env.GREGALE_TASK_ATTEMPT);
    if (!jobInputId(id) || !ref || !path || !Number.isSafeInteger(attempt) || attempt < 1 || !env.GREGALE_RUN_ID || !/^\d+$/.test(env.GREGALE_TASK_INDEX ?? '') || !/^\d+$/.test(env.GREGALE_TASK_COUNT ?? '') || Number(env.GREGALE_TASK_INDEX) >= Number(env.GREGALE_TASK_COUNT))
        throw new JobError('invalid_input');
    const stopped = new AbortController(), stop = () => stopped.abort(new JobError('cancelled'));
    if (options.manageSignals !== false)
        process.once('SIGTERM', stop);
    try {
        return await jobScope(options.timeoutMs, options.signal ? AbortSignal.any([options.signal, stopped.signal]) : stopped.signal, async (signal) => {
            const context = { inputId: id, attempt, signal };
            const input = await readJobInput(contract, { input_id: id, input_ref: ref }, context, options.store);
            signal.throwIfAborted();
            let result: Output;
            try {
                result = contract.output(await handler(input, context));
            }
            catch (e) {
                signal.throwIfAborted();
                throw e instanceof JobError ? e : new JobError('handler_failed');
            }
            signal.throwIfAborted();
            const bytes = jobBytes({ version: 1, contract: contract.sha256, input_id: id, data: result }), sha = jobHash(bytes), uri = await options.store.put(bytes, sha, context);
            signal.throwIfAborted();
            if (!jobURI(uri))
                throw new JobError('invalid_reference');
            const manifest = jobBytes({ version: 1, artifacts: [{ name: 'result', uri, size_bytes: bytes.length, sha256: 'sha256:' + sha }], outcome_code: 'typed_job_succeeded' });
            if (manifest.length > 4096)
                throw new JobError('limit');
            await atomic(path, manifest, signal);
            signal.throwIfAborted();
            return result;
        });
    }
    finally {
        if (options.manageSignals !== false)
            process.removeListener('SIGTERM', stop);
    }
}
/** Local tests only: maps s3://gregale-local/<digest> references to private files. */
export function localJobStore(directory: string): JobStore {
    return {
        async put(bytes, sha, context) { context.signal.throwIfAborted(); if (jobHash(bytes) !== sha || bytes.length > JOB_MAX_BYTES)
            throw new JobError('integrity_failed'); await mkdir(directory, { recursive: true, mode: 0o700 }); const path = join(directory, sha); try {
            await writeFile(path, bytes, { flag: 'wx', mode: 0o600 });
        }
        catch (e) {
            if ((e as NodeJS.ErrnoException).code !== 'EEXIST')
                throw e;
            const current = await readFile(path);
            if (jobHash(current) !== sha)
                throw new JobError('integrity_failed');
        } context.signal.throwIfAborted(); return 's3://gregale-local/' + sha; },
        async get(uri, context) { context.signal.throwIfAborted(); const match = /^s3:\/\/gregale-local\/([a-f0-9]{64})$/.exec(uri); if (!match)
            throw new JobError('invalid_reference'); const bytes = await readFile(join(directory, match[1]!)); context.signal.throwIfAborted(); if (bytes.length > JOB_MAX_BYTES || jobHash(bytes) !== match[1])
            throw new JobError('integrity_failed'); return bytes; }
    };
}
