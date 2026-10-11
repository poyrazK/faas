import { getCommitDeliveryReport, CommitDeliveryReportError } from './commit-delivery-report.js';
import type { CommitDeliveryReportItem } from './commit-delivery-report.js';
import { sameAcceptance } from './commit-recovery-plan.js';
import type { CommitEventPublication } from './generated/models/CommitEventPublication.js';
import type { CommitRecoveryPlanItem, CommitRecoveryPlan } from './commit-recovery-plan.js';
import type { CommitRecoveryApplyItem, CommitRecoveryApplyResult } from './commit-recovery-apply.js';
import type { EventReceiptRecipientResponse } from './generated/models/EventReceiptRecipientResponse.js';

export interface CommitRecoveryStatusSelection {
  publication: CommitEventPublication; reviewed: CommitRecoveryPlanItem;
}
export interface CommitRecoveryStatusObservation {
  status: string; reason?: string; attribution: string; observed_at?: string;
  generation?: number; generation_relation?: string; execution_id?: string; execution_state?: string;
}
export interface CommitRecoveryStatusChange {
  field: string; previous: unknown; current: unknown; previous_observed_at?: string; observed_at?: string;
}
export interface CommitRecoveryStatusItem {
  previous_observation?: CommitRecoveryStatusObservation; changes?: CommitRecoveryStatusChange[];
  execution_id?: string; execution_state?: string;
  selection?: CommitRecoveryStatusSelection;
  attempt: CommitRecoveryApplyItem; status: string; reason?: string; attribution: string;
  observed_at?: string; expected_generation?: number; generation?: number; generation_relation?: string;
  recipient?: EventReceiptRecipientResponse;
}
export interface CommitRecoveryMonitoring {
  max_observation_age_ms?: number; freshness?: Record<string, number>;
  stop_reason: string; passes: number; retries_consumed: number; elapsed_ms: number; acknowledged_executions_terminal: boolean;
}
export interface CommitRecoveryStatusResult {
  monitoring?: CommitRecoveryMonitoring;
  source_id: string; refreshing?: boolean; subscription_id?: string; remaining: number; items: CommitRecoveryStatusItem[]; counts: Record<string, number>; complete: boolean;
}
export class CommitRecoveryStatusError extends Error {
  constructor(readonly result: CommitRecoveryStatusResult, cause: unknown) {
    super('Commit recovery status read failed; partial observations are attached', { cause });
    this.name = 'CommitRecoveryStatusError';
  }
}
function observe(row: CommitRecoveryStatusItem, evidence?: CommitDeliveryReportItem): void {
  if (!evidence) return;
  row.observed_at = evidence.observed_at; row.reason = evidence.evidence_status;
  const recipient = evidence.subscribers.find(item => item.subscription_id.toLowerCase() === row.attempt.subscription_id.toLowerCase())?.recipient;
  if (!recipient) { if (evidence.evidence_status === 'available') row.reason = 'recipient_unavailable'; return; }
  row.recipient = recipient; row.generation = Number.isSafeInteger(recipient.routing?.generation) ? recipient.routing?.generation : undefined;
  if (row.expected_generation !== undefined) {
    row.generation_relation = row.generation === undefined || !Number.isSafeInteger(row.generation) ? 'unavailable'
      : row.generation > row.expected_generation ? 'advanced' : row.generation < row.expected_generation ? 'regressed' : 'unchanged';
  }
  if (recipient.workflow_name) { row.status = 'unsupported_recipient'; row.reason = 'unsupported_recipient'; return; }
  const { execution, matched } = selectedExecution(row);
  if (matched) row.attribution = 'matches_acknowledged_execution';
  row.execution_id = execution?.invocation_id; row.execution_state = execution?.state;
  let state: string | undefined = execution?.state ?? recipient.routing?.state;
  if (!execution && recipient.cancellation) state = 'cancelled';
  if (!execution && recipient.execution_unavailable && state !== 'cancelled') { row.status = 'unresolved'; row.reason = recipient.execution_unavailable; return; }
  row.reason = state || 'evidence_unavailable';
  row.status = state === 'completed' ? 'completed' : ['pending', 'processing', 'dispatching'].includes(state ?? '') ? 'active'
    : ['failed', 'dead_letter', 'cancelled', 'expired', 'superseded'].includes(state ?? '') ? 'failed' : 'unresolved';
}
/** Read guarded evidence only. An unknown attempt remains unknown even when delivery advances. */
export async function getCommitRecoveryStatus(source: string, reviewed: CommitRecoveryPlan, applied: CommitRecoveryApplyResult,
  options: { maxObservationAgeMs?: number; limit?: number; recipientPages?: number; timeoutMs?: number; signal?: AbortSignal; resume?: CommitRecoveryStatusResult; refresh?: CommitRecoveryStatusResult } = {}): Promise<CommitRecoveryStatusResult> {
  const { limit = 50, recipientPages = 1, timeoutMs = 120000, signal, maxObservationAgeMs = 0 } = options;
  validateObservationAge(maxObservationAgeMs);
  if (options.resume && options.refresh) throw new TypeError('choose resume or refresh');
  const plan = structuredClone(reviewed), result = structuredClone(applied), previous = options.resume || options.refresh ? structuredClone(options.resume ?? options.refresh!) : undefined;
  const out: CommitRecoveryStatusResult = { source_id: source, subscription_id: plan.report.subscription_id, remaining: 0, items: [], counts: Object.create(null), complete: false };
  if (!Number.isInteger(limit) || limit < 1 || limit > 50 || !Number.isInteger(recipientPages) || recipientPages < 1 || recipientPages > 20 || !Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 86400000 || plan.report.source_id !== source || result.source_id.toLowerCase() !== source.toLowerCase() || plan.report.items.length > 200 || plan.items.length > 200*200*20 || result.items.length > 200*200*20) throw new TypeError('invalid recovery status budgets or source');
  const publications = new Map(plan.report.items.map(item => [item.acceptance.event_id.toLowerCase(), item]));
  const selection = new Map<string, typeof plan.items[number]>();
  const key = (item: { event_id: string; subscription_id: string }) => (item.event_id + ':' + item.subscription_id).toLowerCase();
  const uuid = /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;
  for (const item of plan.items) {
    if (!publications.has(item.event_id.toLowerCase()) || !uuid.test(item.subscription_id) || item.subscription_id === '00000000-0000-0000-0000-000000000000' || selection.has(key(item)) || (plan.report.subscription_id && plan.report.subscription_id.toLowerCase() !== item.subscription_id.toLowerCase()) || !['eligible', 'skipped'].includes(item.status) || (item.status === 'eligible' && !item.action)) throw new TypeError('invalid recovery status selection');
    selection.set(key(item), item);
  }
  const seen = new Set<string>();
  for (const item of result.items) {
    const selected = selection.get(key(item));
    if (!selected || seen.has(key(item)) || !['queued', 'unknown', 'rejected', 'read_error', 'skipped', 'not_attempted'].includes(item.status) || (['queued', 'unknown', 'rejected', 'read_error'].includes(item.status) && selected.status !== 'eligible')) throw new TypeError('apply result does not match reviewed selection');
    seen.add(key(item));
    const generation = selected.action?.kind === 'routing_replay' ? selected.action.body?.expected_generation : undefined;
    const publication = publications.get(item.event_id.toLowerCase())!.acceptance.publication;
    if (!publication) throw new TypeError('saved publication identity mismatch');
    out.items.push({ selection: { publication, reviewed: selected }, attempt: item, status: 'not_observed', reason: 'status_budget', attribution: 'unproven',
      expected_generation: generation !== undefined && Number.isSafeInteger(generation) && generation >= 0 ? generation : undefined });
  }
  if (previous) {
    if (previous.refreshing !== undefined && typeof previous.refreshing !== 'boolean') throw new TypeError('invalid saved refresh flag');
    if (previous.items.some(row => !row.selection)) throw new TypeError('saved status lacks selection snapshots; run recovery-status without --resume');
    if (previous.source_id.toLowerCase() !== source.toLowerCase() || (previous.subscription_id ?? '').toLowerCase() !== (out.subscription_id ?? '').toLowerCase() || previous.items.length !== out.items.length) throw new TypeError('saved status selection mismatch');
    for (let i = 0; i < previous.items.length; i++) {
      const row = previous.items[i]!, expected = out.items[i]!;
      if (!row.selection || !sameSelection(row.selection, expected.selection!) || canonical(row.attempt) !== canonical(expected.attempt) || row.expected_generation !== expected.expected_generation) throw new TypeError('saved status does not match reviewed plan and apply result');
      if (row.observed_at !== undefined && (typeof row.observed_at !== 'string' || !sameAcceptance(row.observed_at, row.observed_at))) throw new TypeError('invalid saved observation timestamp');
      if (!['completed', 'active', 'failed', 'unresolved', 'unsupported_recipient', 'read_error', 'not_observed'].includes(row.status) || !['unproven', 'matches_acknowledged_execution'].includes(row.attribution) || (row.attribution === 'matches_acknowledged_execution' && row.attempt.status !== 'queued')) throw new TypeError('invalid saved observation or attribution');
      if (Boolean(previous.refreshing) !== Boolean(row.previous_observation)) throw new TypeError('invalid saved refresh baseline');
      if (row.previous_observation) validateObservation(row.previous_observation, row.attempt.status);
    }
    out.items = previous.items;
    out.refreshing = previous.refreshing;
  }
  if (options.refresh) {
    out.refreshing = true;
    out.items = out.items.map(old => ({
      attempt: old.attempt, selection: old.selection, expected_generation: old.expected_generation,
      previous_observation: observation(old), status: 'not_observed', reason: 'status_budget', attribution: 'unproven',
    }));
  }
  recount(out);
  const pending = ['not_observed', 'read_error'].flatMap(status => out.items.map((row, index) => row.status === status ? index : -1).filter(index => index >= 0));
  if (maxObservationAgeMs > 0) {
    const now = Date.now();
    const stale = out.items.map((row, index) => ({ row, index })).filter(({row}) => !['not_observed', 'read_error'].includes(row.status) && freshnessKind(row, now, maxObservationAgeMs) !== 'fresh');
    const priority = (row: CommitRecoveryStatusItem) => { const value = Date.parse(row.observed_at ?? ''); return Number.isFinite(value) && value <= now ? value : -Infinity; };
    stale.sort((a, b) => priority(a.row) - priority(b.row));
    for (const item of stale) pending.push(item.index);
  }
  const controller = new AbortController(), abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true }); if (signal?.aborted) abort();
  const timer = setTimeout(abort, timeoutMs);
  let cause: unknown;
  try {
    await getCommitDeliveryReport(source, { refresh: plan.report, recipientPages: 1, timeoutMs, signal: controller.signal });
    for (const index of pending.slice(0, limit)) {
      if (controller.signal.aborted) { cause ??= new Error('status interrupted'); break; }
      const old = out.items[index]!;
      const row: CommitRecoveryStatusItem = { previous_observation: old.previous_observation, attempt: old.attempt, selection: old.selection, expected_generation: old.expected_generation, status: 'unresolved', reason: 'evidence_unavailable', attribution: 'unproven' };
      out.items[index] = row;
      const original = publications.get(row.attempt.event_id.toLowerCase())!;
      try {
        const report = await getCommitDeliveryReport(source, { refresh: { ...plan.report, subscription_id: row.attempt.subscription_id.toLowerCase(), items: [{ ...original, subscribers: [] }] }, recipientPages, timeoutMs, signal: controller.signal });
        observe(row, report.items[0]);
      } catch (error) {
        if (error instanceof CommitDeliveryReportError) observe(row, error.report.items[0]);
        row.status = 'read_error'; row.reason = 'evidence_read_failed'; out.complete = false; cause = cause ? new AggregateError([cause, error], 'recovery status reads failed') : error;
      }
    }
  } catch (error) { cause = error; }
  finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); }
  if (controller.signal.aborted) { cause ??= new Error('status interrupted'); for (const row of out.items) if (row.status === 'not_observed') row.reason = 'interrupted'; }
  recount(out);
  if (cause) out.complete = false;
  if (cause) throw new CommitRecoveryStatusError(out, cause);
  return out;
}


