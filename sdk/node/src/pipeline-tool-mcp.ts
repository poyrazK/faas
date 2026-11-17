/** Transport-neutral MCP registration/call adapter. The host owns authentication and lifecycle. */
import { JobError, jobScope, jobSnapshot } from './job-runtime.js';
import { PipelineToolError, type PipelineTool, type PipelineToolStart, type PipelineToolStatus } from './pipeline-tools.js';
import { pipelineToolWire as WIRE } from './pipeline-tool-protocol.js';
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
    readonly tools: readonly PipelineToolMCPDescriptor[];
    call(name: string, args: unknown, access: A, options: PipelineRunOptions): Promise<PipelineToolMCPResult>;
}
export class PipelineToolMCPError extends Error {
    constructor(readonly code: -32601 | -32602) { super(code === -32601 ? 'Unknown pipeline tool' : 'Invalid pipeline tool arguments'); this.name = 'PipelineToolMCPError'; }
}
export function createPipelineToolMCP<I, O, A>(tool: PipelineTool<I, O, A>, options: {
    protocolVersion?: '2025-11-25' | '2026-07-28';
} = {}): PipelineToolMCP<A> {
    const protocol = options.protocolVersion ?? '2025-11-25';
    if (!['2025-11-25', '2026-07-28'].includes(protocol))
        throw new PipelineToolError('invalid_input');
    const tools = WIRE.operations.map(op => {
        const properties: Record<string, unknown> = { request_id: WIRE.request_id_schema };
        if (op === 'start')
            properties.input = tool.contract.spec.input;
        if (op === 'reconcile')
            properties.run_id = WIRE.request_id_schema;
        const outputSchema = structuredClone(WIRE.result_schema) as Record<string, any>;
        if (op === 'result')
            outputSchema.properties.output = tool.contract.spec.output;
        return jobSnapshot({ name: tool.name + '_' + op, description: tool.description + ' ' + ({ start: 'Start once using a stable request_id; retain it for recovery. Unknown starts require reconciliation; never invent a new ID to retry.', status: 'Read current status and an authenticated human review link when approval is pending.', result: 'Read validated output when complete; otherwise return current status.', cancel: 'Explicitly cancel orchestration; already accepted Jobs may continue.', reconcile: 'Adopt an exact verified run after uncertain acceptance; never starts or restarts work.' }[op]), inputSchema: { type: 'object', properties, required: Object.keys(properties), additionalProperties: false }, outputSchema, annotations: { readOnlyHint: op === 'status' || op === 'result', destructiveHint: op === 'start' || op === 'cancel', idempotentHint: op !== 'cancel', openWorldHint: true } });
    });
    const pack = (value: Record<string, unknown>): PipelineToolMCPResult => {
        const result: PipelineToolMCPResult = jobSnapshot({ ...(protocol === '2026-07-28' ? { resultType: 'complete' as const } : {}), content: [{ type: 'text' as const, text: JSON.stringify(value) }], structuredContent: value, isError: false });
        if (Buffer.byteLength(JSON.stringify(result)) > 1048576)
            throw new PipelineToolError('limit');
        return result;
    };
    const publicResult = (raw: PipelineToolStart | PipelineToolStatus | null) => {
        if (!raw)
            return { state: 'not_found', receipt: null, steps: [], approval: null };
        const i = raw.receipt.intent;
        return { state: raw.state, receipt: { identity: i.identity, request_id: i.request_id, tool: i.name, run_id: raw.receipt.checkpoint?.run_id ?? null }, steps: 'steps' in raw ? raw.steps : [], approval: 'approval' in raw ? raw.approval : null, ...('replayed' in raw ? { replayed: raw.replayed } : {}) };
    };
    return Object.freeze({ tools: jobSnapshot(tools), async call(name, args, access, settings) {
            const index = tools.findIndex(t => t.name === name);
            if (index < 0)
                throw new PipelineToolMCPError(-32601);
            const op = WIRE.operations[index]!;
            if (!args || typeof args !== 'object' || Array.isArray(args))
                throw new PipelineToolMCPError(-32602);
            const a = jobSnapshot(args) as Record<string, unknown>, expected = op === 'start' ? ['input', 'request_id'] : op === 'reconcile' ? ['request_id', 'run_id'] : ['request_id'];
            if (Object.keys(a).sort().join() !== expected.join() || typeof a.request_id !== 'string' || op === 'reconcile' && typeof a.run_id !== 'string')
                throw new PipelineToolMCPError(-32602);
            try {
                if (Buffer.byteLength(JSON.stringify(a)) > 1048576)
                    throw new PipelineToolMCPError(-32602);
                return await jobScope(settings.timeoutMs, settings.signal, async (signal) => {
                    const end = performance.now() + settings.timeoutMs, remaining = () => ({ timeoutMs: Math.max(1, Math.ceil(end - performance.now())), signal });
                    if (op === 'start')
                        return pack(publicResult(await tool.start(tool.contract.input(a.input), a.request_id as string, access, remaining())));
                    if (op === 'reconcile')
                        return pack(publicResult(await tool.reconcile(a.request_id as string, a.run_id as string, access, remaining())));
                    if (op === 'cancel')
                        return pack(publicResult(await tool.cancel(a.request_id as string, access, remaining())));
                    const status = await tool.inspect(a.request_id as string, access, remaining());
                    if (op === 'result' && status?.state === 'succeeded') {
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
        } } satisfies PipelineToolMCP<A>);
}
