/** Typed business pipelines exposed through durable, currently authorized tools. */
import { JobContract, JobError, jobScope, jobSnapshot, jobValueHash } from './job-runtime.js';
import { createJobPipelineClient, type PipelineCheckpoint, type PipelineRunOptions } from './job-pipeline-client.js';
import { eventPipelineScope } from './event-pipeline-state.js';
import { parsePipelineGateRecord, type PipelineGateStore } from './pipeline-gates.js';
import { pipelineName, type PipelineScope } from './pipeline-state.js';
import { PipelineToolError, pipelineToolName, pipelineToolRequestId, pipelineToolIdentity, parsePipelineToolReceipt, matchPipelineToolCheckpoint, type PipelineToolActor, type PipelineToolAction, type PipelineToolIntent, type PipelineToolReceipt, type PipelineToolStore } from './pipeline-tool-state.js';
export * from './pipeline-tool-state.js';
export interface PipelineToolRequest<I> {
    readonly request_id: string;
    readonly input?: I;
}
export interface PipelineToolStart {
    readonly state: 'started' | 'start_unknown';
    readonly receipt: PipelineToolReceipt;
    readonly replayed: boolean;
}
export interface PipelineToolApproval {
    readonly kind: 'approval' | 'input';
    readonly id: string;
    readonly name: string;
    readonly expires_at: string;
    readonly review_url: string;
}
export interface PipelineToolStatus {
    readonly state: 'start_unknown' | 'running' | 'awaiting_approval' | 'awaiting_input' | 'succeeded' | 'failed' | 'cancelled';
    readonly receipt: PipelineToolReceipt;
    readonly steps: readonly {
        readonly name: string;
        readonly status: string;
        readonly attempt: number;
    }[];
    readonly approval: PipelineToolApproval | null;
}
export interface PipelineTool<I, O, A> {
    readonly name: string;
    readonly description: string;
    readonly contract: JobContract<I, O>;
    start(input: I, requestId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStart>;
    inspect(requestId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStatus | null>;
    result(requestId: string, access: A, options: PipelineRunOptions): Promise<O>;
    cancel(requestId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStatus>;
    reconcile(requestId: string, runId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStart>;
}
export interface PipelineToolDefinition<I, A> {
    readonly name: string;
    readonly description: string;
    readonly scope: PipelineScope;
    readonly store: PipelineToolStore;
    readonly token: string;
    readonly fetch?: typeof fetch;
    /** Authenticate principal and resolve current business authority. Never trust owner/subject from tool arguments. */
    readonly authorize: (request: PipelineToolRequest<I>, access: A, action: PipelineToolAction, signal: AbortSignal) => Promise<PipelineToolActor | null>;
    readonly gateStore?: PipelineGateStore;
    readonly reviewBaseURL?: string;
}
export function pipelineToolReviewURL(value: string): string {
    try {
        const u = new URL(value);
        if (u.username || u.password || u.hash || u.search || u.protocol !== 'https:' && !(u.protocol === 'http:' && ['127.0.0.1', 'localhost', '[::1]'].includes(u.hostname)))
            throw Error();
        return u.href;
    }
    catch {
        throw new PipelineToolError('invalid_input');
    }
}
export function definePipelineTool<I, O, A>(source: JobContract<I, O>, definition: PipelineToolDefinition<I, A>): PipelineTool<I, O, A> {
    const d = { ...definition, scope: eventPipelineScope(definition.scope) }, contract = new JobContract<I, O>(source.spec), scope = d.scope;
    if (!pipelineToolName(d.name) || typeof d.description !== 'string' || !d.description.trim() || d.description.length > 4096 || !d.token || !!d.gateStore !== !!d.reviewBaseURL)
        throw new PipelineToolError('invalid_input');
    const reviewURL = d.reviewBaseURL ? pipelineToolReviewURL(d.reviewBaseURL) : undefined;
    async function execute<T>(settings: PipelineRunOptions, work: (signal: AbortSignal, check: () => void, left: () => number) => Promise<T>): Promise<T> {
        const end = performance.now() + settings.timeoutMs;
        try {
            return await jobScope(settings.timeoutMs, settings.signal, async (signal) => {
                const check = () => {
                    signal.throwIfAborted();
                    if (performance.now() >= end)
                        throw new PipelineToolError('deadline');
                };
                check();
                const value = await work(signal, check, () => { check(); return Math.max(1, Math.ceil(end - performance.now())); });
                check();
                return value;
            });
        }
        catch (error) {
            if (error instanceof PipelineToolError)
                throw error;
            if (settings.signal?.aborted)
                throw new PipelineToolError('cancelled');
            if (error instanceof JobError && error.code === 'deadline')
                throw new PipelineToolError('deadline');
            throw new PipelineToolError('request_failed');
        }
    }
    async function prepare(request: PipelineToolRequest<I>, access: A, action: PipelineToolAction, signal: AbortSignal, check: () => void) {
        let actor: PipelineToolActor | null;
        try {
            actor = jobSnapshot(await d.authorize(jobSnapshot(request), access, action, signal));
            check();
        }
        catch {
            check();
            throw new PipelineToolError('unauthorized');
        }
        if (!actor || !pipelineName(actor.subject_id) || !pipelineName(actor.owner_id))
            throw new PipelineToolError('unauthorized');
        const identity = pipelineToolIdentity(scope, d.name, actor.subject_id, request.request_id);
        return { actor, binding: { version: 1 as const, scope, name: d.name, request_id: request.request_id, subject_id: actor.subject_id, owner_id: actor.owner_id, contract_sha256: contract.sha256, identity, idempotency_key: 'pipeline-tool:' + identity } };
    }
    function bound(binding: Omit<PipelineToolIntent, 'input_sha256'>, raw: unknown) {
        const r = parsePipelineToolReceipt(raw);
        if (Object.entries(binding).some(([key, value]) => jobValueHash(value) !== jobValueHash(r.intent[key as keyof PipelineToolIntent])))
            throw new PipelineToolError('receipt_conflict');
        return r;
    }
    async function stored<T>(work: () => Promise<T>, check: () => void) {
        check();
        try {
            const r = await work();
            check();
            return r;
        }
        catch (error) {
            check();
            if (error instanceof PipelineToolError)
                throw error;
            throw new PipelineToolError('store_failed');
        }
    }
    const client = (ownerId: string) => createJobPipelineClient({ baseURL: scope.base_url, scope, ownerId, contract, token: d.token, ...(d.fetch ? { fetch: d.fetch } : {}) });
    const startResult = (receipt: PipelineToolReceipt, replayed: boolean): PipelineToolStart => jobSnapshot({ state: receipt.checkpoint ? 'started' : 'start_unknown', receipt, replayed });
    async function operation(requestId: string, access: A, action: Exclude<PipelineToolAction, 'start'>, settings: PipelineRunOptions, runId?: string): Promise<PipelineToolStatus | O | PipelineToolStart | null> {
        const request = { request_id: pipelineToolRequestId(requestId) };
        return execute(settings, async (signal, check, left) => {
            const { actor, binding } = await prepare(request, access, action, signal, check), raw = await stored(() => d.store.load(binding.identity, signal), check);
            if (!raw) {
                if (action === 'inspect')
                    return null;
                throw new PipelineToolError('receipt_conflict');
            }
            let receipt = bound(binding, raw);
            if (action === 'reconcile') {
                const checkpoint = (await client(actor.owner_id).resumeById(pipelineToolRequestId(runId), { timeoutMs: left(), signal })).checkpoint;
                check();
                matchPipelineToolCheckpoint(receipt.intent, checkpoint);
                receipt = bound(binding, await stored(() => d.store.complete(receipt.intent, checkpoint, signal), check));
                if (jobValueHash(receipt.checkpoint) !== jobValueHash(checkpoint))
                    throw new PipelineToolError('receipt_conflict');
                return startResult(receipt, true);
            }
            if (!receipt.checkpoint) {
                if (action !== 'inspect')
                    throw new PipelineToolError('start_unknown');
                return jobSnapshot({ state: 'start_unknown', receipt, steps: [], approval: null });
            }
            const run = await client(actor.owner_id).resumeById(receipt.checkpoint.run_id, { timeoutMs: left(), signal });
            check();
            matchPipelineToolCheckpoint(receipt.intent, run.checkpoint);
            const info = action === 'cancel' ? await run.cancel({ timeoutMs: left(), signal }) : await run.inspect({ timeoutMs: left(), signal });
            check();
            if (action === 'result') {
                if (info.status !== 'succeeded' || info.cancelled)
                    throw new PipelineToolError('not_terminal');
                return run.result({ timeoutMs: left(), signal });
            }
            let approval: PipelineToolApproval | null = null;
            let state: PipelineToolStatus['state'] = info.cancelled ? 'cancelled' : info.status === 'succeeded' ? 'succeeded' : ['failed', 'dead'].includes(info.status) ? 'failed' : 'running';
            if (state === 'running' && d.gateStore && reviewURL) {
                const rows = await stored(() => d.gateStore!.list(run.checkpoint.run_id, actor.owner_id, signal), check);
                if (rows.length > 100)
                    throw new PipelineToolError('limit');
                for (const raw of rows) {
                    const r = parsePipelineGateRecord(raw), i = r.intent;
                    if (i.workflow_run_id !== run.checkpoint.run_id || i.owner_id !== actor.owner_id || i.pipeline_sha256 !== contract.sha256 || i.input_sha256 !== run.checkpoint.input_sha256 || jobValueHash(i.scope) !== jobValueHash(scope))
                        throw new PipelineToolError('receipt_conflict');
                    if (r.decision === null) {
                        const resolved = await stored(() => d.gateStore!.resolve(i.id, signal), check);
                        if (!resolved)
                            throw new PipelineToolError('receipt_conflict');
                        const current = parsePipelineGateRecord(resolved);
                        if (jobValueHash(current.intent) !== jobValueHash(i))
                            throw new PipelineToolError('receipt_conflict');
                        if (current.decision === null) {
                            if (approval)
                                throw new PipelineToolError('receipt_conflict');
                            const url = new URL(reviewURL);
                            url.searchParams.set('run', run.checkpoint.run_id);
                            url.searchParams.set('gate', i.id);
                            approval = { kind: i.kind, id: i.id, name: i.name, expires_at: current.expires_at, review_url: url.href };
                            state = i.kind === 'approval' ? 'awaiting_approval' : 'awaiting_input';
                        }
                    }
                }
            }
            return jobSnapshot({ state, receipt, steps: info.steps, approval });
        });
    }
    return Object.freeze({ name: d.name, description: d.description, contract,
        start(input, requestId, access, settings) {
            const captured = contract.input(input), request = { request_id: pipelineToolRequestId(requestId), input: captured };
            return execute(settings, async (signal, check, left) => {
                const { actor, binding } = await prepare(request, access, 'start', signal, check), trigger = { version: 1 as const, kind: 'tool' as const, identity: binding.identity };
                const intent: PipelineToolIntent = jobSnapshot({ ...binding, input_sha256: jobValueHash({ version: 1, scope, owner_id: actor.owner_id, contract_sha256: contract.sha256, input: captured, trigger }) });
                const reservation = await stored(() => d.store.reserve(intent, signal), check), receipt = bound(binding, reservation.receipt);
                if (typeof reservation.created !== 'boolean' || jobValueHash(receipt.intent) !== jobValueHash(intent))
                    throw new PipelineToolError('receipt_conflict');
                if (!reservation.created)
                    return startResult(receipt, true);
                if (receipt.checkpoint)
                    throw new PipelineToolError('receipt_conflict');
                let checkpoint: PipelineCheckpoint;
                try {
                    checkpoint = (await client(actor.owner_id).start(captured, { timeoutMs: left(), signal, idempotencyKey: intent.idempotency_key, trigger })).checkpoint;
                    check();
                }
                catch {
                    check();
                    return startResult(receipt, false);
                }
                matchPipelineToolCheckpoint(intent, checkpoint);
                const completed = bound(binding, await stored(() => d.store.complete(intent, checkpoint, signal), check));
                if (jobValueHash(completed.checkpoint) !== jobValueHash(checkpoint))
                    throw new PipelineToolError('receipt_conflict');
                return startResult(completed, false);
            });
        },
        inspect: (id, access, settings) => operation(id, access, 'inspect', settings) as Promise<PipelineToolStatus | null>,
        result: (id, access, settings) => operation(id, access, 'result', settings) as Promise<O>,
        cancel: (id, access, settings) => operation(id, access, 'cancel', settings) as Promise<PipelineToolStatus>,
        reconcile: (id, runId, access, settings) => operation(id, access, 'reconcile', settings, runId) as Promise<PipelineToolStart>,
    } satisfies PipelineTool<I, O, A>);
}
