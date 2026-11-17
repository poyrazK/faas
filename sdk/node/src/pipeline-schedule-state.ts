/** Typed fixed inputs for native, default-deployment workflow schedules. */
import { JobContract, jobSnapshot, jobValueHash } from './job-runtime.js';
import { eventPipelineScope } from './event-pipeline-state.js';
import { outboxInstant } from './event-outbox-state.js';
import { pipelineName, type PipelineScope, type PipelineEnvelope } from './pipeline-state.js';
import { fileEventUUID } from './file-events.js';
import type { WorkflowTriggerSpec } from './generated/models/WorkflowTriggerSpec.js';
import type { WorkflowScheduleOccurrenceResponse } from './generated/models/WorkflowScheduleOccurrenceResponse.js';
import type { WorkflowSchedulePreviewResponse } from './generated/models/WorkflowSchedulePreviewResponse.js';
import type { WorkflowScheduleResponse } from './generated/models/WorkflowScheduleResponse.js';
import type { WorkflowScheduleReplayOutcome } from './generated/models/WorkflowScheduleReplayOutcome.js';
export type PipelineScheduleCode = 'invalid_input' | 'unauthorized' | 'scope_mismatch' | 'deployment_changed' | 'definition_changed' | 'not_found' | 'invalid_response' | 'history_limit' | 'request_failed' | 'replay_unknown' | 'deadline' | 'cancelled';
export class PipelineScheduleError extends Error {
    readonly occurrenceIds: readonly string[];
    constructor(readonly code: PipelineScheduleCode, ids: readonly string[] = []) { super('pipeline schedule: ' + code); this.name = 'PipelineScheduleError'; this.occurrenceIds = Object.freeze([...ids]); }
}
export interface PipelineScheduleOptions {
    scope: PipelineScope;
    ownerId: string;
    cron: string;
    timezone?: string;
    overlap?: 'skip' | 'allow';
    catchUp?: 'skip' | 'latest';
    catchUpWindowMinutes?: number;
    enabled?: boolean;
    environment?: 'default';
}
export interface PipelineSchedule<I, O> {
    readonly contract: JobContract<I, O>;
    readonly scope: PipelineScope;
    readonly ownerId: string;
    readonly identity: string;
    readonly trigger: WorkflowTriggerSpec & {
        type: 'schedule';
        input: PipelineEnvelope<I>;
    };
}
export interface PipelineScheduleHistory {
    readonly occurrences: readonly WorkflowScheduleOccurrenceResponse[];
    readonly next_cursor?: string;
}
export interface PipelineScheduleInspection {
    readonly runtime_enabled: boolean;
    readonly unavailable_reason?: string;
    readonly schedule: WorkflowScheduleResponse;
}
export function scheduleInteger(v: unknown, lo: number, hi: number): number { if (typeof v !== 'number' || !Number.isSafeInteger(v) || v < lo || v > hi)
    throw new PipelineScheduleError('invalid_input'); return v; }
