/** File-to-job composition with application-owned durable submission receipts. */
import { JobContract, jobSnapshot, jobValueHash, jobHash, prepareJobInput, type JobInput, type JobStore } from './job-runtime.js';
import { parseJobCheckpoint, type JobCheckpoint, type TypedJob } from './typed-jobs.js';
import { blobFileSource, fileSourceDigest, type FileBucket } from './files.js';
import { FilePipelineError, fileEventHash, fileEventUUID, filePipelineScope, filePipelineRun, filePipelineCheck, captureFileCreatedEvent, type FilePipelineScope, type FileCreatedEvent, type FileDelivery, type FileEventVerifier, type FilePipelineOptions } from './file-events.js';
export * from './file-events.js';
export interface FilePipelineOwner {
    readonly id: string;
    readonly sha256?: string;
    readonly source_digest?: string;
}
export interface FilePipelineIntent {
    readonly version: 1;
    readonly identity: string;
    readonly request_digest: string;
    readonly idempotency_key: string;
    readonly pipeline: string;
    readonly scope: FilePipelineScope;
    readonly owner_id: string;
    readonly event: FileCreatedEvent;
    readonly contract_sha256: string;
    readonly job_name: string;
    readonly input_digest: string;
    readonly inputs: readonly {
        readonly input_id: string;
        readonly binding_sha256: string;
        readonly source_task_index: null;
    }[];
}
export interface FilePipelineReceipt {
    readonly intent: FilePipelineIntent;
    readonly checkpoint: JobCheckpoint | null;
}
export interface FilePipelineStore {
    load(identity: string, signal: AbortSignal): Promise<FilePipelineReceipt | null>;
    /** Atomic insert-if-absent. `created` proves this call installed the intent. */
    reserve(intent: FilePipelineIntent, signal: AbortSignal): Promise<{
        created: boolean;
        receipt: FilePipelineReceipt;
    }>;
    complete(intent: FilePipelineIntent, checkpoint: JobCheckpoint, signal: AbortSignal): Promise<FilePipelineReceipt>;
}
export type FilePipelineOutcome = {
    readonly outcome: 'ignored';
    readonly reason: 'filter_mismatch' | 'unowned_file';
} | {
    readonly outcome: 'submitted' | 'duplicate';
    readonly receipt: FilePipelineReceipt;
    readonly checkpoint: JobCheckpoint;
} | {
    readonly outcome: 'unresolved';
    readonly receipt: FilePipelineReceipt;
    readonly runId?: string;
};
export interface FilePipeline<Input, Output> {
    handle(delivery: FileDelivery, options: FilePipelineOptions): Promise<FilePipelineOutcome>;
    /** Explicit read-only Jobs lookup, then adopt only the exact saved manifest. */
    reconcile(identity: string, runId: string, options: FilePipelineOptions): Promise<FilePipelineOutcome>;
}
function matches(intent: FilePipelineIntent, value: JobCheckpoint): JobCheckpoint {
    const c = parseJobCheckpoint(value);
    if (c.base_url !== intent.scope.base_url || c.account_id !== intent.scope.account_id || c.job_name !== intent.job_name || c.contract_sha256 !== intent.contract_sha256 || c.input_digest !== intent.input_digest || c.source_run_id !== null || jobValueHash(c.inputs) !== jobValueHash(intent.inputs))
        throw new FilePipelineError('conflict');
    return c;
}
export function parseFilePipelineReceipt(value: unknown): FilePipelineReceipt {
    try {
        const r = value as FilePipelineReceipt, i = r.intent;
        if (Object.keys(r).sort().join() !== 'checkpoint,intent' || Object.keys(i).sort().join() !== 'contract_sha256,event,idempotency_key,identity,input_digest,inputs,job_name,owner_id,pipeline,request_digest,scope,version' || i.version !== 1 || !fileEventHash(i.identity) || !fileEventHash(i.request_digest) || i.idempotency_key !== 'file-pipeline:' + i.identity || !fileEventHash(i.contract_sha256) || !fileEventHash(i.input_digest) || !Array.isArray(i.inputs) || !i.inputs.length || i.inputs.length > 10000 || new Set(i.inputs.map(x => x.input_id)).size !== i.inputs.length || i.inputs.some(x => !x || Object.keys(x).sort().join() !== 'binding_sha256,input_id,source_task_index' || typeof x.input_id !== 'string' || !/^[A-Za-z0-9_.:-]{1,128}$/.test(x.input_id) || !fileEventHash(x.binding_sha256) || x.source_task_index !== null) || typeof i.owner_id !== 'string' || !i.owner_id || i.owner_id.length > 128 || typeof i.pipeline !== 'string' || !/^[A-Za-z0-9_.:-]{1,128}$/.test(i.pipeline) || typeof i.job_name !== 'string' || !/^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/.test(i.job_name))
            throw new Error();
        if (jobValueHash(filePipelineScope(i.scope)) !== jobValueHash(i.scope))
            throw new Error();
        const e = i.event;
        if (!e || e.version !== 1 || e.account_id !== i.scope.account_id || e.app_id !== i.scope.app_id || e.bucket !== i.scope.bucket || !fileEventUUID(e.receipt_id) || !['write:' + e.receipt_id, 'multipart:' + e.receipt_id].includes(e.event_id) || typeof e.key !== 'string' || !e.key || Buffer.byteLength(e.key) > 1024 || !Number.isSafeInteger(e.size_bytes) || e.size_bytes < 0 || e.size_bytes > 1024 ** 4 || typeof e.etag !== 'string' || !e.etag || e.version_id !== null && !fileEventUUID(e.version_id))
            throw new Error();
        if (jobValueHash(captureFileCreatedEvent(e, i.scope)) !== jobValueHash(e))
            throw new Error();
        if (r.checkpoint !== null)
            matches(i, r.checkpoint);
        return jobSnapshot(r);
    }
    catch {
        throw new FilePipelineError('conflict');
    }
}
export interface FilePipelineDefinition<Input, Output> {
    readonly name: string;
    readonly revision: string;
    readonly scope: FilePipelineScope;
    readonly prefix: string;
    readonly suffix: string;
    readonly contract: JobContract<Input, Output>;
    readonly job: TypedJob<Input, Output>;
    readonly files: FileBucket;
    /** Same content-addressed store as `job`: the same digest must return the
     * same URI. The SDK's managed JobStore satisfies this contract. */
    readonly jobStore?: JobStore;
    readonly store: FilePipelineStore;
    readonly verify: FileEventVerifier;
    /** Resolve a durable application record. Never derive ownership from the key alone. */
    readonly authorize: (event: FileCreatedEvent, signal: AbortSignal) => Promise<FilePipelineOwner | null>;
    /** Pure input mapping: effects belong in the job handler, after submission. */
    readonly map: (event: FileCreatedEvent, bytes: Uint8Array, owner: FilePipelineOwner, signal: AbortSignal) => Promise<readonly JobInput<Input>[]>;
}
export function createFilePipeline<Input, Output>(definition: FilePipelineDefinition<Input, Output>): FilePipeline<Input, Output> {
    const d = { ...definition, scope: filePipelineScope(definition.scope) };
    if (!/^[A-Za-z0-9_.:-]{1,128}$/.test(d.name) || typeof d.revision !== 'string' || !/^[A-Za-z0-9_.:-]{1,128}$/.test(d.revision) || typeof d.prefix !== 'string' || !d.prefix || typeof d.suffix !== 'string' || Buffer.byteLength(d.prefix) > 1024 || Buffer.byteLength(d.suffix) > 1024 || /[\x00*]/.test(d.prefix + d.suffix) || d.files.scope.base_url !== d.scope.base_url || d.files.scope.app_slug !== d.scope.app_slug || d.files.scope.bucket !== d.scope.bucket)
        throw new FilePipelineError('invalid_input');
    const config = jobValueHash({ revision: d.revision, scope: d.scope, prefix: d.prefix, suffix: d.suffix, contract: d.contract.sha256 });
    async function load(identity: string, signal: AbortSignal): Promise<FilePipelineReceipt | null> {
        try {
            filePipelineCheck(signal);
            const value = await d.store.load(identity, signal);
            filePipelineCheck(signal);
            const r = value === null ? null : parseFilePipelineReceipt(value);
            if (r && (r.intent.identity !== identity || r.intent.pipeline !== d.name || jobValueHash(r.intent.scope) !== jobValueHash(d.scope) || r.intent.contract_sha256 !== d.contract.sha256 || r.intent.job_name !== d.contract.spec.name))
                throw new FilePipelineError('conflict');
            return r;
        }
        catch (e) {
            filePipelineCheck(signal);
            if (e instanceof FilePipelineError)
                throw e;
            throw new FilePipelineError('store_failed');
        }
    }
    function outcome(r: FilePipelineReceipt, kind: 'submitted' | 'duplicate'): FilePipelineOutcome { return r.checkpoint ? { outcome: kind, receipt: r, checkpoint: r.checkpoint } : { outcome: 'unresolved', receipt: r }; }
    async function finish(intent: FilePipelineIntent, checkpoint: JobCheckpoint, signal: AbortSignal): Promise<FilePipelineReceipt> {
        matches(intent, checkpoint);
        filePipelineCheck(signal);
        try {
            const value = await d.store.complete(intent, checkpoint, signal);
            filePipelineCheck(signal);
            const r = parseFilePipelineReceipt(value);
            if (jobValueHash(r.intent) !== jobValueHash(intent) || r.checkpoint?.run_id !== checkpoint.run_id)
                throw new FilePipelineError('conflict');
            return r;
        }
        catch (e) {
            filePipelineCheck(signal);
            if (e instanceof FilePipelineError)
                throw e;
            throw new FilePipelineError('store_failed', checkpoint.run_id);
        }
    }
    return Object.freeze({
        handle(delivery, options) {
            const captured = { body: new Uint8Array(delivery.body), invocationId: delivery.invocationId }, settings = { ...options };
            return filePipelineRun(settings, async (signal) => {
                const event = captureFileCreatedEvent(await d.verify(captured, signal), d.scope);
                filePipelineCheck(signal);
                if (event.account_id !== d.scope.account_id || event.app_id !== d.scope.app_id || event.bucket !== d.scope.bucket)
                    throw new FilePipelineError('scope_mismatch');
                if (!event.key.startsWith(d.prefix) || !event.key.endsWith(d.suffix))
                    return { outcome: 'ignored', reason: 'filter_mismatch' };
                const ownerValue = await d.authorize(event, signal);
                filePipelineCheck(signal);
                if (ownerValue === null)
                    return { outcome: 'ignored', reason: 'unowned_file' };
                const owner = jobSnapshot({ id: ownerValue.id, ...(ownerValue.sha256 !== undefined ? { sha256: ownerValue.sha256 } : {}), ...(ownerValue.source_digest !== undefined ? { source_digest: ownerValue.source_digest } : {}) });
                if (typeof owner.id !== 'string' || !owner.id || owner.id.length > 128 || owner.sha256 !== undefined && !fileEventHash(owner.sha256) || owner.source_digest !== undefined && !fileEventHash(owner.source_digest))
                    throw new FilePipelineError('authorization_denied');
                const identity = jobValueHash([d.name, d.scope.account_id, d.scope.app_id, d.scope.bucket, event.receipt_id]), requestDigest = jobValueHash({ config, event, owner });
                const existing = await load(identity, signal);
                if (existing) {
                    if (existing.intent.request_digest !== requestDigest)
                        throw new FilePipelineError('conflict');
                    return outcome(existing, 'duplicate');
                }
                if (event.size_bytes > (settings.maxBytes ?? 16 * 1024 * 1024))
                    throw new FilePipelineError('limit');
                if (event.version_id === null && !owner.sha256 && !owner.source_digest)
                    throw new FilePipelineError('version_required');
                let bytes: Uint8Array;
                try {
                    bytes = await d.files.download(event.key, { timeoutMs: settings.timeoutMs, signal, maxBytes: settings.maxBytes ?? 16 * 1024 * 1024, ...(event.version_id ? { versionId: event.version_id } : {}) });
                    filePipelineCheck(signal);
                }
                catch {
                    filePipelineCheck(signal);
                    throw new FilePipelineError('source_unavailable');
                }
                if (bytes.length !== event.size_bytes || owner.sha256 && jobHash(bytes) !== owner.sha256 || owner.source_digest && await fileSourceDigest(blobFileSource(new Blob([bytes as BlobPart])), signal) !== owner.source_digest)
                    throw new FilePipelineError('source_changed');
                const mapped = await d.map(event, bytes, owner, signal);
                filePipelineCheck(signal);
                if (!Array.isArray(mapped) || !mapped.length || mapped.length > (settings.maxItems ?? 1000) || new Set(mapped.map(i => i.id)).size !== mapped.length)
                    throw new FilePipelineError('limit');
                const inputs = jobSnapshot(mapped.map(i => ({ id: i.id, value: d.contract.input(i.value) }))), refs = [];
                for (const input of inputs) {
                    filePipelineCheck(signal);
                    refs.push(await prepareJobInput(d.contract, input, { inputId: input.id, attempt: 0, signal }, d.jobStore));
                }
                filePipelineCheck(signal);
                if (Buffer.byteLength(JSON.stringify(refs)) > 1024 * 1024)
                    throw new FilePipelineError('limit');
                const intent: FilePipelineIntent = jobSnapshot({ version: 1, identity, request_digest: requestDigest, idempotency_key: 'file-pipeline:' + identity, pipeline: d.name, scope: d.scope, owner_id: owner.id, event, contract_sha256: d.contract.sha256, job_name: d.contract.spec.name, input_digest: jobHash(Buffer.from(JSON.stringify(refs))), inputs: refs.map(r => ({ input_id: r.input_id, binding_sha256: jobHash(Buffer.from(r.input_ref)), source_task_index: null })) });
                let reservation;
                try {
                    reservation = await d.store.reserve(intent, signal);
                    filePipelineCheck(signal);
                }
                catch {
                    filePipelineCheck(signal);
                    throw new FilePipelineError('store_failed');
                }
                const saved = parseFilePipelineReceipt(reservation.receipt);
                if (jobValueHash(saved.intent) !== jobValueHash(intent) || typeof reservation.created !== 'boolean')
                    throw new FilePipelineError('conflict');
                if (!reservation.created)
                    return outcome(saved, 'duplicate');
                if (saved.checkpoint !== null)
                    throw new FilePipelineError('conflict');
                filePipelineCheck(signal);
                let checkpoint: JobCheckpoint;
                try {
                    checkpoint = (await d.job.submit(inputs, { timeoutMs: settings.timeoutMs, signal, maxItems: settings.maxItems ?? 1000, idempotencyKey: intent.idempotency_key, retryMax: 0, expectedAccountId: d.scope.account_id, expectedBaseURL: d.scope.base_url })).checkpoint;
                    filePipelineCheck(signal);
                    matches(intent, checkpoint);
                }
                catch {
                    filePipelineCheck(signal);
                    return { outcome: 'unresolved', receipt: saved };
                }
                try {
                    return outcome(await finish(intent, checkpoint, signal), 'submitted');
                }
                catch (e) {
                    filePipelineCheck(signal);
                    if (e instanceof FilePipelineError && e.code === 'conflict')
                        throw e;
                    return { outcome: 'unresolved', receipt: saved, runId: checkpoint.run_id };
                }
            });
        },
        reconcile(identity, runId, options) {
            return filePipelineRun({ ...options }, async (signal) => {
                if (!fileEventHash(identity) || !fileEventUUID(runId))
                    throw new FilePipelineError('invalid_input');
                const saved = await load(identity, signal);
                if (!saved)
                    throw new FilePipelineError('invalid_input');
                if (saved.checkpoint) {
                    if (saved.checkpoint.run_id !== runId)
                        throw new FilePipelineError('conflict');
                    return outcome(saved, 'duplicate');
                }
                const run = await d.job.resumeById(runId, { timeoutMs: options.timeoutMs, signal, maxItems: options.maxItems ?? 1000 });
                filePipelineCheck(signal);
                matches(saved.intent, run.checkpoint);
                return outcome(await finish(saved.intent, run.checkpoint, signal), 'duplicate');
            });
        },
    } satisfies FilePipeline<Input, Output>);
}
/** Install explicitly in the application's PostgreSQL database. Keep receipts
 * for as long as an event can be delivered/replayed; deleting permits another run. */
