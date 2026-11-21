/** Portable application-owned tool start receipts. No payloads or credentials. */
import { jobSnapshot, jobValueHash } from './job-runtime.js';
import { eventPipelineScope } from './event-pipeline-state.js';
import { pipelineName, type PipelineScope, type PipelineQuery } from './pipeline-state.js';
import { fileEventHash, fileEventUUID } from './file-events.js';
import type { PipelineCheckpoint } from './job-pipeline-client.js';
import { pipelineToolSQL as SQL } from './pipeline-tool-protocol.js';
export { pipelineToolSchema } from './pipeline-tool-protocol.js';
export type PipelineToolAction = 'start' | 'inspect' | 'result' | 'cancel' | 'reconcile';
export type PipelineToolCode = 'invalid_input' | 'unauthorized' | 'receipt_conflict' | 'store_failed' | 'request_failed' | 'start_unknown' | 'not_terminal' | 'deadline' | 'cancelled' | 'limit';
export class PipelineToolError extends Error {
    constructor(readonly code: PipelineToolCode) { super('Pipeline tool: ' + code); this.name = 'PipelineToolError'; }
}
export interface PipelineToolActor {
    readonly subject_id: string;
    readonly owner_id: string;
}
export interface PipelineToolIntent {
    readonly version: 1;
    readonly identity: string;
    readonly scope: PipelineScope;
    readonly name: string;
    readonly request_id: string;
    readonly subject_id: string;
    readonly owner_id: string;
    readonly contract_sha256: string;
    readonly input_sha256: string;
    readonly idempotency_key: string;
}
export interface PipelineToolReceipt {
    readonly intent: PipelineToolIntent;
    readonly checkpoint: PipelineCheckpoint | null;
}
export interface PipelineToolStore {
    load(identity: string, signal: AbortSignal): Promise<PipelineToolReceipt | null>;
    reserve(intent: PipelineToolIntent, signal: AbortSignal): Promise<{
        created: boolean;
        receipt: PipelineToolReceipt;
    }>;
    complete(intent: PipelineToolIntent, checkpoint: PipelineCheckpoint, signal: AbortSignal): Promise<PipelineToolReceipt>;
}
export const pipelineToolName = (name: unknown): name is string => typeof name === 'string' && /^[a-z][a-z0-9_]{0,47}$/.test(name);
export function pipelineToolRequestId(id: unknown): string {
    if (!fileEventUUID(id))
        throw new PipelineToolError('invalid_input');
    return id;
}
export function pipelineToolIdentity(scope: PipelineScope, name: string, subject: string, requestId: string): string {
    if (!pipelineToolName(name) || !pipelineName(subject))
        throw new PipelineToolError('invalid_input');
    return jobValueHash(['gregale.pipeline-tool.v1', eventPipelineScope(scope), name, subject, pipelineToolRequestId(requestId)]);
}
export function matchPipelineToolCheckpoint(i: PipelineToolIntent, raw: unknown): PipelineCheckpoint {
    const c = raw as PipelineCheckpoint;
    if (!c || Object.keys(c).sort().join() !== 'contract_sha256,input_sha256,owner_id,run_id,scope,version' || c.version !== 1 || !fileEventUUID(c.run_id) || c.owner_id !== i.owner_id || c.contract_sha256 !== i.contract_sha256 || c.input_sha256 !== i.input_sha256 || jobValueHash(c.scope) !== jobValueHash(i.scope))
        throw new PipelineToolError('receipt_conflict');
    return jobSnapshot(c);
}
export function parsePipelineToolReceipt(raw: unknown): PipelineToolReceipt {
    try {
        const r = raw as PipelineToolReceipt, i = r.intent;
        if (Object.keys(r).sort().join() !== 'checkpoint,intent' || !i || Object.keys(i).sort().join() !== 'contract_sha256,idempotency_key,identity,input_sha256,name,owner_id,request_id,scope,subject_id,version' || i.version !== 1 || !pipelineName(i.owner_id) || !fileEventHash(i.input_sha256) || !fileEventHash(i.contract_sha256) || jobValueHash(i.scope) !== jobValueHash(eventPipelineScope(i.scope)) || i.identity !== pipelineToolIdentity(i.scope, i.name, i.subject_id, i.request_id) || i.idempotency_key !== 'pipeline-tool:' + i.identity)
            throw Error();
        if (r.checkpoint !== null)
            matchPipelineToolCheckpoint(i, r.checkpoint);
        return jobSnapshot(r);
    }
    catch {
        throw new PipelineToolError('receipt_conflict');
    }
}
/** Each query must be one acknowledged autocommit statement on the same primary. */
export function postgresPipelineToolStore(query: PipelineQuery): PipelineToolStore {
    const load = async (identity: string, signal: AbortSignal) => {
        signal.throwIfAborted();
        const r = await query(SQL.load, [identity], signal);
        signal.throwIfAborted();
        if (r.rows.length > 1)
            throw new PipelineToolError('receipt_conflict');
        return r.rows.length ? parsePipelineToolReceipt(r.rows[0]!.receipt) : null;
    };
    return Object.freeze({ load,
        async reserve(intent, signal) {
            signal.throwIfAborted();
            parsePipelineToolReceipt({ intent, checkpoint: null });
            const r = await query(SQL.reserve, [intent.identity, JSON.stringify({ intent, checkpoint: null })], signal);
            signal.throwIfAborted();
            const receipt = r.rows.length === 1 ? parsePipelineToolReceipt(r.rows[0]!.receipt) : await load(intent.identity, signal);
            if (r.rows.length > 1 || !receipt || jobValueHash(receipt.intent) !== jobValueHash(intent))
                throw new PipelineToolError('receipt_conflict');
            return { created: r.rows.length === 1, receipt };
        },
        async complete(intent, checkpoint, signal) {
            signal.throwIfAborted();
            matchPipelineToolCheckpoint(intent, checkpoint);
            const r = await query(SQL.complete, [intent.identity, JSON.stringify(intent), JSON.stringify(checkpoint)], signal);
            signal.throwIfAborted();
            if (r.rows.length !== 1)
                throw new PipelineToolError('receipt_conflict');
            const receipt = parsePipelineToolReceipt(r.rows[0]!.receipt);
            if (jobValueHash(receipt.intent) !== jobValueHash(intent) || jobValueHash(receipt.checkpoint) !== jobValueHash(checkpoint))
                throw new PipelineToolError('receipt_conflict');
            return receipt;
        }
    } satisfies PipelineToolStore);
}
