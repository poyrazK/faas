/** Read-only verification of explicit project deployment identities. */
import type { ClientConfig } from './client-instance.js';
import { scopedRequest } from './request-scope.js';
import { snapshot } from './deployment-waiters.js';
import { loadProjectApplicationState, type ProjectApplicationResult } from './project-workflows.js';
export type VerificationOutcome = 'verified' | 'failed' | 'blocked' | 'inconclusive';
export type VerificationCode = 'verified' | 'not_observed' | 'complete' | 'release_blocked' | 'release_failed' | 'release_pending' | 'deployment_identity_unavailable' | 'identity_mismatch' | 'invalid_evidence' | 'response_limit' | 'request_limit' | 'deadline' | 'evidence_unavailable' | 'stale_evidence' | 'deployment_not_serving' | 'health_failed' | 'health_unknown' | 'smoke_failed' | 'smoke_inconclusive' | 'smoke_required' | 'progress_failed';
export interface VerificationWorkload {
    readonly slug: string;
    readonly app_id: string | null;
    readonly deployment_id: string | null;
}
/** Portable resume document. Contains identities only; never credentials. */
export interface ProjectVerificationTarget {
    readonly version: 1;
    readonly base_url: string;
    readonly project_slug: string;
    readonly environment: string | null;
    readonly workloads: readonly VerificationWorkload[];
}
export interface VerificationCheckResult {
    readonly outcome: 'verified' | 'failed' | 'inconclusive';
    /** Required for verified/failed evidence: identity actually observed by the check. */
    readonly deployment_id?: string;
}
export interface VerificationCheckContext extends VerificationWorkload {
    readonly signal: AbortSignal;
    readonly remainingMs: number;
}
export interface VerificationCheck {
    readonly name: string;
    readonly workload: string;
    /** Read-only, repeatable probe. Use your application's credentials, if needed. */
    readonly run: (context: VerificationCheckContext) => VerificationCheckResult | Promise<VerificationCheckResult>;
}
export interface VerificationCheckEvidence {
    readonly name: string;
    readonly outcome: VerificationCheckResult['outcome'];
    readonly code: 'check_observed' | 'check_identity_unconfirmed' | 'check_unavailable';
}
export interface VerificationHealth {
    readonly status: string;
    readonly phase: string;
    readonly evaluated_at: string | null;
    readonly metrics_as_of: string | null;
    readonly fresh: boolean;
    readonly serving_deployment_ids: readonly string[];
}
export interface VerificationTimeline {
    readonly phase: string;
    readonly status: string;
    readonly traffic_percent: number;
    readonly event_count: number;
    readonly events: readonly {
        readonly kind: string;
        readonly at: string;
    }[];
    readonly truncated: boolean;
}
export interface VerificationWorkloadReport extends VerificationWorkload {
    readonly outcome: VerificationOutcome;
    readonly code: VerificationCode;
    readonly polls: number;
    readonly timeline?: VerificationTimeline;
    readonly health?: VerificationHealth;
    readonly checks: readonly VerificationCheckEvidence[];
    readonly request_ids: readonly string[];
    /** Opt-in, caller-redacted failure reason and log excerpts. */
    readonly diagnostics: readonly string[];
    readonly diagnostics_truncated: boolean;
    readonly next_actions: readonly string[];
}
export interface ProjectVerificationReport {
    readonly version: 1;
    readonly target: ProjectVerificationTarget;
    readonly outcome: VerificationOutcome;
    readonly code: VerificationCode;
    readonly requests: number;
    readonly workloads: readonly VerificationWorkloadReport[];
}
export interface ProjectVerificationOptions {
    readonly timeoutMs: number;
    readonly signal?: AbortSignal;
    readonly maxPolls?: number;
    readonly pollDelayMs?: number;
    readonly concurrency?: number;
    readonly maxRequests?: number;
    readonly maxResponseBytes?: number;
    readonly maxEvidenceAgeMs?: number;
    readonly checks?: readonly VerificationCheck[];
    /** Default true. Workers always require an identity-aware smoke check. */
    readonly requireSmokeChecks?: boolean;
    /** Text is omitted unless this synchronous redactor is supplied. It must remove application secrets. */
    readonly redact?: (text: string) => string;
    readonly onProgress?: (report: VerificationWorkloadReport) => void | Promise<void>;
}
export interface ProjectVerification {
    verifyApplication(input: ProjectVerificationTarget | ProjectApplicationResult, options: ProjectVerificationOptions): Promise<ProjectVerificationReport>;
}
const slug = (v: unknown): v is string => typeof v === 'string' && /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(v);
const id = (v: unknown): v is string => typeof v === 'string' && /^(?:[a-f0-9]{32}|[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12})$/.test(v);
function base(v: string): string {
    const u = new URL(v);
    if (!['https:', 'http:'].includes(u.protocol) || u.username || u.password || u.search || u.hash)
        throw new TypeError('Invalid verification endpoint');
    return u.href.replace(/\/+$/, '');
}
export function parseProjectVerificationTarget(value: unknown): ProjectVerificationTarget {
    const t = value as ProjectVerificationTarget;
    if (!t || Object.keys(t).sort().join() !== 'base_url,environment,project_slug,version,workloads' || t.version !== 1 || base(t.base_url) !== t.base_url || !slug(t.project_slug) ||
        t.environment !== null && !slug(t.environment) || !Array.isArray(t.workloads) || !t.workloads.length || t.workloads.length > 64 ||
        t.workloads.some(w => !w || Object.keys(w).sort().join() !== 'app_id,deployment_id,slug' || !slug(w.slug) || w.app_id !== null && !id(w.app_id) || w.deployment_id !== null && !id(w.deployment_id)) ||
        new Set(t.workloads.map(w => w.slug)).size !== t.workloads.length)
        throw new TypeError('Invalid verification target');
    return snapshot(t);
}
export async function projectVerificationTarget(result: ProjectApplicationResult): Promise<ProjectVerificationTarget> {
    const state = await loadProjectApplicationState(result.statePath);
    if (state.attempt_id !== result.attemptId || result.receipt.attempt_id !== state.attempt_id || result.receipt.project_slug !== state.project_slug)
        throw new TypeError('Application identity mismatch');
    return parseProjectVerificationTarget({ version: 1, base_url: state.base_url, project_slug: state.project_slug, environment: state.environment,
        workloads: result.workloads.map(w => ({ slug: w.slug, app_id: w.appId, deployment_id: w.deploymentId })) });
}
function timestamp(value: unknown): number {
    if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(value))
        return NaN;
    const year = Number(value.slice(0, 4)), month = Number(value.slice(5, 7)), day = Number(value.slice(8, 10));
    const days = [31, year % 400 === 0 || year % 4 === 0 && year % 100 !== 0 ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
    if (!year || month < 1 || month > 12 || day < 1 || day > days[month - 1]! || Number(value.slice(11, 13)) > 23 || Number(value.slice(14, 16)) > 59 || Number(value.slice(17, 19)) > 59)
        return NaN;
    return Date.parse(value);
}
class EvidenceError extends Error {
    constructor(readonly code: VerificationCode) { super(code); }
}
const actions = (code: VerificationCode): readonly string[] => code === 'verified' ? [] : code === 'release_blocked' ? ['review_release_approval'] :
    code === 'release_failed' ? ['inspect_build_and_deployment'] : code === 'smoke_failed' || code === 'health_failed' ? ['inspect_service', 'review_source_change'] :
        code === 'smoke_required' ? ['add_identity_aware_smoke_check'] : code === 'evidence_unavailable' ? ['check_permissions', 'retry_verification'] : ['inspect_evidence', 'retry_verification'];
