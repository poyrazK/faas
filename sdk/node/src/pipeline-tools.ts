/** Typed business pipelines exposed through durable, currently authorized tools. */
import { JobContract, JobError, jobScope, jobSnapshot, jobValueHash } from './job-runtime.js';
import { createJobPipelineClient, type PipelineCheckpoint, type PipelineRunOptions } from './job-pipeline-client.js';
import { eventPipelineScope } from './event-pipeline-state.js';
import { parsePipelineGateRecord, type PipelineGateStore } from './pipeline-gates.js';
import { PipelineError, isPinnedPipelineSourceFailure, pipelineName, type PipelineScope, type PinnedPipelineSource, type PinnedPipelineSourceFailure } from './pipeline-state.js';
import { PipelineToolError, pipelineToolName, pipelineToolRequestId, pipelineToolIdentity, parsePipelineToolReceipt, samePipelineToolRequest, matchPipelineToolCheckpoint, type PipelineToolActor, type PipelineToolAction, type PipelineToolIntent, type PipelineToolReceipt, type PipelineToolStore } from './pipeline-tool-state.js';
import { checkPipelinePreview, pipelinePreviewExpired, type PipelineStartPreview, type PipelineStartPreviewPolicy } from './pipeline-start-preview.js';
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
    readonly source_failure?: PinnedPipelineSourceFailure;
}
export type PipelineToolStartMatch =
    | Readonly<{ state: 'found'; request_id: string; run_id: string }>
    | Readonly<{ state: 'start_unknown'; request_id: string }>;