function canonical(value: unknown): string {
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']';
  if (value && typeof value === 'object') return '{' + Object.entries(value).filter(([, v]) => v !== undefined).sort(([a], [b]) => a.localeCompare(b)).map(([k, v]) => JSON.stringify(k) + ':' + canonical(v)).join(',') + '}';
  return JSON.stringify(value ?? null);
}
function sameSelection(a: CommitRecoveryStatusSelection, b: CommitRecoveryStatusSelection): boolean {
  if (!a.publication || !sameAcceptance(a.publication.accepted_at, b.publication.accepted_at)) return false;
  return canonical({ ...a, publication: { ...a.publication, accepted_at: b.publication.accepted_at } }) === canonical(b);
}
function recount(out: CommitRecoveryStatusResult): void {
  out.counts = Object.create(null); out.remaining = 0;
  for (const row of out.items) {
    row.changes = compareObservation(row.previous_observation, observation(row));
    out.counts[row.status] = (out.counts[row.status] ?? 0) + 1;
    if (['not_observed', 'read_error'].includes(row.status)) out.remaining++;
  }
  out.complete = out.remaining === 0;
}


function selectedExecution(row: CommitRecoveryStatusItem) {
  const recipient = row.recipient;
  if (recipient?.workflow_name) return { execution: undefined, matched: false };
  let execution = recipient?.recovery?.latest_replay ?? recipient?.execution;
  let matched = false;
  const ack = row.attempt.result as { id?: unknown } | undefined;
  if (row.attempt.status === 'queued' && typeof ack?.id === 'string' && /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(ack.id)) {
    const matching = [recipient?.execution, recipient?.recovery?.latest_replay].find(item => item?.invocation_id.toLowerCase() === (ack.id as string).toLowerCase());
    if (matching) { execution = matching; matched = true; }
  }
  return { execution, matched };
}
function observation(row: CommitRecoveryStatusItem): CommitRecoveryStatusObservation {
  const { execution } = selectedExecution(row);
  return {
    status: row.status, reason: row.reason || undefined, attribution: row.attribution,
    observed_at: row.observed_at, generation: row.generation, generation_relation: row.generation_relation || undefined,
    execution_id: row.execution_id || execution?.invocation_id, execution_state: row.execution_state || execution?.state,
  };
}
function validateObservation(previous: CommitRecoveryStatusObservation, attempt: string): void {
  if (!['completed', 'active', 'failed', 'unresolved', 'unsupported_recipient', 'read_error', 'not_observed'].includes(previous.status)
    || !['unproven', 'matches_acknowledged_execution'].includes(previous.attribution)
    || (previous.attribution === 'matches_acknowledged_execution' && attempt !== 'queued')) throw new TypeError('invalid saved refresh observation');
  if (previous.observed_at !== undefined && (typeof previous.observed_at !== 'string' || !sameAcceptance(previous.observed_at, previous.observed_at))) throw new TypeError('invalid saved refresh timestamp');
}
function compareObservation(previous: CommitRecoveryStatusObservation | undefined, current: CommitRecoveryStatusObservation): CommitRecoveryStatusChange[] | undefined {
  if (!previous || ['not_observed', 'read_error'].includes(previous.status) || ['not_observed', 'read_error'].includes(current.status)) return undefined;
  const changes: CommitRecoveryStatusChange[] = [];
  const fields = ['status', 'reason', 'generation', 'generation_relation', 'attribution', 'execution_id', 'execution_state'] as const;
  for (const field of fields) {
    const fallback = field === 'generation' ? null : '';
    const before = previous[field] ?? fallback, after = current[field] ?? fallback;
    if (canonical(before) !== canonical(after)) changes.push({ field, previous: before, current: after, previous_observed_at: previous.observed_at, observed_at: current.observed_at });
  }
  return changes.length ? changes : undefined;
}

