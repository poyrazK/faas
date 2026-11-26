/** Transport-neutral MCP registration/call adapter. The host owns authentication and lifecycle. */
import { JobError, jobScope, jobSnapshot, jobValueHash } from './job-runtime.js';
import { PipelineToolError, type PipelineTool, type PipelineToolStart, type PipelineToolStatus, type PipelineToolWithResult } from './pipeline-tools.js';
import { pipelineToolWire as WIRE } from './pipeline-tool-protocol.js';
import { createPipelineOutputResources, type PipelineOutputResource } from './pipeline-output-resources.js';
import { fileEventHash } from './file-events.js';
import { pipelineTaskRevision, type PipelineContractMetadata, type PipelineTaskRevision } from './pipeline-task-manifest.js';
import type { QueryToolMCP } from './query-tool-mcp.js';
import type { QueryResourceLink } from './query-tools.js';
import { capturePipelinePreviewPolicy, isPipelinePreviewToken, pipelinePreviewTokenPattern, type PipelineStartPreviewPolicy } from './pipeline-start-preview.js';
import type { PipelineRunOptions } from './job-pipeline-client.js';
export interface PipelineToolMCPDescriptor {
    readonly name: string;
    readonly description: string;
    readonly inputSchema: Readonly<Record<string, unknown>>;
    readonly outputSchema: Readonly<Record<string, unknown>>;
    readonly annotations: {
        readonly readOnlyHint: boolean;
        readonly destructiveHint: boolean;
        readonly idempotentHint: boolean;
        readonly openWorldHint: boolean;
    };
}
export interface PipelineToolMCPResult {
    readonly resultType?: 'complete';
    readonly content: readonly {
        readonly type: 'text';
        readonly text: string;
    }[];
    readonly structuredContent?: Readonly<Record<string, unknown>>;
    readonly isError: boolean;
}
export interface PipelineToolMCP<A> {
    readonly pipelineContract?: PipelineContractMetadata;
    readonly tools: readonly PipelineToolMCPDescriptor[];
    call(name: string, args: unknown, access: A, options: PipelineRunOptions): Promise<PipelineToolMCPResult>;
}
export class PipelineToolMCPError extends Error {
    constructor(readonly code: -32601 | -32602) { super(code === -32601 ? 'Unknown pipeline tool' : 'Invalid pipeline tool arguments'); this.name = 'PipelineToolMCPError'; }
}
type PipelineMCPOptions<I, A, P> = {
    protocolVersion?: '2025-11-25' | '2026-07-28';
    revision?: PipelineTaskRevision;
    preview?: PipelineStartPreviewPolicy<I, P, A>;
};
export type PipelineToolMCPWithResources<A> = Pick<QueryToolMCP<A>, 'resourceTemplates' | 'readResource'> & Omit<PipelineToolMCP<A>, 'call'> & {
    call(name: string, args: unknown, access: A, options: PipelineRunOptions): Promise<
        Omit<PipelineToolMCPResult, 'content'> & {
            readonly content: readonly [PipelineToolMCPResult['content'][number], ...QueryResourceLink[]];
        }
    >;
};
export function createPipelineToolMCP<I, O, A, P = unknown>(tool: PipelineToolWithResult<I, O, A>, options: PipelineMCPOptions<I, A, P> & {
    outputResources: readonly PipelineOutputResource<O>[];
}): PipelineToolMCPWithResources<A>;
export function createPipelineToolMCP<I, O, A, P = unknown>(tool: PipelineTool<I, O, A>, options?: PipelineMCPOptions<I, A, P>): PipelineToolMCP<A>;
export function createPipelineToolMCP<I, O, A, P = unknown>(tool: PipelineTool<I, O, A>, options: PipelineMCPOptions<I, A, P> & {
    outputResources?: readonly PipelineOutputResource<O>[];
} = {}): PipelineToolMCP<A> | PipelineToolMCPWithResources<A> {
    const protocol = options.protocolVersion ?? '2025-11-25';
    if (!['2025-11-25', '2026-07-28'].includes(protocol))
        throw new PipelineToolError('invalid_input');
    let revision: PipelineTaskRevision | undefined;
    try {
        revision = options.revision === undefined ? undefined : pipelineTaskRevision(options.revision);
    } catch {
        throw new PipelineToolError('invalid_input');
    }
    const retained = revision?.starts === 'retained';
    const preview = !retained && options.preview ? capturePipelinePreviewPolicy(options.preview) : undefined;
    const start = retained ? tool.replayStart : tool.start;
    if (preview && (typeof tool.previewStart !== 'function' || typeof tool.startPreviewed !== 'function'))
        throw new PipelineToolError('invalid_input');
    const operations = preview ? [...WIRE.operations, 'preview' as const] : WIRE.operations;
    const tokenSchema = { type: 'string', pattern: pipelinePreviewTokenPattern };
    if (typeof start !== 'function')
        throw new PipelineToolError('invalid_input');
    const resources = options.outputResources === undefined ? undefined : createPipelineOutputResources(tool as PipelineToolWithResult<I, O, A>, options.outputResources);
    const descriptions = {
        start: retained
            ? 'Replay an existing start with its original request_id and input. Unknown starts require reconciliation.'
            : preview
                ? 'Confirm a pipeline preview using the same request_id and input.'
                : 'Start once using a stable request_id; retain it for recovery. Unknown starts require reconciliation; never invent a new ID to retry.',
        preview: 'Preview the current source and expected actions for this request.',
        status: 'Read current status and an authenticated human review link when approval is pending.',
        result: 'Read validated output when complete; otherwise return current status.',
        cancel: 'Explicitly cancel orchestration; already accepted Jobs may continue.',
        reconcile: 'Adopt an exact verified run after uncertain acceptance; never starts or restarts work.',
    };
    const tools = operations.map(op => {
        const properties: Record<string, unknown> = { request_id: WIRE.request_id_schema };
        if (op === 'start' || op === 'preview')
            properties.input = tool.contract.spec.input;
        if (op === 'reconcile')
            properties.run_id = WIRE.request_id_schema;
        const required = Object.keys(properties);
        if (op === 'preview' || op === 'start' && (revision || preview))
            properties.expected_contract_sha256 = { type: 'string', pattern: '^[a-f0-9]{64}$' };
        if (op === 'start' && (preview || retained))
            properties.preview_token = tokenSchema;
        const outputSchema = op === 'preview'
            ? { type: 'object', properties: {
                summary: preview!.contract.spec.output,
                preview_token: tokenSchema,
                expires_at: { type: 'integer', minimum: 1 },
            }, required: ['summary', 'preview_token', 'expires_at'], additionalProperties: false }
            : structuredClone(WIRE.result_schema) as Record<string, any>;
        if (op === 'result')
            outputSchema.properties.output = tool.contract.spec.output;
        return jobSnapshot({
            name: tool.name + '_' + op,
            description: tool.description + ' ' + descriptions[op],
            inputSchema: { type: 'object', properties, required, additionalProperties: false },
            outputSchema,
            annotations: {
                readOnlyHint: op === 'status' || op === 'result' || op === 'preview',
                destructiveHint: (op === 'start' && !retained) || op === 'cancel',
                idempotentHint: op !== 'cancel',
                openWorldHint: true,
            },
        });
    });
    type Result = Awaited<ReturnType<PipelineToolMCPWithResources<A>['call']>>;
    const pack = (value: Record<string, unknown>, links: QueryResourceLink[] = []): Result => {
        const result: Result = jobSnapshot({ ...(protocol === '2026-07-28' ? { resultType: 'complete' as const } : {}), content: [{ type: 'text' as const, text: JSON.stringify(value) }, ...links], structuredContent: value, isError: false });
        if (Buffer.byteLength(JSON.stringify(result)) > 1048576)
            throw new PipelineToolError('limit');
        return result;
    };
    const publicResult = (raw: PipelineToolStart | PipelineToolStatus | null) => {
        if (!raw)
            return { state: 'not_found', receipt: null, steps: [], approval: null };
        const i = raw.receipt.intent;
        return { state: raw.state, receipt: { identity: i.identity, request_id: i.request_id, tool: i.name, run_id: raw.receipt.checkpoint?.run_id ?? null }, steps: 'steps' in raw ? raw.steps : [], approval: 'approval' in raw ? raw.approval : null, ...('replayed' in raw ? { replayed: raw.replayed } : {}), ...('source_failure' in raw && { source_failure: raw.source_failure }) };
    };
    const adapter = { pipelineContract: jobSnapshot({ tool: tool.name, spec: tool.contract.spec, ...(revision && { revision }), ...(preview && { preview: preview.contract.spec.output }) }), tools: jobSnapshot(tools), async call(name: string, args: unknown, access: A, settings: PipelineRunOptions): Promise<Result> {
            const index = tools.findIndex(t => t.name === name);
            if (index < 0)
                throw new PipelineToolMCPError(-32601);
            const op = operations[index]!;
            if (!args || typeof args !== 'object' || Array.isArray(args))
                throw new PipelineToolMCPError(-32602);
            const a = jobSnapshot(args) as Record<string, unknown>, expected = (op === 'start' || op === 'preview') ? ['input', 'request_id'] : op === 'reconcile' ? ['request_id', 'run_id'] : ['request_id'];
            if ((op === 'preview' || op === 'start' && (revision || preview)) && Object.hasOwn(a, 'expected_contract_sha256')) {
                if (!fileEventHash(a.expected_contract_sha256))
                    throw new PipelineToolMCPError(-32602);
                expected.unshift('expected_contract_sha256');
            }
            if (op === 'start' && (preview || retained) && Object.hasOwn(a, 'preview_token')) {
                if (!isPipelinePreviewToken(a.preview_token))
                    throw new PipelineToolMCPError(-32602);
                expected.push('preview_token');
            }
            if (Object.keys(a).sort().join() !== expected.sort().join() || typeof a.request_id !== 'string' || op === 'reconcile' && typeof a.run_id !== 'string')
                throw new PipelineToolMCPError(-32602);
            try {
                if (Buffer.byteLength(JSON.stringify(a)) > 1048576)
                    throw new PipelineToolMCPError(-32602);
                return await jobScope(settings.timeoutMs, settings.signal, async (signal) => {
                    const end = performance.now() + settings.timeoutMs, remaining = () => ({ timeoutMs: Math.max(1, Math.ceil(end - performance.now())), signal });
                    if (op === 'start' || op === 'preview') {
                        if (a.expected_contract_sha256 !== undefined && a.expected_contract_sha256 !== tool.contract.sha256)
                            throw new PipelineToolError('revision_unavailable');
                        const input = tool.contract.input(a.input), id = a.request_id as string;
                        if (op === 'preview')
                            return pack({ ...await tool.previewStart!(input, id, access, remaining(), preview!) });
                        const started = preview
                            ? await tool.startPreviewed!(input, id, access, remaining(), preview, a.preview_token as string | undefined)
                            : await start.call(tool, input, id, access, remaining());
                        return pack(publicResult(started));
                    }
                    if (op === 'reconcile')
                        return pack(publicResult(await tool.reconcile(a.request_id as string, a.run_id as string, access, remaining())));
                    if (op === 'cancel')
                        return pack(publicResult(await tool.cancel(a.request_id as string, access, remaining())));
                    const status = await tool.inspect(a.request_id as string, access, remaining());
                    if (op === 'result' && status?.state === 'succeeded') {
                        if (resources) {
                            const snapshot = await resources.result(a.request_id as string, access, remaining());
                            if (snapshot.receipt.intent.identity !== status.receipt.intent.identity ||
                                jobValueHash(snapshot.receipt.checkpoint) !== jobValueHash(status.receipt.checkpoint))
                                throw new PipelineToolError('receipt_conflict');
                            return pack({ ...publicResult(status), output: snapshot.output }, resources.links(snapshot));
                        }
                        const output = await tool.result(a.request_id as string, access, remaining());
                        return pack({ ...publicResult(status), output });
                    }
                    return pack(publicResult(status));
                });
            }
            catch (error) {
                if (error instanceof PipelineToolMCPError)
                    throw error;
                if (error instanceof JobError && error.code === 'invalid_input' || error instanceof PipelineToolError && error.code === 'invalid_input')
                    throw new PipelineToolMCPError(-32602);
                const code = settings.signal?.aborted ? 'cancelled' : error instanceof JobError && error.code === 'deadline' ? 'deadline' : error instanceof PipelineToolError ? error.code : 'request_failed';
                return jobSnapshot({ ...(protocol === '2026-07-28' ? { resultType: 'complete' as const } : {}), content: [{ type: 'text' as const, text: JSON.stringify({ code }) }], isError: true });
            }
        } };
    if (resources) return Object.freeze({
        ...adapter, resourceTemplates: resources.resourceTemplates, readResource: resources.readResource,
    });
    return Object.freeze(adapter) as PipelineToolMCP<A>;
}