export function scheduleTime(v: unknown): bigint { try {
    return outboxInstant(v);
}
catch {
    throw new PipelineScheduleError('invalid_response');
} }
export function scheduleUUID(v: unknown): v is string { return fileEventUUID(v); }
function cron(v: unknown): string { if (typeof v !== 'string' || v.length > 256)
    throw new PipelineScheduleError('invalid_input'); const fields = v.trim().split(/\s+/); if (fields.length !== 5 || fields.some(f => !f || !/^[A-Za-z0-9*,/\-?#]+$/.test(f)))
    throw new PipelineScheduleError('invalid_input'); return fields.join(' '); }
function zone(v: unknown): string { if (typeof v !== 'string' || !v || v.length > 128 || !/^[A-Za-z0-9_+/-]+$/.test(v))
    throw new PipelineScheduleError('invalid_input'); try {
    new Intl.DateTimeFormat('en', { timeZone: v });
    return v;
}
catch {
    throw new PipelineScheduleError('invalid_input');
} }
function minutes(v: unknown): number { if (typeof v !== 'string' || !/^(?:\d+(?:\.\d+)?[hms])+$/.test(v))
    throw new PipelineScheduleError('invalid_response'); let seconds = 0; for (const m of v.matchAll(/(\d+(?:\.\d+)?)([hms])/g))
    seconds += Number(m[1]) * ({ h: 3600, m: 60, s: 1 }[m[2]!]!); if (!Number.isSafeInteger(seconds) || seconds < 60 || seconds > 86400)
    throw new PipelineScheduleError('invalid_response'); return seconds / 60; }
export function scheduleCalendar(t: any) { if (!t || t.type !== 'schedule' || (t.tenant_configurable !== undefined && t.tenant_configurable !== false) || t.source || t.event_type || t.filter)
    throw new PipelineScheduleError('definition_changed'); const policy = t.catch_up ?? 'skip', overlap = t.overlap ?? 'skip', enabled = t.enabled ?? true; if (!['skip', 'latest'].includes(policy) || !['skip', 'allow'].includes(overlap) || typeof enabled !== 'boolean' || policy === 'skip' && t.catch_up_window !== undefined)
    throw new PipelineScheduleError('invalid_input'); return { schedule: cron(t.schedule), timezone: zone(t.timezone ?? 'UTC'), overlap, enabled, catch_up: policy, ...(policy === 'latest' ? { window_minutes: minutes(t.catch_up_window ?? '60m') } : {}) }; }
export function definePipelineSchedule<I, O>(contract: JobContract<I, O>, input: I, options: PipelineScheduleOptions): PipelineSchedule<I, O> {
    try {
        const scope = eventPipelineScope(options.scope);
        if (!pipelineName(options.ownerId) || options.environment !== undefined && options.environment !== 'default')
            throw new Error();
        const policy = options.catchUp ?? 'skip';
        if (policy === 'skip' && options.catchUpWindowMinutes !== undefined)
            throw new Error();
        const trigger = { type: 'schedule' as const, schedule: cron(options.cron), timezone: zone(options.timezone ?? 'UTC'), overlap: options.overlap ?? 'skip', catch_up: policy, enabled: options.enabled ?? true, ...(policy === 'latest' ? { catch_up_window: scheduleInteger(options.catchUpWindowMinutes ?? 60, 1, 1440) + 'm' } : {}) };
        scheduleCalendar(trigger);
        const captured = new JobContract<I, O>(contract.spec), bare = { version: 1 as const, scope, owner_id: options.ownerId, contract_sha256: captured.sha256, input: captured.input(input) };
        const identity = jobValueHash(['gregale.pipeline-schedule.v1', captured.spec.name, bare, scheduleCalendar(trigger)]);
        const value = jobSnapshot({ ...trigger, input: { ...bare, trigger: { version: 1 as const, kind: 'schedule' as const, identity } } });
        if (Buffer.byteLength(JSON.stringify(value)) > 1048576)
            throw new Error();
        return Object.freeze({ contract: captured, scope: jobSnapshot(scope), ownerId: options.ownerId, identity, trigger: value });
    }
    catch {
        throw new PipelineScheduleError('invalid_input');
    }
}
export function scheduleMetadata(raw: any, s: PipelineSchedule<any, any>, deployment: string): WorkflowScheduleResponse {
    if (!raw || raw.workflow_name !== s.contract.spec.name)
        throw new PipelineScheduleError('not_found');
    if (raw.deployment_id !== deployment)
        throw new PipelineScheduleError('deployment_changed');
    const actual = scheduleCalendar({ type: 'schedule', ...raw, catch_up: raw.catch_up ?? s.trigger.catch_up });
    if (jobValueHash(actual) !== jobValueHash(scheduleCalendar(s.trigger)))
        throw new PipelineScheduleError('definition_changed');
    const out: any = { workflow_name: raw.workflow_name, deployment_id: deployment, schedule: actual.schedule, timezone: actual.timezone, overlap: actual.overlap, enabled: actual.enabled, catch_up: actual.catch_up, ...(raw.catch_up_window !== undefined ? { catch_up_window: raw.catch_up_window } : {}) };
    for (const k of ['next_fire_at', 'last_evaluated_at', 'last_scheduled_for'])
        if (raw[k] !== undefined) {
            scheduleTime(raw[k]);
            out[k] = raw[k];
        }
    if (raw.last_status !== undefined) {
        if (!['armed', 'started', 'skipped_overlap', 'skipped_quota'].includes(raw.last_status))
            throw new PipelineScheduleError('invalid_response');
        out.last_status = raw.last_status;
    }
    if (raw.last_run_id !== undefined) {
        if (!scheduleUUID(raw.last_run_id))
            throw new PipelineScheduleError('invalid_response');
        out.last_run_id = raw.last_run_id;
    }
    return jobSnapshot(out);
}
export function scheduleOccurrence(r: any, app: string): WorkflowScheduleOccurrenceResponse {
    if (!r || !scheduleUUID(r.id) || r.app_id !== app || !scheduleUUID(r.deployment_id) || typeof r.workflow_name !== 'string' || !['started', 'skipped_overlap', 'skipped_quota'].includes(r.status))
        throw new PipelineScheduleError('invalid_response');
    const out: any = { id: r.id, app_id: r.app_id, deployment_id: r.deployment_id, workflow_name: r.workflow_name, status: r.status, scheduled_for: r.scheduled_for, evaluated_at: r.evaluated_at };
    scheduleTime(r.scheduled_for);
    scheduleTime(r.evaluated_at);
    for (const k of ['platform_tenant_id', 'run_id', 'replay_run_id'])
        if (r[k] != null) {
            if (!scheduleUUID(r[k]))
                throw new PipelineScheduleError('invalid_response');
            out[k] = r[k];
        }
    if (r.status === 'started' && out.replay_run_id || r.status !== 'started' && out.run_id)
        throw new PipelineScheduleError('invalid_response');
    if (r.replayed_at !== undefined) {
        scheduleTime(r.replayed_at);
        out.replayed_at = r.replayed_at;
    }
    return jobSnapshot(out);
}
export function schedulePreview(r: any, s: PipelineSchedule<any, any>, deployment: string, count: number): WorkflowSchedulePreviewResponse {
    scheduleMetadata({ ...r, catch_up: r?.catch_up?.policy, catch_up_window: r?.catch_up?.window }, s, deployment);
    scheduleTime(r.observed_at);
    scheduleTime(r.evaluation_at);
    const d = r.dst_behavior, c = r.catch_up;
    if (!d || !['fixed_wall_time', 'interval'].includes(d.mode) || !['shift_to_first_valid_minute', 'follow_cron_interval'].includes(d.spring_gap) || !['run_once_at_first_occurrence', 'repeat_as_clock_falls_back'].includes(d.fall_fold) || !c || !['disabled', 'first_evaluation_arms', 'no_new_evaluation', 'run_current_fire', 'coalesce_latest', 'skip_missed', 'outside_catch_up_window', 'no_due_occurrence'].includes(c.outcome) || typeof c.missed_occurrences_not_recovered !== 'boolean' || typeof c.missed_outside_window !== 'boolean')
        throw new PipelineScheduleError('invalid_response');
    const fire = (f: any) => { if (!f || scheduleTime(f.scheduled_for) !== scheduleTime(f.local_time) || f.dst_adjustment !== undefined && f.dst_adjustment !== 'shifted_forward')
        throw new PipelineScheduleError('invalid_response'); return { scheduled_for: f.scheduled_for, local_time: f.local_time, ...(f.dst_adjustment ? { dst_adjustment: f.dst_adjustment } : {}) }; };
    if (!Array.isArray(r.upcoming) || r.upcoming.length > count)
        throw new PipelineScheduleError('invalid_response');
    let last = scheduleTime(r.evaluation_at);
    for (const f of r.upcoming) {
        const n = scheduleTime(f.scheduled_for);
        if (n <= last)
            throw new PipelineScheduleError('invalid_response');
        last = n;
    }
    if (c.selected && scheduleTime(c.selected.scheduled_for) > scheduleTime(r.evaluation_at))
        throw new PipelineScheduleError('invalid_response');
    const catchup = { policy: c.policy, outcome: c.outcome, eligible_occurrences: scheduleInteger(c.eligible_occurrences, 0, 2147483647), coalesced_occurrences: scheduleInteger(c.coalesced_occurrences, 0, 2147483647), missed_occurrences_not_recovered: c.missed_occurrences_not_recovered, missed_outside_window: c.missed_outside_window, ...(c.window !== undefined ? { window: c.window } : {}), ...(c.since !== undefined ? (scheduleTime(c.since), { since: c.since }) : {}), ...(c.selected ? { selected: fire(c.selected) } : {}) };
    return jobSnapshot({ workflow_name: r.workflow_name, deployment_id: deployment, schedule: r.schedule, timezone: r.timezone, overlap: r.overlap, enabled: r.enabled, observed_at: r.observed_at, evaluation_at: r.evaluation_at, dst_behavior: { mode: d.mode, spring_gap: d.spring_gap, fall_fold: d.fall_fold }, upcoming: r.upcoming.map(fire), catch_up: catchup });
}
export function scheduleReplayOutcomes(value: any, selected: readonly WorkflowScheduleOccurrenceResponse[], preview: boolean): readonly WorkflowScheduleReplayOutcome[] {
    if (!Array.isArray(value?.outcomes) || value.outcomes.length !== selected.length)
        throw new PipelineScheduleError('invalid_response');
    const seen = new Set<string>();
    let last: bigint | undefined;
    return jobSnapshot(value.outcomes.map((r: any) => {
        const old = selected.find(v => v.id === r.occurrence_id);
        if (!old || seen.has(r.occurrence_id) || r.platform_tenant_id != null || r.workflow_name !== undefined && r.workflow_name !== old.workflow_name || r.scheduled_for !== undefined && scheduleTime(r.scheduled_for) !== scheduleTime(old.scheduled_for) || !['eligible', 'replayed', 'already_replayed', 'occurrence_not_found', 'not_skipped', 'history_not_replayable', 'deployment_changed', 'definition_changed', 'schedule_disabled', 'overlap_active', 'quota_full', 'tenant_unavailable', 'target_unavailable', 'plan_unavailable'].includes(r.outcome) || preview && r.outcome === 'replayed' || !preview && r.outcome === 'eligible')
            throw new PipelineScheduleError('invalid_response');
        seen.add(r.occurrence_id);
        const instant = scheduleTime(old.scheduled_for);
        if (last !== undefined && instant < last)
            throw new PipelineScheduleError('invalid_response');
        last = instant;
        if ((['replayed', 'already_replayed'].includes(r.outcome)) !== scheduleUUID(r.replay_run_id))
            throw new PipelineScheduleError('invalid_response');
        return { occurrence_id: old.id, workflow_name: old.workflow_name, scheduled_for: old.scheduled_for, outcome: r.outcome, ...(r.replay_run_id ? { replay_run_id: r.replay_run_id } : {}) };
    }));
}
export type PipelineSchedulePreview = WorkflowSchedulePreviewResponse;
export type PipelineScheduleOccurrence = WorkflowScheduleOccurrenceResponse;
export type PipelineScheduleReplayOutcome = WorkflowScheduleReplayOutcome;

/** @internal Capture a coherent generated definition instead of trusting independent owner/input fields. */
export function capturePipelineSchedule<I,O>(s:PipelineSchedule<I,O>):PipelineSchedule<I,O>{
    try {
        const c=scheduleCalendar(s.trigger);
        const captured=definePipelineSchedule(s.contract,s.trigger.input.input,{scope:s.scope,ownerId:s.ownerId,cron:c.schedule,timezone:c.timezone,overlap:c.overlap,enabled:c.enabled,catchUp:c.catch_up,...(c.window_minutes!==undefined?{catchUpWindowMinutes:c.window_minutes}:{})});
        if(captured.identity!==s.identity||jobValueHash(captured.trigger.input)!==jobValueHash(s.trigger.input))throw new Error();
        return captured;
    } catch { throw new PipelineScheduleError('invalid_input'); }
}