/** True only when all queued attempts have matching terminal execution evidence. */
export function commitRecoveryStatusSettled(out: CommitRecoveryStatusResult): boolean {
  return out.complete && out.remaining === 0 && out.items.every(row => {
    if (row.attempt.status === 'queued') return ['completed', 'failed'].includes(row.status) && row.attribution === 'matches_acknowledged_execution'
      && ['completed', 'failed', 'cancelled', 'expired', 'superseded'].includes(row.execution_state ?? '');
    return ['rejected', 'skipped', 'not_attempted'].includes(row.attempt.status);
  });
}

/** Poll a fixed selection; errors retain the last/partial result. */
export async function watchCommitRecoveryStatus(source: string, plan: CommitRecoveryPlan, applied: CommitRecoveryApplyResult, options: {
  limit?: number; recipientPages?: number; timeoutMs?: number; intervalMs?: number; readRetries?: number; maxObservationAgeMs?: number; signal?: AbortSignal;
  resume?: CommitRecoveryStatusResult; refresh?: CommitRecoveryStatusResult;
  onProgress?: (status: CommitRecoveryStatusResult) => void;
} = {}): Promise<CommitRecoveryStatusResult> {
  const { limit, recipientPages, onProgress, signal } = options;
  const maxObservationAgeMs = options.maxObservationAgeMs ?? 0;
  validateObservationAge(maxObservationAgeMs);
  const readRetries = options.readRetries ?? 0;
  if (!Number.isInteger(readRetries) || readRetries < 0 || readRetries > 20) throw new TypeError('invalid recovery watch read retry budget');
  const intervalMs = options.intervalMs ?? 1000, timeoutMs = options.timeoutMs ?? 120000;
  if (!Number.isFinite(intervalMs) || intervalMs < 100 || intervalMs > 60000 || !Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 86400000 || (options.resume && options.refresh)) throw new TypeError('invalid recovery watch options');
  const fixedPlan = structuredClone(plan), fixedApplied = structuredClone(applied);
  let previous = options.resume || options.refresh ? structuredClone(options.resume ?? options.refresh) : undefined;
  let refresh = Boolean(options.refresh) || Boolean(previous && previous.items.every(row => !['not_observed', 'read_error'].includes(row.status)));
  const controller = new AbortController(), abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true });
  if (signal?.aborted) abort();
  const timer = setTimeout(abort, timeoutMs), started = performance.now();
  let out: CommitRecoveryStatusResult = { source_id: source, items: [], counts: {}, remaining: 0, complete: false };
  let usedRetries = 0, passes = 0;
  let reason = 'read_failure', phase = 'read';
  const summarize = (stop_reason: string): CommitRecoveryMonitoring => {
    const freshness = maxObservationAgeMs > 0 ? freshnessCounts(out, maxObservationAgeMs) : undefined;
    return {
      stop_reason, passes, retries_consumed: usedRetries, elapsed_ms: Math.max(0, Math.floor(performance.now() - started)),
      acknowledged_executions_terminal: commitRecoveryStatusSettled(out) && (!freshness || freshness.fresh === out.items.length),
      ...(freshness ? { max_observation_age_ms: maxObservationAgeMs, freshness } : {}),
    };
  };
  try {
    for (;;) {
      if (controller.signal.aborted || performance.now() - started >= timeoutMs) throw new CommitRecoveryStatusError(out, new Error('watch interrupted'));
      let delay = intervalMs, failed = false;
      passes++;
      try {
        out = await getCommitRecoveryStatus(source, fixedPlan, fixedApplied, {
          limit, recipientPages, maxObservationAgeMs,
          timeoutMs: Math.max(1, timeoutMs - (performance.now() - started)), signal: controller.signal,
          ...(previous ? refresh ? { refresh: previous } : { resume: previous } : {}),
        });
      } catch (error) {
        if (!(error instanceof CommitRecoveryStatusError)) throw error;
        out = error.result;
        const retryAfter = recoveryReadRetry(error.cause);
        if (retryAfter === undefined || usedRetries >= readRetries || controller.signal.aborted) {
          if (retryAfter !== undefined && readRetries > 0 && usedRetries >= readRetries) reason = 'retry_exhausted';
          throw error;
        }
        usedRetries++; failed = true;
        delay = Math.max(Math.min(60000, intervalMs * 2 ** (usedRetries - 1)), retryAfter);
      }
      out.monitoring = summarize('running');
      phase = 'callback';
      onProgress?.(structuredClone(out));
      phase = 'read';
      if (controller.signal.aborted || performance.now() - started >= timeoutMs) throw new CommitRecoveryStatusError(out, new Error('watch interrupted'));
      if (!failed && commitRecoveryStatusSettled(out) && observationsFresh(out, maxObservationAgeMs)) { out.monitoring = summarize('settled'); return out; }
      previous = out; refresh = !failed && out.remaining === 0 && observationsFresh(out, maxObservationAgeMs);
      await new Promise<void>(resolve => {
        const done = () => { clearTimeout(poll); controller.signal.removeEventListener('abort', done); resolve(); };
        const poll = setTimeout(done, Math.min(delay, Math.max(1, timeoutMs - (performance.now() - started))));
        controller.signal.addEventListener('abort', done, { once: true });
        if (controller.signal.aborted) done();
      });
    }
  } catch (error) {
    if (error instanceof CommitRecoveryStatusError) out = error.result;
    if (signal?.aborted) reason = 'cancelled';
    else if (controller.signal.aborted || performance.now() - started >= timeoutMs) reason = 'timeout';
    else if (phase === 'callback') reason = 'callback_failure';
    else if (recoveryValidationFailure(error)) reason = 'validation_failure';
    out.monitoring = summarize(reason);
    if (error instanceof CommitRecoveryStatusError) throw error;
    throw new CommitRecoveryStatusError(out, error);
  } finally {
    clearTimeout(timer); signal?.removeEventListener('abort', abort);
  }
}

