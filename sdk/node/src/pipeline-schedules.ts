/** Native schedules remain the only clock/admission owner. Every operation reauthorizes. */
import { JobError, jobScope, jobSnapshot, jobValueHash } from './job-runtime.js';
import { createJobPipelineClient, type PipelineRunOptions, type TypedPipelineRun } from './job-pipeline-client.js';
import { PipelineScheduleError, definePipelineSchedule, capturePipelineSchedule, scheduleCalendar, scheduleMetadata, scheduleOccurrence, schedulePreview, scheduleReplayOutcomes, scheduleTime, scheduleUUID, scheduleInteger, type PipelineSchedule, type PipelineScheduleHistory, type PipelineScheduleInspection } from './pipeline-schedule-state.js';
import type { WorkflowSchedulePreviewResponse } from './generated/models/WorkflowSchedulePreviewResponse.js';
import type { WorkflowScheduleOccurrenceResponse } from './generated/models/WorkflowScheduleOccurrenceResponse.js';
import type { WorkflowScheduleReplayOutcome } from './generated/models/WorkflowScheduleReplayOutcome.js';
export { definePipelineSchedule, PipelineScheduleError };
export type * from './pipeline-schedule-state.js';
class RejectedRequest extends PipelineScheduleError { constructor() { super('request_failed'); } }
export type PipelineScheduleAction = 'inspect' | 'preview' | 'history' | 'preview_replay' | 'replay' | 'attach' | 'read_run' | 'cancel_run';
export interface PipelineScheduleClient<I, O, A> {
    inspect(access: A, options: PipelineRunOptions): Promise<PipelineScheduleInspection>;
    preview(access: A, options: PipelineRunOptions & {
        count?: number;
        at?: string;
        since?: string;
    }): Promise<WorkflowSchedulePreviewResponse>;
    history(access: A, options: PipelineRunOptions & {
        cursor?: string;
        limit?: number;
    }): Promise<PipelineScheduleHistory>;
    previewReplay(ids: readonly string[], access: A, options: PipelineRunOptions & {
        maxPages?: number;
    }): Promise<readonly WorkflowScheduleReplayOutcome[]>;
    replay(ids: readonly string[], access: A, options: PipelineRunOptions & {
        maxPages?: number;
    }): Promise<readonly WorkflowScheduleReplayOutcome[]>;
    attach(id: string, access: A, options: PipelineRunOptions & {
        maxPages?: number;
        replayed?: boolean;
    }): Promise<TypedPipelineRun<O>>;
}
export function createPipelineScheduleClient<I, O, A>(schedule: PipelineSchedule<I, O>, options: {
    deploymentId: string;
    token: string;
    fetch?: typeof fetch;
    authorize: (access: A, action: PipelineScheduleAction, signal: AbortSignal) => Promise<boolean>;
}): PipelineScheduleClient<I, O, A> {
    const s = capturePipelineSchedule(schedule), dep = options.deploymentId, token = options.token, authorize = options.authorize;
    if (!scheduleUUID(dep) || !token)
        throw new PipelineScheduleError('invalid_input');
    const fetcher = options.fetch ?? globalThis.fetch, base = s.scope.base_url, root = '/v1/apps/' + encodeURIComponent(s.scope.app_slug), name = s.contract.spec.name;
    type Request = (path: string, body?: unknown) => Promise<any>;
    async function execute<T>(access: A, action: PipelineScheduleAction, settings: PipelineRunOptions, work: (request: Request, signal: AbortSignal, remaining: () => number, dispatched: () => void) => Promise<T>, ids: readonly string[] = []): Promise<T> {
        let sent = false;
        const end = performance.now() + settings.timeoutMs;
        try {
            return await jobScope(settings.timeoutMs, settings.signal, async (signal) => {
                const remaining = () => { signal.throwIfAborted(); const left = Math.floor(end - performance.now()); if (left < 1)
                    throw new PipelineScheduleError('deadline'); return left; };
                remaining();
                if (await authorize(access, action, signal) !== true)
                    throw new PipelineScheduleError('unauthorized');
                remaining();
                const request: Request = async (path, body) => {
                    remaining();
                    const response = await fetcher(base + path, { method: body === undefined ? 'GET' : 'POST', redirect: 'manual', headers: { authorization: 'Bearer ' + token, accept: 'application/json', ...(body !== undefined ? { 'content-type': 'application/json' } : {}) }, ...(body !== undefined ? { body: JSON.stringify(body) } : {}), signal });
                    remaining();
                    if (!response.ok) {
                        void response.body?.cancel().catch(() => { });
                        if (response.status >= 400 && response.status < 500 && ![408, 429].includes(response.status))
                            throw new RejectedRequest();
                        throw new PipelineScheduleError('request_failed');
                    }
                    const reader = response.body?.getReader();
                    let length = 0;
                    const chunks: Uint8Array[] = [];
                    try {
                        if (reader)
                            for (;;) {
                                const p = await reader.read();
                                remaining();
                                if (p.done)
                                    break;
                                length += p.value.length;
                                if (length > 1048576)
                                    throw new PipelineScheduleError('invalid_response');
                                chunks.push(p.value);
                            }
                    }
                    finally {
                        if (reader) {
                            void reader.cancel().catch(() => { });
                            reader.releaseLock();
                        }
                    }
                    const value = JSON.parse(Buffer.concat(chunks).toString('utf8'));
                    if (!value || typeof value !== 'object' || Array.isArray(value))
                        throw new PipelineScheduleError('invalid_response');
                    return value;
                };
                const a = await request('/v1/account');
                if (a.id !== s.scope.account_id)
                    throw new PipelineScheduleError('scope_mismatch');
                const app = await request(root);
                if (app.id !== s.scope.app_id)
                    throw new PipelineScheduleError('scope_mismatch');
                const value = await work(request, signal, remaining, () => { remaining(); sent = true; });
                remaining();
                return value;
            });
        }
        catch (error) {
            if (sent && !(error instanceof RejectedRequest))
                throw new PipelineScheduleError('replay_unknown', ids);
            if (settings.signal?.aborted)
                throw new PipelineScheduleError('cancelled');
            if (error instanceof PipelineScheduleError)
                throw error;
            if (error instanceof JobError && error.code === 'deadline')
                throw new PipelineScheduleError('deadline');
            throw new PipelineScheduleError('request_failed');
        }
    }
    async function verifyBinding(request: Request): Promise<void> {
        const deployed = await request('/v1/deployments/' + dep);
        if (deployed.id !== dep || deployed.app_id !== s.scope.app_id || (deployed.scope ?? 'default') !== 'default')
            throw new PipelineScheduleError('scope_mismatch');
        const defs = deployed.workflows?.filter((d: any) => d.name === name);
        if (!Array.isArray(defs) || defs.length !== 1 || jobValueHash(scheduleCalendar(defs[0].trigger)) !== jobValueHash(scheduleCalendar(s.trigger)) || jobValueHash(defs[0].trigger.input) !== jobValueHash(s.trigger.input))
            throw new PipelineScheduleError('definition_changed');
    }
    async function verify(request: Request): Promise<PipelineScheduleInspection> {
        const listing = await request(root + '/workflows/schedules');
        if (typeof listing.runtime_enabled !== 'boolean' || !Array.isArray(listing.schedules))
            throw new PipelineScheduleError('invalid_response');
        const rows = listing.schedules.filter((r: any) => r.workflow_name === name);
        if (rows.length !== 1)
            throw new PipelineScheduleError('not_found');
        const current = scheduleMetadata(rows[0], s, dep);
        await verifyBinding(request);
        if (listing.unavailable_reason !== undefined && !['runtime_disabled', 'account_inactive', 'plan_not_allowed', 'maintenance', 'tenant_required'].includes(listing.unavailable_reason))
            throw new PipelineScheduleError('invalid_response');
        return jobSnapshot({ runtime_enabled: listing.runtime_enabled, ...(listing.unavailable_reason ? { unavailable_reason: listing.unavailable_reason } : {}), schedule: current });
    }
    async function page(request: Request, cursor?: string, limit = 100): Promise<PipelineScheduleHistory> {
        scheduleInteger(limit, 1, 200);
        if (cursor !== undefined && !scheduleUUID(cursor))
            throw new PipelineScheduleError('invalid_input');
        const q = new URLSearchParams({ limit: String(limit), ...(cursor ? { cursor } : {}) });
        const raw = await request(root + '/workflows/schedules/occurrences?' + q);
        if (!Array.isArray(raw.occurrences) || raw.occurrences.length > limit || raw.next_cursor !== undefined && (!scheduleUUID(raw.next_cursor) || raw.next_cursor === cursor))
            throw new PipelineScheduleError('invalid_response');
        const rows = raw.occurrences.map((v: any) => scheduleOccurrence(v, s.scope.app_id)) as WorkflowScheduleOccurrenceResponse[];
        const seen = new Set<string>();
        let last: bigint | undefined;
        for (const r of rows) {
            const t = scheduleTime(r.scheduled_for);
            if (seen.has(r.id) || last !== undefined && t > last)
                throw new PipelineScheduleError('invalid_response');
            seen.add(r.id);
            last = t;
        }
        return jobSnapshot({ occurrences: rows.filter(r => r.workflow_name === name && r.deployment_id === dep && r.platform_tenant_id == null), ...(raw.next_cursor ? { next_cursor: raw.next_cursor } : {}) });
    }
    function selection(ids: readonly string[]): readonly string[] { if (!Array.isArray(ids) || ids.length < 1 || ids.length > 20 || ids.some(id => !scheduleUUID(id)) || new Set(ids).size !== ids.length)
        throw new PipelineScheduleError('invalid_input'); return Object.freeze([...ids]); }
    async function find(request: Request, ids: readonly string[], maximum = 5): Promise<WorkflowScheduleOccurrenceResponse[]> { scheduleInteger(maximum, 1, 100); const found = new Map<string, WorkflowScheduleOccurrenceResponse>(), cursors = new Set<string>(); let cursor: string | undefined; for (let n = 0; n < maximum; n++) {
        const value = await page(request, cursor, 200);
        for (const row of value.occurrences)
            if (ids.includes(row.id)) {
                if (found.has(row.id))
                    throw new PipelineScheduleError('invalid_response');
                found.set(row.id, row);
            }
        if (found.size === ids.length)
            return ids.map(id => found.get(id)!);
        if (!value.next_cursor)
            throw new PipelineScheduleError('not_found');
        if (cursors.has(value.next_cursor))
            throw new PipelineScheduleError('invalid_response');
        cursors.add(value.next_cursor);
        cursor = value.next_cursor;
    } throw new PipelineScheduleError('history_limit'); }
    const replay = (ids: readonly string[], access: A, settings: PipelineRunOptions & {
        maxPages?: number;
    }, preview: boolean) => { const saved = selection(ids); return execute(access, preview ? 'preview_replay' : 'replay', settings, async (request, signal, remaining, dispatched) => { await verify(request); const rows = await find(request, saved, settings.maxPages); remaining(); if (!preview)
        dispatched(); const result = await request(root + '/workflows/schedules/occurrences:' + (preview ? 'replay-preview' : 'replay'), { occurrence_ids: saved }); return scheduleReplayOutcomes(result, rows, preview); }, saved); };
    return Object.freeze({
        inspect: (access, settings) => execute(access, 'inspect', settings, request => verify(request)),
        preview: (access, settings) => execute(access, 'preview', settings, async (request) => { const count = scheduleInteger(settings.count ?? 5, 1, 20); for (const t of [settings.at, settings.since])
            if (t !== undefined)
                scheduleTime(t); await verify(request); const q = new URLSearchParams({ count: String(count), ...(settings.at ? { at: settings.at } : {}), ...(settings.since ? { since: settings.since } : {}) }); return schedulePreview(await request(root + '/workflows/schedules/' + encodeURIComponent(name) + '/preview?' + q), s, dep, count); }),
        history: (access, settings) => execute(access, 'history', settings, async (request) => { await verifyBinding(request); return page(request, settings.cursor, settings.limit); }),
        previewReplay: (ids, access, settings) => replay(ids, access, settings, true), replay: (ids, access, settings) => replay(ids, access, settings, false),
        attach: (id, access, settings) => { const ids = selection([id]); return execute(access, 'attach', settings, async (request, signal, remaining) => { await verifyBinding(request); const [row] = await find(request, ids, settings.maxPages); const run = settings.replayed ? row!.replay_run_id : row!.run_id; if (!scheduleUUID(run))
            throw new PipelineScheduleError('not_found'); const record = await request('/v1/workflows/runs/' + run); if (record.id !== run || record.app_id !== s.scope.app_id || record.deployment_id !== dep || record.workflow_name !== name || record.platform_tenant_id != null || jobValueHash(record.input) !== jobValueHash(s.trigger.input) || scheduleTime(record.scheduled_for) !== scheduleTime(row!.scheduled_for))
            throw new PipelineScheduleError('scope_mismatch'); const typed = createJobPipelineClient({ baseURL: base, token, scope: s.scope, ownerId: s.ownerId, contract: s.contract, fetch: fetcher }); const attached = await typed.resumeById(run, { timeoutMs: remaining(), signal }); if (attached.checkpoint.input_sha256 !== jobValueHash(s.trigger.input))
            throw new PipelineScheduleError('scope_mismatch'); return Object.freeze({ checkpoint: attached.checkpoint, inspect: (next: PipelineRunOptions) => execute(access, 'read_run', next, async (_r, active, left) => attached.inspect({ timeoutMs: left(), signal: active })), result: (next: PipelineRunOptions) => execute(access, 'read_run', next, async (_r, active, left) => attached.result({ timeoutMs: left(), signal: active })), cancel: (next: PipelineRunOptions) => execute(access, 'cancel_run', next, async (_r, active, left) => attached.cancel({ timeoutMs: left(), signal: active })) }); }); },
    } satisfies PipelineScheduleClient<I, O, A>);
}