export interface PipelineToolResult<O> {
    readonly receipt: PipelineToolReceipt;
    readonly output: O;
}
export interface PipelineTool<I, O, A> {
    readonly name: string;
    readonly description: string;
    readonly contract: JobContract<I, O>;
    start(input: I, requestId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStart>;
    replayStart?(input: I, requestId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStart>;
    previewStart?<P>(input: I, id: string, access: A, options: PipelineRunOptions, policy: PipelineStartPreviewPolicy<I, P, A>): Promise<PipelineStartPreview<P>>;
    startPreviewed?(input: I, id: string, access: A, options: PipelineRunOptions, policy: PipelineStartPreviewPolicy<I, unknown, A>, token?: string): Promise<PipelineToolStart>;
    inspect(requestId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStatus | null>;
    result(requestId: string, access: A, options: PipelineRunOptions): Promise<O>;
    cancel(requestId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStatus>;
    reconcile(requestId: string, runId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStart>;
    /** Verify an accepted run for the receipt using current reconciliation authority. */
    findAcceptedRun?(requestId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStartMatch>;
}
export interface PipelineToolWithResult<I, O, A> extends PipelineTool<I, O, A> {
    resultWithReceipt(requestId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolResult<O>>;
}
export interface PipelineToolWithStartLookup<I, O, A> extends PipelineTool<I, O, A> {
    findAcceptedRun(requestId: string, access: A, options: PipelineRunOptions): Promise<PipelineToolStartMatch>;
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
export function definePipelineTool<I, O, A>(source: JobContract<I, O>, definition: PipelineToolDefinition<I, A>): PipelineToolWithStartLookup<I, O, A> & PipelineToolWithResult<I, O, A> {
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
    async function operation(requestId: string, access: A, action: Exclude<PipelineToolAction, 'start'> | 'find_start', settings: PipelineRunOptions, runId?: string): Promise<PipelineToolStatus | PipelineToolResult<O> | PipelineToolStart | PipelineToolStartMatch | null> {
        const request = { request_id: pipelineToolRequestId(requestId) };
        return execute(settings, async (signal, check, left) => {
            const authorization = action === 'find_start' ? 'reconcile' : action;
            const { actor, binding } = await prepare(request, access, authorization, signal, check), raw = await stored(() => d.store.load(binding.identity, signal), check);
            if (!raw) {
                if (action === 'inspect')
                    return null;
                throw new PipelineToolError('receipt_conflict');
            }
            let receipt = bound(binding, raw);
            if (action === 'reconcile' || action === 'find_start') {
                const native = client(actor.owner_id), settings = { timeoutMs: left(), signal };
                let checkpoint: PipelineCheckpoint | null;
                try {
                    const id = action === 'reconcile' ? pipelineToolRequestId(runId) : receipt.checkpoint?.run_id;
                    checkpoint = id
                        ? (await native.resumeById(id, settings)).checkpoint
                        : await native.findToolStart(receipt.intent.identity, settings);
                } catch (error) {
                    if (action === 'find_start' && error instanceof PipelineError && ['receipt_conflict', 'scope_mismatch'].includes(error.code))
                        throw new PipelineToolError('receipt_conflict');
                    throw error;
                }
                check();
                // Recheck application authority before exposing a candidate or attaching it.
                const latest = await prepare(request, access, 'reconcile', signal, check);
                if (jobValueHash(latest.binding) !== jobValueHash(binding)) throw new PipelineToolError('unauthorized');
                if (checkpoint) matchPipelineToolCheckpoint(receipt.intent, checkpoint);
                if (action === 'find_start') return checkpoint
                    ? jobSnapshot({ state: 'found' as const, request_id: request.request_id, run_id: checkpoint.run_id })
                    : jobSnapshot({ state: 'start_unknown' as const, request_id: request.request_id });
                if (!checkpoint) throw new PipelineToolError('receipt_conflict');
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
                const output = await run.result({ timeoutMs: left(), signal });
                return { receipt, output };
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
            const failure = info.failure?.code;
            return jobSnapshot({ state, receipt, steps: info.steps, approval,
                ...(state === 'failed' && isPinnedPipelineSourceFailure(failure) && { source_failure: failure }),
            });
        });
    }
    async function prepareStart(request: PipelineToolRequest<I>, access: A, signal: AbortSignal, check: () => void) {
        const { actor, binding } = await prepare(request, access, 'start', signal, check);
        const trigger = { version: 1 as const, kind: 'tool' as const, identity: binding.identity };
        const envelope = { version: 1, scope, owner_id: actor.owner_id, contract_sha256: contract.sha256, input: request.input, trigger };
        const intent: PipelineToolIntent = jobSnapshot({ ...binding, input_sha256: jobValueHash(envelope) });
        return { actor, binding, intent, trigger, envelope };
    }
    function replay(intent: PipelineToolIntent, receipt: PipelineToolReceipt) {
        if (!samePipelineToolRequest(intent, receipt.intent))
            throw new PipelineToolError('receipt_conflict');
        return startResult(receipt, true);
    }
    function start(input: I, requestId: string, access: A, settings: PipelineRunOptions, replayOnly: boolean,
        confirmation?: { policy: PipelineStartPreviewPolicy<I, unknown, A>; token: string | undefined }) {
        const captured = contract.input(input), request = { request_id: pipelineToolRequestId(requestId), input: captured };
        return execute(settings, async (signal, check, left) => {
            const { actor, binding, intent, trigger, envelope } = await prepareStart(request, access, signal, check);
            let pinnedSource: PinnedPipelineSource | undefined;
            if (replayOnly || confirmation) {
                const raw = await stored(() => d.store.load(binding.identity, signal), check);
                if (raw)
                    return replay(intent, bound(binding, raw));
                if (replayOnly)
                    throw new PipelineToolError('revision_unavailable');
            }
            if (confirmation) {
                const checked = await checkPipelinePreview(confirmation.policy, intent, captured, access,
                    { timeoutMs: left(), signal }, confirmation);
                const current = await prepareStart(request, access, signal, check);
                if (pipelinePreviewExpired(checked.preview.expires_at) || current.intent.input_sha256 !== intent.input_sha256 || current.intent.identity !== intent.identity)
                    throw new PipelineToolError('preview_stale');
                pinnedSource = checked.pinnedSource;
            }
            const admitted: PipelineToolIntent = pinnedSource
                ? jobSnapshot({ ...intent, request_sha256: intent.input_sha256, input_sha256: jobValueHash({ ...envelope, pinned_source: pinnedSource }) })
                : intent;
            const reservation = await stored(() => d.store.reserve(admitted, signal), check), receipt = bound(binding, reservation.receipt);
            if (typeof reservation.created !== 'boolean')
                throw new PipelineToolError('receipt_conflict');
            if (!reservation.created)
                return replay(intent, receipt);
            if (receipt.checkpoint || jobValueHash(receipt.intent) !== jobValueHash(admitted))
                throw new PipelineToolError('receipt_conflict');
            let checkpoint: PipelineCheckpoint;
            try {
                checkpoint = (await client(actor.owner_id).start(captured, { timeoutMs: left(), signal, idempotencyKey: admitted.idempotency_key, trigger, ...(pinnedSource && { pinnedSource }) })).checkpoint;
                check();
            }
            catch {
                check();
                return startResult(receipt, false);
            }
            matchPipelineToolCheckpoint(admitted, checkpoint);
            const completed = bound(admitted, await stored(() => d.store.complete(admitted, checkpoint, signal), check));
            if (jobValueHash(completed.checkpoint) !== jobValueHash(checkpoint))
                throw new PipelineToolError('receipt_conflict');
            return startResult(completed, false);
        });
    }
    const resultWithReceipt = (id: string, access: A, settings: PipelineRunOptions) =>
        operation(id, access, 'result', settings) as Promise<PipelineToolResult<O>>;
    return Object.freeze({ name: d.name, description: d.description, contract,
        start: (input, id, access, settings) => start(input, id, access, settings, false),
        replayStart: (input, id, access, settings) => start(input, id, access, settings, true),
        previewStart(input, id, access, settings, policy) {
            const captured = contract.input(input), request = { request_id: pipelineToolRequestId(id), input: captured };
            return execute(settings, async (signal, check, left) => {
                const { intent } = await prepareStart(request, access, signal, check);
                const { preview } = await checkPipelinePreview(policy, intent, captured, access, { timeoutMs: left(), signal });
                const current = await prepareStart(request, access, signal, check);
                if (pipelinePreviewExpired(preview.expires_at) || current.intent.input_sha256 !== intent.input_sha256 || current.intent.identity !== intent.identity)
                    throw new PipelineToolError('preview_stale');
                return jobSnapshot(preview);
            });
        },
        startPreviewed: (input, id, access, settings, policy, token) =>
            start(input, id, access, settings, false, { policy, token }),
        inspect: (id, access, settings) => operation(id, access, 'inspect', settings) as Promise<PipelineToolStatus | null>,
        async result(id, access, settings) { return (await resultWithReceipt(id, access, settings)).output; },
        resultWithReceipt,
        cancel: (id, access, settings) => operation(id, access, 'cancel', settings) as Promise<PipelineToolStatus>,
        reconcile: (id, runId, access, settings) => operation(id, access, 'reconcile', settings, runId) as Promise<PipelineToolStart>,
        findAcceptedRun: (id, access, settings) => operation(id, access, 'find_start', settings) as Promise<PipelineToolStartMatch>,
    } satisfies PipelineToolWithStartLookup<I, O, A> & PipelineToolWithResult<I, O, A>);
}