function recoveryReadRetry(error: unknown): number | undefined {
  if (error instanceof AggregateError) {
    const delays = error.errors.map(recoveryReadRetry);
    return delays.some(delay => delay === undefined) ? undefined : Math.max(0, ...delays as number[]);
  }
  if (error instanceof CommitDeliveryReportError || error instanceof CommitRecoveryStatusError) return recoveryReadRetry(error.cause);
  if (!(error instanceof Error)) return undefined;
  const typed = error as Error & { status?: number; retryAfter?: string; problem?: { retry_after_seconds?: number }; body?: { retry_after_seconds?: number }; code?: string };
  if (typed.status !== undefined) {
    if (![429, 500, 502, 503, 504].includes(typed.status)) return undefined;
    let delay = 0;
    const seconds = typed.problem?.retry_after_seconds ?? typed.body?.retry_after_seconds;
    if (typeof seconds === 'number' && Number.isFinite(seconds) && seconds > 0) delay = seconds * 1000;
    if (typed.retryAfter) {
      const value = /^\d+$/.test(typed.retryAfter) ? Number(typed.retryAfter) * 1000 : Date.parse(typed.retryAfter) - Date.now();
      if (Number.isFinite(value)) delay = Math.max(delay, value);
      else if (/^\d+$/.test(typed.retryAfter)) delay = Math.max(delay, 86400000);
    }
    return delay;
  }
  if (['ECONNRESET', 'ECONNREFUSED', 'ETIMEDOUT', 'EAI_AGAIN', 'ENETUNREACH', 'UND_ERR_CONNECT_TIMEOUT', 'UND_ERR_SOCKET'].includes(typed.code ?? '')) return 0;
  if (error instanceof TypeError && error.message === 'fetch failed') return 0;
  return undefined;
}