const aggregate = (items: readonly VerificationWorkloadReport[]): VerificationOutcome => (['failed', 'blocked', 'inconclusive'] as const).find(o => items.some(w => w.outcome === o)) ?? 'verified';
export function bindProjectVerification(getConfig: () => ClientConfig): ProjectVerification {
    return { verifyApplication(input, options) {
            const config = getConfig();
            const capturedInput = 'version' in input ? parseProjectVerificationTarget(input) : snapshot(input);
            const integer = (v: number | undefined, d: number, min: number, max: number): number => {
                const n = v ?? d;
                if (!Number.isInteger(n) || n < min || n > max)
                    throw new TypeError('Invalid verification limit');
                return n;
            };
            const timeout = integer(options.timeoutMs, 0, 1, 2 ** 31 - 1), polls = integer(options.maxPolls, 3, 1, 1000), delay = integer(options.pollDelayMs, 1000, 0, 60000);
            const concurrency = integer(options.concurrency, 2, 1, 8), maxRequests = integer(options.maxRequests, 256, 1, 10000), maxBytes = integer(options.maxResponseBytes, 262144, 1024, 1048576);
            const maxAge = integer(options.maxEvidenceAgeMs, 120000, 1, 3600000), requireSmoke = options.requireSmokeChecks ?? true;
            const checks = (options.checks ?? []).map(c => ({ ...c }));
            if (typeof requireSmoke !== 'boolean' || options.redact !== undefined && typeof options.redact !== 'function' || options.onProgress !== undefined && typeof options.onProgress !== 'function' ||
                checks.length > 64 || checks.some(c => !slug(c.name) || !slug(c.workload) || typeof c.run !== 'function') || new Set(checks.map(c => c.name)).size !== checks.length)
                throw new TypeError('Invalid verification checks');
            const settings = { ...options };
            const controller = new AbortController(), started = performance.now(), end = started + timeout;
            const abort = () => controller.abort(settings.signal?.reason);
            if (settings.signal?.aborted)
                abort();
            else
                settings.signal?.addEventListener('abort', abort, { once: true });
            const timer = setTimeout(() => controller.abort(new EvidenceError('deadline')), timeout);
            const guard = () => { if (controller.signal.aborted)
                throw controller.signal.reason; if (performance.now() >= end) {
                controller.abort(new EvidenceError('deadline'));
                throw controller.signal.reason;
            } };
            async function bounded<T>(work: () => T | PromiseLike<T>): Promise<T> {
                guard();
                return new Promise<T>((resolve, reject) => {
                    const stop = () => { controller.signal.removeEventListener('abort', stop); reject(controller.signal.reason); };
                    controller.signal.addEventListener('abort', stop, { once: true });
                    Promise.resolve().then(() => { guard(); return work(); }).then(v => { controller.signal.removeEventListener('abort', stop); resolve(v); }, e => { controller.signal.removeEventListener('abort', stop); reject(e); });
                });
            }
            let requests = 0;
            async function read(path: string, ids: string[]): Promise<Record<string, any>> {
                guard();
                if (requests >= maxRequests)
                    throw new EvidenceError('request_limit');
                requests++;
                const requestFetch = config.requestFetch;
                const owned: ClientConfig = { ...config, requestFetch: opts => {
                        const fetch = requestFetch(opts);
                        return async (url, init) => {
                            const response = await fetch(url, { ...init, redirect: 'error' });
                            const reader = response.body?.getReader();
                            if (!reader)
                                return response;
                            let size = 0;
                            const chunks: Uint8Array[] = [];
                            const cancelBody = () => { void reader.cancel(controller.signal.reason).catch(() => { }); };
                            controller.signal.addEventListener('abort', cancelBody, { once: true });
                            if (controller.signal.aborted) cancelBody();
                            try {
                                for (;;) {
                                    guard();
                                    const { done, value } = await reader.read();
                                    guard();
                                    if (done)
                                        break;
                                    size += value.byteLength;
                                    if (size > maxBytes)
                                        throw new EvidenceError('response_limit');
                                    chunks.push(value);
                                }
                            }
                            finally {
                                controller.signal.removeEventListener('abort', cancelBody);
                                await reader.cancel().catch(() => { });
                            }
                            return new Response(Buffer.concat(chunks), { status: response.status, headers: response.headers });
                        };
                    } };
                const data: unknown = await bounded(() => scopedRequest(owned, { method: 'GET', url: path }, { signal: controller.signal, timeoutMs: 0, retry: false, onResponse: info => {
                        if (info.requestId && /^[\w.-]{1,128}$/.test(info.requestId) && ids.length < 32 && !ids.includes(info.requestId))
                            ids.push(info.requestId);
                    } }));
                if (!data || typeof data !== 'object' || Array.isArray(data))
                    throw new EvidenceError('invalid_evidence');
                return data;
            }
            return (async () => {
                let target: ProjectVerificationTarget;
                try {
                    target = await bounded(() => 'version' in capturedInput ? capturedInput : projectVerificationTarget(capturedInput));
                }
                catch (e) {
                    clearTimeout(timer);
                    settings.signal?.removeEventListener('abort', abort);
                    throw e;
                }
                let reports: VerificationWorkloadReport[] = target.workloads.map(w => ({ ...w, outcome: 'inconclusive', code: 'not_observed', polls: 0, checks: [], request_ids: [], diagnostics: [], diagnostics_truncated: false, next_actions: actions('not_observed') }));
                const report = (code: VerificationCode): ProjectVerificationReport => snapshot({ version: 1, target, outcome: code !== 'complete' && aggregate(reports) === 'verified' ? 'inconclusive' : aggregate(reports), code, requests, workloads: reports });
                try {
                    if (target.base_url !== base(config.BASE))
                        throw new TypeError('Verification endpoint mismatch');
                    if (checks.some(c => !target.workloads.some(w => w.slug === c.workload)))
                        throw new TypeError('Unknown check workload');
                    let project: Record<string, any>;
                    try {
                        project = await read(`/v1/projects/${encodeURIComponent(target.project_slug)}`, []);
                    }
                    catch (e) {
                        if (e instanceof EvidenceError || controller.signal.aborted)
                            throw e;
                        throw new EvidenceError('evidence_unavailable');
                    }
                    if (project.slug !== target.project_slug || !id(project.id))
                        throw new EvidenceError('identity_mismatch');
                    let next = 0;
                    const worker = async () => {
                        for (;;) {
                            guard();
                            const index = next++;
                            if (index >= target.workloads.length)
                                return;
                            const workload = target.workloads[index]!, requestIds: string[] = [];
                            let current = reports[index]!;
                            const update = (outcome: VerificationOutcome, code: VerificationCode) => { current = { ...current, outcome, code, request_ids: [...requestIds], next_actions: actions(code) }; };
                            try {
                                if (!workload.app_id || !workload.deployment_id) {
                                    update('inconclusive', 'deployment_identity_unavailable');
                                }
                                else {
                                    const app = await read(`/v1/apps/${encodeURIComponent(workload.slug)}`, requestIds);
                                    if (app.id !== workload.app_id || app.slug !== workload.slug || app.project_id !== project.id)
                                        throw new EvidenceError('identity_mismatch');
                                    for (let poll = 1; poll <= polls; poll++) {
                                        guard();
                                        current = { ...current, polls: poll, checks: [], diagnostics: [], diagnostics_truncated: false };
                                        const dep = await read(`/v1/deployments/${workload.deployment_id}`, requestIds);
                                        if (dep.id !== workload.deployment_id || dep.app_id !== workload.app_id || (dep.scope ?? 'default') !== (target.environment ?? 'default'))
                                            throw new EvidenceError('identity_mismatch');
                                        const timeline = await read(`/v1/deployments/${workload.deployment_id}/timeline`, requestIds);
                                        if (timeline.deployment_id !== workload.deployment_id || timeline.app !== workload.slug || (timeline.scope ?? 'default') !== (target.environment ?? 'default'))
                                            throw new EvidenceError('identity_mismatch');
                                        if (typeof timeline.phase !== 'string' || !/^[a-z_][a-z0-9_-]{0,63}$/.test(timeline.phase) || typeof timeline.status !== 'string' || !/^[a-z_][a-z0-9_-]{0,63}$/.test(timeline.status) || !Number.isFinite(timeline.traffic_percent) || timeline.traffic_percent < 0 || timeline.traffic_percent > 100 || !Array.isArray(timeline.events))
                                            throw new EvidenceError('invalid_evidence');
                                        current = { ...current, timeline: { phase: timeline.phase.slice(0, 64), status: timeline.status.slice(0, 64), traffic_percent: timeline.traffic_percent, event_count: timeline.events.length, events: timeline.events.slice(-20).filter((e: any) => e && typeof e.kind === 'string' && /^[a-z_][a-z0-9_-]{0,63}$/.test(e.kind) && typeof e.at === 'string' && Number.isFinite(timestamp(e.at))).map((e: any) => ({ kind: e.kind, at: new Date(timestamp(e.at)).toISOString() })), truncated: timeline.truncated === true || timeline.events.length > 20 } };
                                        const diagnostics: string[] = [];
                                        let truncated = false;
                                        if (settings.redact) {
                                            const texts = [timeline.reason, dep.error_why, dep.error_fix, ...(Array.isArray(dep.error_relevant_logs) ? dep.error_relevant_logs.map((l: any) => l?.message) : [])].filter((t): t is string => typeof t === 'string');
                                            truncated = texts.length > 20;
                                            for (const text of texts.slice(0, 20)) {
                                                if (text.length > 4096) {
                                                    truncated = true;
                                                    continue;
                                                }
                                                try {
                                                    const safe: unknown = settings.redact(text);
                                                    if (typeof safe === 'string') {
                                                        diagnostics.push(safe.slice(0, 512));
                                                        truncated ||= text.length > 4096 || safe.length > 512;
                                                    }
                                                }
                                                catch {
                                                    truncated = true;
                                                }
                                            }
                                        }
                                        current = { ...current, diagnostics, diagnostics_truncated: truncated };
                                        if (['failed', 'cancelled', 'rolled_back'].includes(timeline.phase))
                                            update('failed', 'release_failed');
                                        else if (['held', 'awaiting_approval'].includes(timeline.phase))
                                            update('blocked', 'release_blocked');
                                        else if (timeline.phase !== 'released' || timeline.status !== 'live' || dep.status !== 'live' || timeline.traffic_percent !== 100)
                                            update('inconclusive', 'release_pending');
                                        else {
                                            const health = await read(`/v1/apps/${encodeURIComponent(workload.slug)}/health${target.environment ? `?environment=${encodeURIComponent(target.environment)}` : ''}`, requestIds);
                                            if (health.app_id !== workload.app_id || health.scope !== (target.environment ?? 'default'))
                                                throw new EvidenceError('identity_mismatch');
                                            const now = Date.now(), evaluated = timestamp(health.evaluated_at), sampled = timestamp(health.metrics_as_of);
                                            const fresh = Number.isFinite(evaluated) && Number.isFinite(sampled) && evaluated <= now && sampled <= evaluated && now - sampled <= maxAge &&
                                                Number.isFinite(health.valid_for_seconds) && health.valid_for_seconds > 0 && now - evaluated < Math.min(maxAge, health.valid_for_seconds * 1000);
                                            const matches = Array.isArray(health.serving_deployment_ids) && health.serving_deployment_ids.length === 1 && health.serving_deployment_ids[0] === workload.deployment_id;
                                            const isWorker = ['worker', 'job'].includes(app.workload_class);
                                            const unsupported = isWorker && health.phase === 'unsupported';
                                            if (!['healthy', 'degraded', 'unhealthy', 'unknown'].includes(health.status) || !['serving', 'idle', 'starting', 'deploying', 'stopped', 'not_deployed', 'maintenance', 'unsupported', 'unknown'].includes(health.phase) || !Array.isArray(health.serving_deployment_ids) || health.serving_deployment_ids.length > 64 || !health.serving_deployment_ids.every(id))
                                                throw new EvidenceError('invalid_evidence');
                                            current = { ...current, health: { status: health.status, phase: health.phase, evaluated_at: Number.isFinite(evaluated) ? new Date(evaluated).toISOString() : null, metrics_as_of: Number.isFinite(sampled) ? new Date(sampled).toISOString() : null, fresh, serving_deployment_ids: health.serving_deployment_ids } };
                                            if (!unsupported && (!fresh || !matches))
                                                update('inconclusive', !fresh ? 'stale_evidence' : 'deployment_not_serving');
                                            else if (!unsupported && ['unhealthy', 'degraded'].includes(health.status))
                                                update('failed', 'health_failed');
                                            else if (!unsupported && (health.status !== 'healthy' || health.phase !== 'serving'))
                                                update('inconclusive', 'health_unknown');
                                            else {
                                                const evidence: VerificationCheckEvidence[] = [];
                                                for (const check of checks.filter(c => c.workload === workload.slug)) {
                                                    let result: VerificationCheckResult;
                                                    try {
                                                        result = await bounded(() => check.run(Object.freeze({ ...workload, signal: controller.signal, remainingMs: Math.max(0, end - performance.now()) })));
                                                    }
                                                    catch (e) {
                                                        guard();
                                                        evidence.push({ name: check.name, outcome: 'inconclusive', code: 'check_unavailable' });
                                                        continue;
                                                    }
                                                    guard();
                                                    const valid = result && ['verified', 'failed', 'inconclusive'].includes(result.outcome);
                                                    const attributed = valid && result.deployment_id === workload.deployment_id;
                                                    evidence.push({ name: check.name, outcome: valid && (attributed || result.outcome === 'inconclusive') ? result.outcome : 'inconclusive', code: attributed ? 'check_observed' : 'check_identity_unconfirmed' });
                                                }
                                                current = { ...current, checks: evidence };
                                                if (evidence.some(e => e.outcome === 'failed'))
                                                    update('failed', 'smoke_failed');
                                                else if (evidence.some(e => e.outcome === 'inconclusive'))
                                                    update('inconclusive', 'smoke_inconclusive');
                                                else if (!evidence.length && (requireSmoke || isWorker || unsupported))
                                                    update('inconclusive', 'smoke_required');
                                                else
                                                    update('verified', 'verified');
                                            }
                                        }
                                        guard();
                                        reports[index] = snapshot(current);
                                        if (settings.onProgress) {
                                            try {
                                                await bounded(() => settings.onProgress!(snapshot(current)));
                                            }
                                            catch {
                                                guard();
                                                throw new EvidenceError('progress_failed');
                                            }
                                        }
                                        if (current.outcome !== 'inconclusive' || poll === polls)
                                            break;
                                        await bounded(() => new Promise<void>(resolve => { const timer = setTimeout(done, delay); function done() { clearTimeout(timer); controller.signal.removeEventListener('abort', done); resolve(); } controller.signal.addEventListener('abort', done, { once: true }); }));
                                    }
                                }
                            }
                            catch (e) {
                                if (controller.signal.aborted)
                                    throw e;
                                update('inconclusive', e instanceof EvidenceError ? e.code : 'evidence_unavailable');
                            }
                            guard();
                            reports[index] = snapshot(current);
                        }
                    };
                    await bounded(() => Promise.all(Array.from({ length: Math.min(concurrency, target.workloads.length) }, worker)));
                    return report('complete');
                }
                catch (e) {
                    if (settings.signal?.aborted)
                        throw settings.signal.reason;
                    if (!(e instanceof EvidenceError))
                        throw e;
                    reports = reports.map(w => w.outcome === 'inconclusive' ? { ...w, code: e.code, next_actions: actions(e.code) } : w);
                    return report(e.code);
                }
                finally {
                    clearTimeout(timer);
                    controller.abort();
                    settings.signal?.removeEventListener('abort', abort);
                }
            })();
        } };
}