export const filePipelineSchema = `CREATE TABLE IF NOT EXISTS public.gregale_file_pipeline_receipts (
  identity text PRIMARY KEY CHECK (identity ~ '^[a-f0-9]{64}$'),
  receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt) = 'object'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
`;
export type FilePipelineQuery = (sql: string, params: unknown[], signal: AbortSignal) => Promise<{
    rows: Record<string, unknown>[];
}>;
export function postgresFilePipelineStore(query: FilePipelineQuery): FilePipelineStore {
    const load = async (identity: string, signal: AbortSignal): Promise<FilePipelineReceipt | null> => {
        filePipelineCheck(signal);
        const result = await query('SELECT receipt FROM public.gregale_file_pipeline_receipts WHERE identity=$1', [identity], signal);
        filePipelineCheck(signal);
        if (result.rows.length > 1)
            throw new FilePipelineError('conflict');
        return result.rows.length ? parseFilePipelineReceipt(result.rows[0]!.receipt) : null;
    };
    return Object.freeze({ load, async reserve(intent, signal) {
            filePipelineCheck(signal);
            const result = await query('INSERT INTO public.gregale_file_pipeline_receipts(identity,receipt) VALUES ($1,$2::jsonb) ON CONFLICT (identity) DO NOTHING RETURNING receipt', [intent.identity, JSON.stringify({ intent, checkpoint: null })], signal);
            filePipelineCheck(signal);
            if (result.rows.length > 1)
                throw new FilePipelineError('conflict');
            const receipt = result.rows.length ? parseFilePipelineReceipt(result.rows[0]!.receipt) : await load(intent.identity, signal);
            if (!receipt || jobValueHash(receipt.intent) !== jobValueHash(intent))
                throw new FilePipelineError('conflict');
            return { created: result.rows.length === 1, receipt };
        }, async complete(intent, checkpoint, signal) {
            filePipelineCheck(signal);
            matches(intent, checkpoint);
            const result = await query("UPDATE public.gregale_file_pipeline_receipts SET receipt=jsonb_set(receipt,'{checkpoint}',$3::jsonb) WHERE identity=$1 AND receipt->'intent'=$2::jsonb AND (receipt->'checkpoint'='null'::jsonb OR receipt->'checkpoint'=$3::jsonb) RETURNING receipt", [intent.identity, JSON.stringify(intent), JSON.stringify(checkpoint)], signal);
            filePipelineCheck(signal);
            if (result.rows.length !== 1)
                throw new FilePipelineError('conflict');
            return parseFilePipelineReceipt(result.rows[0]!.receipt);
        } } satisfies FilePipelineStore);
}