function recoveryValidationFailure(error: unknown): boolean {
  if (error instanceof TypeError) return error.message !== 'fetch failed';
  if (error instanceof AggregateError) return error.errors.some(recoveryValidationFailure);
  if (error instanceof CommitRecoveryStatusError || error instanceof CommitDeliveryReportError) return recoveryValidationFailure(error.cause);
  return false;
}

function validateObservationAge(age: number): void {
  if (!Number.isFinite(age) || age < 0 || age > 86400000 || (age > 0 && age < 100)) throw new TypeError('invalid maximum observation age');
}
function freshnessKind(row: CommitRecoveryStatusItem, now: number, age: number): string {
  if (['not_observed', 'read_error'].includes(row.status)) return 'unobserved';
  const observed = Date.parse(row.observed_at ?? '');
  if (!Number.isFinite(observed)) return 'missing_timestamp';
  if (observed > now) return 'future_timestamp';
  return now - observed > age ? 'stale' : 'fresh';
}
function freshnessCounts(out: CommitRecoveryStatusResult, age: number): Record<string, number> {
  const counts: Record<string, number> = { fresh: 0, stale: 0, missing_timestamp: 0, future_timestamp: 0, unobserved: 0 };
  const now = Date.now();
  for (const row of out.items) { const kind = freshnessKind(row, now, age); counts[kind] = (counts[kind] ?? 0) + 1; }
  return counts;
}
function observationsFresh(out: CommitRecoveryStatusResult, age: number): boolean {
  return age === 0 || freshnessCounts(out, age).fresh === out.items.length;
}
