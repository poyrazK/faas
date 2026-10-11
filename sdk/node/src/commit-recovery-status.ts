import { randomUUID } from 'node:crypto';
import type { EventRecipientBlockerQuery } from './generated/models/EventRecipientBlockerQuery.js';
import { EventsService } from './generated/services/EventsService.js';
import type { EventBacklogRecipient } from './generated/models/EventBacklogRecipient.js';
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
export interface CommitRecoveryDiagnosticProgress {
  consumers: CommitRecoveryDiagnosticConsumer[];
  compared: number; changed: number;
  max_diagnostic_age_ms: number; freshness_checked_at: string; freshness: Record<string, number>; fresh_complete: boolean;
  next_index: number; retry_next_index: number; refresh_next_index: number; target_count: number; remaining: number; deferred: number; retained: number; reads: number; counts: Record<string, number>; complete: boolean;
}
export interface CommitRecoveryDiagnostics {
  comparison?: string; previous_observation?: CommitRecoveryDiagnosticObservation; changes?: CommitRecoveryDiagnosticChange[];
  retained?: boolean;
  status: string; reason?: string; routing_state?: string; observed_at?: string; coverage?: string; truncated?: boolean; recipient?: EventBacklogRecipient;
}
export interface CommitRecoveryStatusItem {
  diagnostics?: CommitRecoveryDiagnostics;
  previous_observation?: CommitRecoveryStatusObservation; changes?: CommitRecoveryStatusChange[];
  execution_id?: string; execution_state?: string;
  selection?: CommitRecoveryStatusSelection;
  attempt: CommitRecoveryApplyItem; status: string; reason?: string; attribution: string;
  observed_at?: string; expected_generation?: number; generation?: number; generation_relation?: string;
  recipient?: EventReceiptRecipientResponse;
}
export interface CommitRecoveryMonitoring {
 alert_outbox?: CommitRecoveryAlertOutbox;
 blocker_incidents?: CommitRecoveryBlockerIncident[]; blocker_transitions?: CommitRecoveryBlockerTransition[];
 stall_threshold_ms?: number; consumer_stalls?: CommitRecoveryConsumerStall[];
  diagnostic_estimate?: CommitRecoveryDiagnosticEstimate;
  watch_mode?: string; diagnostic_coverage_complete?: boolean;
  max_observation_age_ms?: number; freshness?: Record<string, number>;
  stop_reason: string; passes: number; retries_consumed: number; elapsed_ms: number; acknowledged_executions_terminal: boolean;
}
export interface CommitRecoveryStatusResult {
  diagnostics_progress?: CommitRecoveryDiagnosticProgress;
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
  options: { diagnostics?: boolean; maxDiagnosticAgeMs?: number; maxObservationAgeMs?: number; limit?: number; recipientPages?: number; timeoutMs?: number; signal?: AbortSignal; resume?: CommitRecoveryStatusResult; refresh?: CommitRecoveryStatusResult } = {}): Promise<CommitRecoveryStatusResult> {
  const { limit = 50, recipientPages = 1, timeoutMs = 120000, signal, maxObservationAgeMs = 0 } = options;
  validateObservationAge(maxObservationAgeMs);
  validateObservationAge(options.maxDiagnosticAgeMs ?? 0);
  if ((options.maxDiagnosticAgeMs ?? 0) > 0 && !options.diagnostics) throw new TypeError("maximum diagnostic age requires diagnostics");
  if (options.resume && options.refresh) throw new TypeError('choose resume or refresh');
  const plan = structuredClone(reviewed), result = structuredClone(applied), previous = options.resume || options.refresh ? structuredClone(options.resume ?? options.refresh!) : undefined;
  const diagnosticPrevious = options.resume ? structuredClone(previous) : undefined;
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
      delete row.diagnostics;
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
    if (options.diagnostics) Object.assign(out, await diagnoseCommitRecoveryStatus(out, cause ? AbortSignal.abort() : controller.signal, diagnosticPrevious, options.maxDiagnosticAgeMs));
  } catch (error) { cause = error; }
  finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); }
  if (controller.signal.aborted) { cause ??= new Error('status interrupted'); for (const row of out.items) if (row.status === 'not_observed') row.reason = 'interrupted'; }
  recount(out);
  if (cause) out.complete = false;
  if (cause && options.diagnostics && !out.diagnostics_progress) Object.assign(out, await diagnoseCommitRecoveryStatus(out, AbortSignal.abort(), diagnosticPrevious, options.maxDiagnosticAgeMs));
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
  limit?: number; recipientPages?: number; timeoutMs?: number; intervalMs?: number; stallThresholdMs?: number; watchDiagnostics?: boolean; diagnostics?: boolean; maxDiagnosticAgeMs?: number; readRetries?: number; maxObservationAgeMs?: number; signal?: AbortSignal;
  resume?: CommitRecoveryStatusResult; refresh?: CommitRecoveryStatusResult;
  onProgress?: (status: CommitRecoveryStatusResult) => void;
 durableAlerts?: boolean; alertAcknowledgements?: CommitRecoveryAlertAcknowledgement[];
 onAlertAcknowledgements?: (status: CommitRecoveryStatusResult) => CommitRecoveryAlertAcknowledgement[];
} = {}): Promise<CommitRecoveryStatusResult> {
  const { limit, recipientPages, onProgress, signal } = options;
  const diagnostics = Boolean(options.diagnostics || options.watchDiagnostics);
  const maxObservationAgeMs = options.maxObservationAgeMs ?? 0;
  validateObservationAge(maxObservationAgeMs);
  validateObservationAge(options.maxDiagnosticAgeMs ?? 0);
  if ((options.maxDiagnosticAgeMs ?? 0) > 0 && !diagnostics) throw new TypeError("maximum diagnostic age requires diagnostics");
  const stallThresholdMs = options.stallThresholdMs ?? 0;
  validateObservationAge(stallThresholdMs);
  if (stallThresholdMs > 0 && (!diagnostics || !(options.maxDiagnosticAgeMs! > 0))) throw new TypeError("stall threshold requires diagnostics and a positive diagnostic age limit");
  if ((options.durableAlerts && !stallThresholdMs) || (!options.durableAlerts && ((options.alertAcknowledgements?.length ?? 0) > 0 || options.onAlertAcknowledgements))) throw new TypeError('durable alerts require stall detection; acknowledgements require durable alerts');
  const initialAcks = structuredClone(options.alertAcknowledgements ?? []);
  if(!Array.isArray(initialAcks) || initialAcks.length>commitRecoveryAlertQueueMax)throw new TypeError("invalid alert acknowledgement bounds");
  const readRetries = options.readRetries ?? 0;
  if (!Number.isInteger(readRetries) || readRetries < 0 || readRetries > 20) throw new TypeError('invalid recovery watch read retry budget');
  const intervalMs = options.intervalMs ?? 1000, timeoutMs = options.timeoutMs ?? 120000;
  if (!Number.isFinite(intervalMs) || intervalMs < 100 || intervalMs > 60000 || !Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 86400000 || (options.resume && options.refresh)) throw new TypeError('invalid recovery watch options');
  const fixedPlan = structuredClone(plan), fixedApplied = structuredClone(applied);
  let previous = options.resume || options.refresh ? structuredClone(options.resume ?? options.refresh) : undefined;
  let diagnosticPrevious = options.refresh ? undefined : previous;
  let refresh = Boolean(options.refresh) || Boolean(previous && previous.items.every(row => !['not_observed', 'read_error'].includes(row.status)));
  const controller = new AbortController(), abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true });
  if (signal?.aborted) abort();
  const timer = setTimeout(abort, timeoutMs), started = performance.now();
  let out: CommitRecoveryStatusResult = { source_id: source, items: [], counts: {}, remaining: 0, complete: false };
  let usedRetries = 0, passes = 0;
  let reason = 'read_failure', phase = 'read';
  const estimator = new DiagnosticEstimator();
  const stalls = new StallTracker();
  const incidents = new IncidentTracker(Boolean(options.durableAlerts));
  const alertPrevious = previous;
  let alertsRestored = false;
  const restoreAlerts = () => {if (!alertsRestored) {incidents.restore(alertPrevious,out,initialAcks);alertsRestored=true;}};
  let estimateDelayMs = intervalMs;
  const readDiagnostics = async (readSignal: AbortSignal, strictReads = false): Promise<CommitRecoveryStatusResult> => {
    const diagnosticStarted = performance.now();
    try {
      const sampled = await diagnoseCommitRecoveryStatus(out, readSignal, diagnosticPrevious, options.maxDiagnosticAgeMs, strictReads);
      estimator.observe(sampled, performance.now() - diagnosticStarted);
      stalls.observe(sampled, options.maxDiagnosticAgeMs ?? 0, stallThresholdMs);
      return sampled;
    } catch (error) {
      if (error instanceof CommitRecoveryStatusError) {estimator.observe(error.result, performance.now() - diagnosticStarted); stalls.observe(error.result, options.maxDiagnosticAgeMs ?? 0, stallThresholdMs);}
      throw error;
    }
  };
  const summarize = (stop_reason: string): CommitRecoveryMonitoring => {
    const consumers = stalls.summarize(out, options.maxDiagnosticAgeMs ?? 0, stallThresholdMs);
    const lifecycle = incidents.summarize(out, consumers, options.maxDiagnosticAgeMs ?? 0, stallThresholdMs);
    const freshness = maxObservationAgeMs > 0 ? freshnessCounts(out, maxObservationAgeMs) : undefined;
    return {
      watch_mode: options.watchDiagnostics ? "diagnostic_coverage" : "recovery_settlement", diagnostic_coverage_complete: stop_reason === "diagnostic_coverage_complete" ? Boolean(out.diagnostics_progress?.complete && out.diagnostics_progress?.fresh_complete) : diagnosticCoverageComplete(out, options.maxDiagnosticAgeMs ?? 0),
      diagnostic_estimate: estimator.estimate(out, performance.now() - started, estimateDelayMs),
      ...(options.durableAlerts && alertsRestored ? {alert_outbox: incidents.outbox(lifecycle.blocker_incidents)} : {}),
      ...(stallThresholdMs > 0 ? {stall_threshold_ms: stallThresholdMs, consumer_stalls: consumers, ...lifecycle} : {}),
      stop_reason, passes, retries_consumed: usedRetries, elapsed_ms: Math.max(0, Math.floor(performance.now() - started)),
      acknowledged_executions_terminal: commitRecoveryStatusSettled(out) && (!freshness || freshness.fresh === out.items.length),
      ...(freshness ? { max_observation_age_ms: maxObservationAgeMs, freshness } : {}),
    };
  };
  try {
    for (;;) {
      if (controller.signal.aborted || performance.now() - started >= timeoutMs) throw new CommitRecoveryStatusError(out, new Error('watch interrupted'));
      let delay = intervalMs, failed = false;
      incidents.beginPass();
      passes++;
      try {
        out = await getCommitRecoveryStatus(source, fixedPlan, fixedApplied, {
          limit, recipientPages, maxObservationAgeMs,
          timeoutMs: Math.max(1, timeoutMs - (performance.now() - started)), signal: controller.signal,
          ...(previous ? refresh ? { refresh: previous } : { resume: previous } : {}),
        });
        restoreAlerts();
        if (diagnostics) out = await readDiagnostics(controller.signal, Boolean(options.watchDiagnostics));
      } catch (error) {
        if (!(error instanceof CommitRecoveryStatusError)) throw error;
        out = error.result;
        if (!recoveryValidationFailure(error)) restoreAlerts();
        if (diagnostics && !out.diagnostics_progress) Object.assign(out, await readDiagnostics(AbortSignal.abort()));
        const retryAfter = recoveryReadRetry(error.cause);
        if (retryAfter === undefined || usedRetries >= readRetries || controller.signal.aborted) {
          if (retryAfter !== undefined && readRetries > 0 && usedRetries >= readRetries) reason = 'retry_exhausted';
          throw error;
        }
        usedRetries++; failed = true;
        delay = Math.max(Math.min(60000, intervalMs * 2 ** (usedRetries - 1)), retryAfter);
      }
      estimateDelayMs = delay;
      out.monitoring = summarize('running');
      phase = 'callback';
      onProgress?.(structuredClone(out));
      if (options.onAlertAcknowledgements) incidents.acknowledge(options.onAlertAcknowledgements(structuredClone(out)));
      if (incidents.queueFull) {reason = "alert_queue_full"; phase = "read"; throw new CommitRecoveryStatusError(out,new Error("alert queue full; acknowledge pending alerts and resume"));}
      phase = 'read';
      if (controller.signal.aborted || performance.now() - started >= timeoutMs) throw new CommitRecoveryStatusError(out, new Error('watch interrupted'));
      if (!failed && options.watchDiagnostics && diagnosticCoverageComplete(out, options.maxDiagnosticAgeMs ?? 0)) { out.monitoring = summarize('diagnostic_coverage_complete'); return out; }
      if (!failed && !options.watchDiagnostics && commitRecoveryStatusSettled(out) && observationsFresh(out, maxObservationAgeMs)) { out.monitoring = summarize('settled'); return out; }
      diagnosticPrevious = out;
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

const diagnosticReadMax = 50;
const diagnosticRefreshReserve = 1;

/** Rotate exact lookups on validated status; previous must have the same selection and attempts. */
export async function diagnoseCommitRecoveryStatus(status: CommitRecoveryStatusResult, signal?: AbortSignal, previous?: CommitRecoveryStatusResult, maxDiagnosticAgeMs = 0, strictReads = false): Promise<CommitRecoveryStatusResult> {
  validateObservationAge(maxDiagnosticAgeMs);
  const out = structuredClone(status), prior = structuredClone(previous ?? status);
  if (out.source_id.toLowerCase() !== prior.source_id.toLowerCase() || (out.subscription_id ?? '').toLowerCase() !== (prior.subscription_id ?? '').toLowerCase() || out.items.length !== prior.items.length || out.items.some((row, i) => !row.selection || !prior.items[i]?.selection || !sameSelection(row.selection, prior.items[i]!.selection!) || canonical(row.attempt) !== canonical(prior.items[i]!.attempt))) throw new TypeError('saved diagnostic selection mismatch');
  const cursor = (value: number | undefined) => Number.isSafeInteger(value) && value! >= 0 && value! < out.items.length ? value! : 0;
  const progress: CommitRecoveryDiagnosticProgress = { compared: 0, changed: 0, consumers: [], max_diagnostic_age_ms: maxDiagnosticAgeMs, freshness_checked_at: new Date().toISOString(), freshness: {fresh: 0, stale: 0, unread: 0}, fresh_complete: false, next_index: cursor(prior.diagnostics_progress?.next_index), retry_next_index: cursor(prior.diagnostics_progress?.retry_next_index), refresh_next_index: cursor(prior.diagnostics_progress?.refresh_next_index), target_count: 0, remaining: 0, deferred: 0, retained: 0, reads: 0, counts: Object.fromEntries(['available', 'not_waiting', 'unavailable', 'acceptance_replaced', 'read_error', 'budget_exhausted', 'deferred', 'unsupported_recipient'].map(key => [key, 0])), complete: false };
  const baselines: (CommitRecoveryDiagnosticObservation | undefined)[] = [];
  for (let i = 0; i < out.items.length; i++) {
    const row = out.items[i]!, old = prior.items[i]!.diagnostics;
    baselines[i] = diagnosticObservation(old, row);
    row.diagnostics = { status: 'budget_exhausted' };
    if (!diagnosticTarget(row)) { row.diagnostics.status = 'unsupported_recipient'; continue; }
    if (old && validDiagnostic(old, row)) { row.diagnostics = { ...old, retained: true, truncated: undefined }; setDiagnosticComparison(row.diagnostics, old.previous_observation, row); }
    else if (old?.status === 'read_error') row.diagnostics = { status: 'read_error', reason: old.reason };
    if (!diagnosticQueryable(row) && row.diagnostics.status === 'budget_exhausted') row.diagnostics = { status: 'deferred', reason: 'receipt_evidence_unavailable' };
  }
  const candidates = (start: number, group: number) => Array.from({length: out.items.length}, (_, n) => (start + n) % out.items.length).filter(index => {
    const row = out.items[index]!;
    const kind = validDiagnostic(row.diagnostics, row) ? 2 : row.diagnostics?.status === 'read_error' ? 1 : 0;
    return diagnosticQueryable(row) && kind === group;
  });
  const unread = candidates(progress.next_index, 0).slice(0, diagnosticReadMax);
  let remaining = diagnosticReadMax - unread.length;
  const failures = candidates(progress.retry_next_index, 1), successes = candidates(progress.refresh_next_index, 2);
  const reserve = successes.length > 0 && failures.length > 0 && remaining > diagnosticRefreshReserve ? diagnosticRefreshReserve : 0;
  const failed = failures.slice(0, remaining - reserve); remaining -= failed.length;
  if (maxDiagnosticAgeMs > 0) {
    const now = Date.now();
    successes.sort((a, b) => Number(diagnosticFreshness(out.items[b]!, now, maxDiagnosticAgeMs) === 'stale') - Number(diagnosticFreshness(out.items[a]!, now, maxDiagnosticAgeMs) === 'stale'));
  }
  const covered = successes.slice(0, remaining);
  const selected = [unread, failed, covered].flatMap((indices, group) => indices.map(index => ({index, group})));
  const queries = selected.map(({index}) => {
    const row = out.items[index]!, publication = row.selection!.publication;
    return {source: publication.source, id: publication.id, subscription_id: row.attempt.subscription_id.toLowerCase(), expected_accepted_at: publication.accepted_at};
  });
  let transportFailure: unknown;
  if (selected.length && !signal?.aborted) {
    const request = EventsService.getEventRecipientBlockers({requestBody: {queries}});
    const abort = () => request.cancel();
    signal?.addEventListener('abort', abort, {once: true});
    let batch: Awaited<typeof request> | undefined;
    try { batch = await request; } catch (error) { transportFailure = error; }
    finally { signal?.removeEventListener('abort', abort); }
    for (const [n, {index, group}] of selected.entries()) {
      const row = out.items[index]!, publication = row.selection!.publication, subscription = row.attempt.subscription_id.toLowerCase();
      progress.reads++;
      const next = (index + 1) % out.items.length;
      if (group === 0) progress.next_index = next;
      else if (group === 1) progress.retry_next_index = next;
      else progress.refresh_next_index = next;
      row.diagnostics = {status: 'read_error'};
      if (!batch) continue;
      const item = Array.isArray(batch.items) ? batch.items[n] : undefined;
      const query = queries[n]!;
      if (!Array.isArray(batch.items) || batch.items.length !== selected.length || !item || item.query?.source !== query.source || item.query.id !== query.id || item.query.subscription_id !== query.subscription_id || !sameAcceptance(item.query.expected_accepted_at, query.expected_accepted_at)) { row.diagnostics.reason = 'invalid_evidence'; continue; }
      if (item.error) continue;
      const result = item.result;
      if (!result) { row.diagnostics.reason = 'invalid_evidence'; continue; }
      if (result.query?.source !== publication.source || result.query.id !== publication.id || result.query.subscription_id !== subscription || !sameAcceptance(result.query.expected_accepted_at, publication.accepted_at)) { row.diagnostics.reason = 'invalid_evidence'; continue; }
      const diagnostic: CommitRecoveryDiagnostics = { status: result.status, reason: result.reason, routing_state: result.routing_state, observed_at: result.observed_at, coverage: 'exact_recipient', recipient: result.recipient };
      row.diagnostics = validDiagnostic(diagnostic, row) ? diagnostic : { status: 'read_error', reason: 'invalid_evidence' };
    }
  }
  for (const [i, row] of out.items.entries()) {
    if (row.diagnostics!.retained) continue;
    setDiagnosticComparison(row.diagnostics!, baselines[i], row);
    if (row.diagnostics!.comparison === "compared") { progress.compared++; if (row.diagnostics!.changes?.length) progress.changed++; }
  }
  progress.freshness_checked_at = new Date().toISOString();
  const checked = Date.parse(progress.freshness_checked_at);
  for (const row of out.items) {
    const diagnostic = row.diagnostics!;
    progress.counts[diagnostic.status] = (progress.counts[diagnostic.status] ?? 0) + 1;
    if (!diagnosticTarget(row)) continue;
    progress.target_count++;
    progress.freshness[diagnosticFreshness(row, checked, maxDiagnosticAgeMs)]!++;
    if (!diagnosticQueryable(row)) progress.deferred++;
    if (!validDiagnostic(diagnostic, row)) progress.remaining++;
    else if (diagnostic.retained) progress.retained++;
  }
  progress.complete = progress.remaining === 0;
  progress.fresh_complete = progress.freshness.fresh === progress.target_count;
  out.diagnostics_progress = progress;
  progress.consumers = diagnosticConsumers(out);
  if (strictReads && transportFailure !== undefined) throw new CommitRecoveryStatusError(out, transportFailure);
  return out;
}
function diagnosticTarget(row: CommitRecoveryStatusItem): boolean { return Boolean(row.selection && row.status !== 'unsupported_recipient' && row.selection.reviewed.reason !== 'unsupported_recipient'); }
function diagnosticQueryable(row: CommitRecoveryStatusItem): boolean { return diagnosticTarget(row) && !['not_observed', 'read_error'].includes(row.status); }
function validDiagnostic(diagnostic: CommitRecoveryDiagnostics | undefined, row: CommitRecoveryStatusItem): boolean {
  if (!diagnostic || !row.selection || diagnostic.coverage !== 'exact_recipient' || typeof diagnostic.observed_at !== 'string' || !sameAcceptance(diagnostic.observed_at, diagnostic.observed_at)) return false;
  if (diagnostic.status === 'unavailable') return !diagnostic.recipient && ['receipt_unavailable', 'snapshot_unavailable', 'recipient_unavailable', 'routing_evidence_unavailable'].includes(diagnostic.reason ?? '');
  if (diagnostic.status === 'acceptance_replaced') return !diagnostic.recipient && diagnostic.reason === 'acceptance_replaced';
  if (diagnostic.status === 'not_waiting') return !diagnostic.recipient && ['enqueued', 'filtered', 'failed'].includes(diagnostic.routing_state ?? '');
  if (diagnostic.status !== 'available') return false;
  const recipient = diagnostic.recipient, publication = row.selection.publication;
  return Boolean(recipient && recipient.event_source === publication.source && recipient.event_id === publication.id && typeof recipient.accepted_at === 'string' && sameAcceptance(recipient.accepted_at, publication.accepted_at) && typeof recipient.subscription_id === 'string' && recipient.subscription_id.toLowerCase() === row.attempt.subscription_id.toLowerCase() && recipient.consumer_kind === 'application' && recipient.state === diagnostic.routing_state && ['pending', 'processing'].includes(recipient.state));
}

function diagnosticFreshness(row: CommitRecoveryStatusItem, now: number, age: number): string {
  if (!validDiagnostic(row.diagnostics, row)) return 'unread';
  const observed = Date.parse(row.diagnostics!.observed_at!);
  return age > 0 && (!Number.isFinite(observed) || observed > now || now - observed > age) ? 'stale' : 'fresh';
}

export interface CommitRecoveryDiagnosticObservation {
  query: EventRecipientBlockerQuery; observed_at: string; status: string; routing_state: string; routing?: CommitRecoveryDiagnosticRouting;
}
export interface CommitRecoveryDiagnosticRouting {
  waiting_reason: string; capacity_scope?: string; target_available: boolean; ordering_blocker?: CommitRecoveryDiagnosticOrdering;
}
export interface CommitRecoveryDiagnosticOrdering {
  event_source: string; event_id: string; subscription_id: string; accepted_at: string; state: string;
}
export interface CommitRecoveryDiagnosticChange { field: string; previous: string; current: string; }
const diagnosticWaitingReasons = new Set(['circuit_open', 'circuit_probe_wait', 'circuit_recovery_rate_limited', 'subscription_paused', 'subscription_rate_limited', 'ordering_blocked', 'capacity_consumer', 'capacity_app', 'capacity_account', 'routing_in_progress', 'receipt_processing', 'retry_backoff', 'workflow_routing', 'ready']);
function diagnosticObservation(diagnostic: CommitRecoveryDiagnostics | undefined, row: CommitRecoveryStatusItem): CommitRecoveryDiagnosticObservation | undefined {
  if (!validDiagnostic(diagnostic, row) || !diagnostic || !['available', 'not_waiting'].includes(diagnostic.status)) return undefined;
  const publication = row.selection!.publication;
  const result: CommitRecoveryDiagnosticObservation = {query: {source: publication.source, id: publication.id, subscription_id: row.attempt.subscription_id.toLowerCase(), expected_accepted_at: publication.accepted_at}, observed_at: diagnostic.observed_at!, status: diagnostic.status, routing_state: diagnostic.routing_state ?? ''};
  if (diagnostic.recipient) {
    const recipient = diagnostic.recipient;
    result.routing = {waiting_reason: recipient.waiting_reason, capacity_scope: recipient.capacity_scope, target_available: recipient.target_available};
    const blocker = recipient.ordering_blocker;
    if (blocker) result.routing.ordering_blocker = {event_source: blocker.event_source, event_id: blocker.event_id, subscription_id: blocker.subscription_id, accepted_at: blocker.accepted_at, state: blocker.state};
  }
  return validDiagnosticObservation(result, result.query) ? result : undefined;
}
function validDiagnosticObservation(observation: CommitRecoveryDiagnosticObservation | undefined, query: EventRecipientBlockerQuery): boolean {
  if (!observation || !observation.query || observation.query.source !== query.source || observation.query.id !== query.id || observation.query.subscription_id !== query.subscription_id || typeof observation.query.expected_accepted_at !== 'string' || !sameAcceptance(observation.query.expected_accepted_at, query.expected_accepted_at) || typeof observation.observed_at !== 'string' || !sameAcceptance(observation.observed_at, observation.observed_at)) return false;
  if (observation.status === 'not_waiting') return !observation.routing && ['enqueued', 'filtered', 'failed'].includes(observation.routing_state);
  const routing = observation.routing;
  if (observation.status !== 'available' || !routing || !['pending', 'processing'].includes(observation.routing_state) || typeof routing.target_available !== 'boolean' || !diagnosticWaitingReasons.has(routing.waiting_reason) || !['', 'consumer', 'app', 'account'].includes(routing.capacity_scope ?? '')) return false;
  const blocker = routing.ordering_blocker;
  if (!blocker) return true;
  return [blocker.event_source, blocker.event_id, blocker.subscription_id].every(value => typeof value === 'string' && value.length > 0 && new TextEncoder().encode(value).length <= 256) && blocker.subscription_id.toLowerCase() === query.subscription_id.toLowerCase() && typeof blocker.accepted_at === 'string' && sameAcceptance(blocker.accepted_at, blocker.accepted_at) && ['pending', 'processing'].includes(blocker.state);
}
function setDiagnosticComparison(diagnostic: CommitRecoveryDiagnostics, previous: CommitRecoveryDiagnosticObservation | undefined, row: CommitRecoveryStatusItem): void {
  delete diagnostic.previous_observation; delete diagnostic.changes;
  diagnostic.comparison = 'evidence_unavailable';
  const current = diagnosticObservation(diagnostic, row);
  if (!current) return;
  diagnostic.comparison = 'baseline_unavailable';
  if (!validDiagnosticObservation(previous, current.query) || !previous) return;
  diagnostic.comparison = 'non_monotonic_timestamp';
  if (!diagnosticTimeAfter(current.observed_at, previous.observed_at)) return;
  diagnostic.comparison = 'compared'; diagnostic.previous_observation = cleanDiagnosticObservation(previous);
  const before = diagnosticRoutingFields(previous), after = diagnosticRoutingFields(current);
  const fields = ['status', 'routing_state'];
  if (previous.status === 'available' && current.status === 'available') fields.push('waiting_reason', 'capacity_scope', 'target_available', 'ordering_source', 'ordering_id', 'ordering_subscription_id', 'ordering_accepted_at', 'ordering_state');
  const changes = fields.filter(field => (before[field] ?? '') !== (after[field] ?? '') && !(field === 'ordering_accepted_at' && before[field] && after[field] && sameAcceptance(before[field]!, after[field]!))).map(field => ({field, previous: before[field] ?? '', current: after[field] ?? ''}));
  if (changes.length) diagnostic.changes = changes;
}
function diagnosticTimeAfter(current: string, previous: string): boolean {
  const pattern = /^(.+?)(?:\.([0-9]{1,9}))?(Z|[+-][0-9]{2}:[0-9]{2})$/;
  const a = pattern.exec(current)!, b = pattern.exec(previous)!;
  const left = Date.parse(a[1]! + a[3]!), right = Date.parse(b[1]! + b[3]!);
  return left > right || (left === right && (a[2] ?? '').padEnd(9, '0') > (b[2] ?? '').padEnd(9, '0'));
}
function diagnosticRoutingFields(observation: CommitRecoveryDiagnosticObservation): Record<string, string> {
  const result: Record<string, string> = {status: observation.status, routing_state: observation.routing_state};
  const routing = observation.routing;
  if (routing) {
    Object.assign(result, {waiting_reason: routing.waiting_reason, capacity_scope: routing.capacity_scope ?? '', target_available: String(routing.target_available)});
    const blocker = routing.ordering_blocker;
    if (blocker) Object.assign(result, {ordering_source: blocker.event_source, ordering_id: blocker.event_id, ordering_subscription_id: blocker.subscription_id.toLowerCase(), ordering_accepted_at: blocker.accepted_at, ordering_state: blocker.state});
  }
  return result;
}

function cleanDiagnosticObservation(observation: CommitRecoveryDiagnosticObservation): CommitRecoveryDiagnosticObservation {
 const query = observation.query;
 const clean: CommitRecoveryDiagnosticObservation = {query: {source: query.source, id: query.id, subscription_id: query.subscription_id, expected_accepted_at: query.expected_accepted_at}, observed_at: observation.observed_at, status: observation.status, routing_state: observation.routing_state};
 const routing = observation.routing;
 if (routing) {
  clean.routing = {waiting_reason: routing.waiting_reason, capacity_scope: routing.capacity_scope, target_available: routing.target_available};
  const blocker = routing.ordering_blocker;
  if (blocker) clean.routing.ordering_blocker = {event_source: blocker.event_source, event_id: blocker.event_id, subscription_id: blocker.subscription_id, accepted_at: blocker.accepted_at, state: blocker.state};
 }
 return clean;
}

function diagnosticCoverageComplete(out: CommitRecoveryStatusResult, age: number): boolean {
 const progress = out.diagnostics_progress;
 if (!progress) return false;
 progress.freshness_checked_at = new Date().toISOString();
 progress.max_diagnostic_age_ms = age;
 progress.freshness = {fresh: 0, stale: 0, unread: 0};
 progress.target_count = 0;
 const now = Date.parse(progress.freshness_checked_at);
 for (const row of out.items) {
  if (!diagnosticTarget(row)) continue;
  progress.target_count++;
  progress.freshness[diagnosticFreshness(row, now, age)]!++;
 }
 progress.remaining = progress.freshness.unread!;
 progress.complete = progress.remaining === 0;
 progress.fresh_complete = progress.freshness.fresh === progress.target_count;
 progress.consumers = diagnosticConsumers(out);
 return progress.complete && progress.fresh_complete;
}

export interface CommitRecoveryDiagnosticEstimate {
  state: string; target_count: number; remaining: number; deferred: number; diagnostic_passes: number; successful_samples: number;
  samples_per_second: number; samples_per_pass: number; estimated_remaining_passes?: number; estimated_remaining_ms?: number;
  estimated_refresh_ms?: number; refresh_exceeds_max_age: boolean;
}
class DiagnosticEstimator {
  private passes = 0;
  private samples = 0;
  private readMs = 0;
  observe(out: CommitRecoveryStatusResult, readMs: number): void {
    if (!out.diagnostics_progress?.reads) return;
    this.passes++; this.readMs += readMs;
    this.samples += out.items.filter(row => diagnosticTarget(row) && !row.diagnostics?.retained && validDiagnostic(row.diagnostics, row)).length;
  }
  estimate(out: CommitRecoveryStatusResult, elapsedMs: number, delayMs: number): CommitRecoveryDiagnosticEstimate | undefined {
    const progress = out.diagnostics_progress;
    if (!progress) return undefined;
    const targets = out.items.filter(diagnosticTarget), unread = targets.filter(row => !validDiagnostic(row.diagnostics, row));
    const result: CommitRecoveryDiagnosticEstimate = {state: 'insufficient_data', target_count: targets.length, remaining: unread.length, deferred: unread.filter(row => !diagnosticQueryable(row)).length, diagnostic_passes: this.passes, successful_samples: this.samples, samples_per_second: elapsedMs > 0 ? this.samples * 1000 / elapsedMs : 0, samples_per_pass: this.passes > 0 ? this.samples / this.passes : 0, refresh_exceeds_max_age: false};
    if (!result.remaining) { result.state = 'covered'; result.estimated_remaining_passes = 0; result.estimated_remaining_ms = 0; }
    else if (result.deferred) result.state = 'awaiting_receipts';
    if (result.deferred || !this.passes || !this.samples || elapsedMs <= 0) return result;
    const cycleMs = (elapsedMs + delayMs) / this.passes, refreshPasses = Math.ceil(result.target_count / result.samples_per_pass);
    result.estimated_refresh_ms = refreshPasses > 0 ? Math.ceil((refreshPasses - 1) * cycleMs + this.readMs / this.passes) : 0;
    result.refresh_exceeds_max_age = progress.max_diagnostic_age_ms > 0 && result.estimated_refresh_ms > progress.max_diagnostic_age_ms;
    if (result.remaining && !result.deferred) {
      result.state = 'estimated'; result.estimated_remaining_passes = Math.ceil(result.remaining / result.samples_per_pass);
      result.estimated_remaining_ms = Math.ceil(result.estimated_remaining_passes * cycleMs);
    }
    return result;
  }
}

export interface CommitRecoveryDiagnosticConsumer {
 subscription_id: string; target_count: number; freshness: Record<string, number>; statuses: Record<string, number>;
 fresh_waiting_reasons: Record<string, number>; stale_waiting_reasons: Record<string, number>; compared: number; changed: number;
 oldest_waiting?: {query: EventRecipientBlockerQuery; observed_at: string; freshness: string; waiting_reason: string};
}
function diagnosticConsumers(out: CommitRecoveryStatusResult): CommitRecoveryDiagnosticConsumer[] {
 const p = out.diagnostics_progress!;
 const groups = new Map<string, CommitRecoveryDiagnosticConsumer>();
 for (const row of out.items) {
  if (!diagnosticTarget(row)) continue;
  const id = row.attempt.subscription_id.toLowerCase();
  let g = groups.get(id);
  if (!g) {g = {subscription_id:id,target_count:0,freshness:{fresh:0,stale:0,unread:0},statuses:{},fresh_waiting_reasons:{},stale_waiting_reasons:{},compared:0,changed:0};groups.set(id,g);}
  g.target_count++;
  const freshness = diagnosticFreshness(row, Date.parse(p.freshness_checked_at), p.max_diagnostic_age_ms);
  g.freshness[freshness]!++;
  const d = row.diagnostics;
  if (!validDiagnostic(d,row)) continue;
  g.statuses[d!.status] = (g.statuses[d!.status] ?? 0)+1;
  if (!d!.retained && d!.comparison === 'compared') {g.compared++;if(d!.changes?.length)g.changed++;}
  if(d!.status !== 'available')continue;
  const reason = d!.recipient!.waiting_reason;
  const reasons = freshness === 'stale' ? g.stale_waiting_reasons : g.fresh_waiting_reasons;
  reasons[reason] = (reasons[reason] ?? 0)+1;
  const pub = row.selection!.publication;
  if(!g.oldest_waiting || diagnosticTimeAfter(g.oldest_waiting.query.expected_accepted_at, pub.accepted_at)) {
   g.oldest_waiting = {query:{source:pub.source,id:pub.id,subscription_id:id,expected_accepted_at:pub.accepted_at},observed_at:d!.observed_at!,freshness,waiting_reason:reason};
  }
 }
 return [...groups.values()].sort((a,b)=>a.subscription_id < b.subscription_id ? -1 : a.subscription_id > b.subscription_id ? 1 : 0);
}

export interface CommitRecoveryPersistentBlocker {
 query: EventRecipientBlockerQuery; waiting_reason: string; state: string; first_observed_at: string; last_observed_at: string; samples: number; monitored_ms: number;
}
export interface CommitRecoveryConsumerStall {
 subscription_id: string; counts: Record<string,number>; blockers: CommitRecoveryPersistentBlocker[];
}
interface StallSample { observation: CommitRecoveryDiagnosticObservation; first: string; started: number; last: number; samples: number; advancing: boolean; changed: boolean; }
const persistentBlockerReasons = new Set(['subscription_paused','subscription_rate_limited','circuit_open','circuit_probe_wait','circuit_recovery_rate_limited','ordering_blocked','capacity_consumer','capacity_app','capacity_account','retry_backoff']);
class StallTracker {
 private samples = new Map<number,StallSample>();
 observe(out: CommitRecoveryStatusResult, age: number, threshold: number): void {
  if(!threshold)return;
  const now=performance.now(), wall=Date.now();
  out.items.forEach((row,i)=>{
   let previous=this.samples.get(i);
   const current=diagnosticObservation(row.diagnostics,row);
   if(!current || diagnosticFreshness(row,wall,age)!=='fresh'){this.samples.delete(i);return;}
   if(previous && (Date.parse(previous.observation.observed_at)>wall || wall-Date.parse(previous.observation.observed_at)>age)){this.samples.delete(i);previous=undefined;}
   if(row.diagnostics!.retained)return;
   const sample: StallSample={observation:current,first:current.observed_at,started:now,last:now,samples:1,advancing:false,changed:false};
   if(previous && diagnosticTimeAfter(current.observed_at,previous.observation.observed_at)){
    const before=diagnosticRoutingFields(previous.observation),after=diagnosticRoutingFields(current);
    if(Object.keys({...before,...after}).every(key=>key === 'ordering_accepted_at' && before[key] && after[key] ? sameAcceptance(before[key]!,after[key]!) : before[key]===after[key])){sample.first=previous.first;sample.started=previous.started;sample.samples=previous.samples+1;}
    else {sample.changed=true;sample.advancing=(previous.observation.routing_state==='pending' && current.routing_state==='processing') || (previous.observation.status==='available' && current.status==='not_waiting' && current.routing_state==='enqueued');}
   }else if(previous){this.samples.delete(i);return;}
   this.samples.set(i,sample);
  });
 }
 summarize(out: CommitRecoveryStatusResult, age: number, threshold: number): CommitRecoveryConsumerStall[] {
  if(!threshold || !out.diagnostics_progress)return [];
  const groups=new Map(out.diagnostics_progress.consumers.map(c=>[c.subscription_id,{subscription_id:c.subscription_id,counts:{insufficient_evidence:0,advancing:0,routing_changed:0,not_blocked:0,observing:0,stalled:0} as Record<string,number>,blockers:[] as CommitRecoveryPersistentBlocker[]}]));
  const now=Date.now();
  out.items.forEach((row,i)=>{
   if(!diagnosticTarget(row))return;
   const g=groups.get(row.attempt.subscription_id.toLowerCase())!,sample=this.samples.get(i);
   if(!sample || diagnosticFreshness(row,now,age)!=='fresh' || !sameAcceptance(row.diagnostics!.observed_at!,sample.observation.observed_at)){this.samples.delete(i);g.counts.insufficient_evidence!++;return;}
   if(sample.changed)g.counts.routing_changed!++;
   if(sample.advancing)g.counts.advancing!++;
   const o=sample.observation;
   if(!o.routing || !persistentBlockerReasons.has(o.routing.waiting_reason)){g.counts.not_blocked!++;return;}
   const duration=Math.max(0,Math.floor(sample.last-sample.started)),state=sample.samples>=2 && sample.last-sample.started>=threshold ? 'stalled':'observing';
   g.counts[state]!++;
   g.blockers.push({query:o.query,waiting_reason:o.routing.waiting_reason,state,first_observed_at:sample.first,last_observed_at:o.observed_at,samples:sample.samples,monitored_ms:duration});
  });
  return [...groups.values()];
 }
}

export interface CommitRecoveryBlockerIncident {
 incident_id: string; query: EventRecipientBlockerQuery; waiting_reason: string; opened_at: string; last_observed_at: string;
}
export interface CommitRecoveryBlockerTransition {
 sequence: number; incident_id: string; transition: string; reason: string; at: string; query: EventRecipientBlockerQuery; waiting_reason: string; evidence?: CommitRecoveryDiagnosticObservation;
}
function incidentRecipientKey(query: EventRecipientBlockerQuery): string {
 return JSON.stringify([query.source,query.id,query.subscription_id.toLowerCase(),query.expected_accepted_at]);
}
class IncidentTracker {
 constructor(private durable = false) {}
 queueFull = false;
 private pending: CommitRecoveryBlockerTransition[] = [];
 private session = randomUUID().replaceAll('-','');
 private sequence = 0;
 private active = new Map<number,{incident: CommitRecoveryBlockerIncident; first: string}>();
 private events: CommitRecoveryBlockerTransition[] = [];
 beginPass(): void {this.events=[];}

 private canOpen(): boolean {
  if(!this.durable)return true;
  if(this.queueFull)return false;
  if(this.pending.length+this.active.size+2>commitRecoveryAlertQueueMax || this.sequence+this.active.size+2>commitRecoveryAlertSequenceMax){this.queueFull=true;return false;}
  return true;
 }
 acknowledge(acks: CommitRecoveryAlertAcknowledgement[]): void {
  if(!Array.isArray(acks) || acks.length>commitRecoveryAlertQueueMax || (!this.durable && acks.length))throw new TypeError('invalid alert acknowledgements');
  const known=new Set(this.pending.map(e=>JSON.stringify([e.incident_id,e.sequence]))),pendingSequences=new Set(this.pending.map(e=>e.sequence)),selected=new Set<string>();
  for(const ack of acks){const key=JSON.stringify([ack?.incident_id,ack?.sequence]),opened=alertIncidentSequence(ack?.incident_id,this.session),alreadyRemoved=opened!==undefined && Number.isSafeInteger(ack?.sequence) && opened<=ack.sequence && ack.sequence<=this.sequence && !pendingSequences.has(ack.sequence);if(selected.has(key) || (!known.has(key) && !alreadyRemoved))throw new TypeError('acknowledgement does not match this alert stream');selected.add(key);}
  this.pending=this.pending.filter(e=>!selected.has(JSON.stringify([e.incident_id,e.sequence])));
 }
 outbox(active: CommitRecoveryBlockerIncident[]): CommitRecoveryAlertOutbox {
  return {version:1,session:this.session,sequence:this.sequence,pending:[...this.pending],active:[...active]};
 }
 restore(previous: CommitRecoveryStatusResult | undefined, out: CommitRecoveryStatusResult, acks: CommitRecoveryAlertAcknowledgement[]): void {
  const saved=previous?.monitoring?.alert_outbox;
  if(!saved){this.acknowledge(acks);return;}
  if(!this.durable)throw new TypeError('resuming pending alert state requires durable alerts');
  if(out.source_id.toLowerCase()!==previous!.source_id.toLowerCase() || (out.subscription_id??'').toLowerCase()!==(previous!.subscription_id??'').toLowerCase() || out.items.length!==previous!.items.length || out.items.some((row,i)=>!row.selection || !previous!.items[i]?.selection || !sameSelection(row.selection,previous!.items[i]!.selection!) || canonical(row.attempt)!==canonical(previous!.items[i]!.attempt)))throw new TypeError('saved alert selection mismatch');
  const normalized=validateAlertOutbox(saved,out);
  const staged=new IncidentTracker(true);staged.session=normalized.session;staged.sequence=normalized.sequence;staged.pending=normalized.pending;staged.acknowledge(acks);
  this.session=normalized.session;this.sequence=normalized.sequence;this.pending=staged.pending;
  for(const incident of normalized.active)this.emit(incident,'confirmation_lost','watch_resumed');
 }
 private emit(incident: CommitRecoveryBlockerIncident, transition: string, reason: string, evidence?: CommitRecoveryDiagnosticObservation): void {
  const event={sequence:++this.sequence,incident_id:incident.incident_id,transition,reason,at:new Date().toISOString(),query:incident.query,waiting_reason:incident.waiting_reason,...(evidence ? {evidence}: {})};
  this.events.push(event); if(this.durable)this.pending.push(event);
 }
 summarize(out: CommitRecoveryStatusResult, consumers: CommitRecoveryConsumerStall[], age: number, threshold: number): {blocker_incidents: CommitRecoveryBlockerIncident[]; blocker_transitions: CommitRecoveryBlockerTransition[]} {
  if(!threshold)return {blocker_incidents:[],blocker_transitions:[]};
  const waiting=new Map(consumers.flatMap(c=>c.blockers.filter(b=>b.state==='stalled').map(b=>[incidentRecipientKey(b.query),b] as const)));
  const now=Date.now(),seen=new Set<number>();
  out.items.forEach((row,i)=>{
   if(!diagnosticTarget(row))return;
   seen.add(i);
   const pub=row.selection!.publication,query={source:pub.source,id:pub.id,subscription_id:row.attempt.subscription_id.toLowerCase(),expected_accepted_at:pub.accepted_at};
   const blocker=waiting.get(incidentRecipientKey(query));
   let active=this.active.get(i);
   const current=diagnosticObservation(row.diagnostics,row),fresh=Boolean(current && diagnosticFreshness(row,now,age)==='fresh');
   if(active){
    const last=Date.parse(active.incident.last_observed_at),uninterrupted=last<=now && now-last<=age,sameEpisode=Boolean(blocker && sameAcceptance(blocker.first_observed_at,active.first));
    if(!fresh || !uninterrupted || !sameEpisode){
     let transition='confirmation_lost',reason='episode_reset';
     if(!fresh)reason='evidence_unavailable';
     else if(!uninterrupted)reason='sample_gap';
     else if(!row.diagnostics!.retained && diagnosticTimeAfter(current!.observed_at,active.incident.last_observed_at) && (current!.status==='not_waiting' || (current!.routing && !persistentBlockerReasons.has(current!.routing.waiting_reason)))){transition='resolved';reason='routing_not_blocked';}
     this.emit(active.incident,transition,reason,fresh ? current : undefined);
     this.active.delete(i);active=undefined;
    }
   }
   if(blocker && fresh){
    if(!active){
     if(!this.canOpen())return;
     const incident={incident_id:this.session+'-'+(this.sequence+1),query,waiting_reason:blocker.waiting_reason,opened_at:new Date(now).toISOString(),last_observed_at:current!.observed_at};
     active={incident,first:blocker.first_observed_at};this.active.set(i,active);this.emit(incident,'opened','persistent_blocker',current);
    }
    active.incident.last_observed_at=current!.observed_at;
   }
  });
  for(const [i,active] of [...this.active.entries()].sort((a,b)=>a[0]-b[0])){if(!seen.has(i)){this.emit(active.incident,'confirmation_lost','selection_unavailable');this.active.delete(i);}}
  return {blocker_incidents:[...this.active.entries()].sort((a,b)=>a[0]-b[0]).map(([,a])=>({...a.incident})),blocker_transitions:[...this.events]};
 }
}

export interface CommitRecoveryAlertAcknowledgement {incident_id: string; sequence: number;}
export interface CommitRecoveryAlertOutbox {version: number; session: string; sequence: number; pending: CommitRecoveryBlockerTransition[]; active: CommitRecoveryBlockerIncident[];}
const commitRecoveryAlertQueueMax=1024;
const commitRecoveryAlertSequenceMax=9007199254740991;
function alertIncidentSequence(id: string, session: string): number | undefined {
 if(typeof id!=='string' || !id.startsWith(session+'-'))return undefined;
 const text=id.slice(session.length+1),value=Number(text);
 return /^[1-9][0-9]*$/.test(text) && Number.isSafeInteger(value) && String(value)===text ? value:undefined;
}
function validAlertTime(value: string): boolean {return typeof value==='string' && sameAcceptance(value,value);}
function validateAlertOutbox(saved: CommitRecoveryAlertOutbox,out: CommitRecoveryStatusResult): CommitRecoveryAlertOutbox {
 const pending=saved.pending??[],active=saved.active??[];
 if(saved.version!==1 || typeof saved.session!=='string' || !/^[0-9a-f]{32}$/.test(saved.session) || !Number.isSafeInteger(saved.sequence) || saved.sequence<0 || !Array.isArray(pending) || !Array.isArray(active) || pending.length+active.length>commitRecoveryAlertQueueMax || active.length>commitRecoveryAlertSequenceMax-saved.sequence)throw new TypeError('invalid saved alert outbox bounds');
 const boundQuery=(query: EventRecipientBlockerQuery): EventRecipientBlockerQuery=>{
  if(!query || ![query.source,query.id,query.subscription_id].every(v=>typeof v==='string' && v.length>0 && new TextEncoder().encode(v).length<=256) || !validAlertTime(query.expected_accepted_at))throw new TypeError('invalid alert query');
  const row=out.items.find(r=>diagnosticTarget(r) && r.selection!.publication.source===query.source && r.selection!.publication.id===query.id && r.attempt.subscription_id.toLowerCase()===query.subscription_id.toLowerCase() && sameAcceptance(r.selection!.publication.accepted_at,query.expected_accepted_at));
  if(!row)throw new TypeError('saved alert query does not match selection');
  return {source:query.source,id:query.id,subscription_id:query.subscription_id,expected_accepted_at:row.selection!.publication.accepted_at};
 };
 let last=0;
 const lineage=new Map<string,string>(),closed=new Set<string>();
 const cleanPending=pending.map(event=>{
  const opened=alertIncidentSequence(event.incident_id,saved.session),query=boundQuery(event.query);
  if(!opened || !Number.isSafeInteger(event.sequence) || event.sequence<=last || opened>event.sequence || event.sequence>saved.sequence || !validAlertTime(event.at) || !persistentBlockerReasons.has(event.waiting_reason) || (event.evidence != null && !validDiagnosticObservation(event.evidence,query)))throw new TypeError('invalid saved pending alert');
  const evidence=event.evidence;
  const valid=event.transition==='opened' ? opened===event.sequence && event.reason==='persistent_blocker' && evidence?.status==='available' && evidence.routing!.waiting_reason===event.waiting_reason
    :event.transition==='resolved' ? event.reason==='routing_not_blocked' && evidence && (evidence.status==='not_waiting' || !persistentBlockerReasons.has(evidence.routing!.waiting_reason))
    :event.transition==='confirmation_lost' && ['episode_reset','evidence_unavailable','sample_gap','selection_unavailable','watch_resumed'].includes(event.reason);
  if(!valid)throw new TypeError('invalid alert transition');
  const identity=JSON.stringify([incidentRecipientKey(query),event.waiting_reason]);
  if(closed.has(event.incident_id) || (lineage.has(event.incident_id) && lineage.get(event.incident_id)!==identity))throw new TypeError('inconsistent saved alert episode');
  lineage.set(event.incident_id,identity);if(event.transition!=='opened')closed.add(event.incident_id);
  last=event.sequence;
  return {sequence:event.sequence,incident_id:event.incident_id,transition:event.transition,reason:event.reason,at:event.at,query,waiting_reason:event.waiting_reason,...(evidence ? {evidence:cleanDiagnosticObservation(evidence)}:{})};
 });
 const ids=new Set<string>(),queries=new Set<string>();
 const cleanActive=active.map(incident=>{
  const opened=alertIncidentSequence(incident.incident_id,saved.session),query=boundQuery(incident.query),key=incidentRecipientKey(query);
  if(!opened || opened>saved.sequence || closed.has(incident.incident_id) || (lineage.has(incident.incident_id) && lineage.get(incident.incident_id)!==JSON.stringify([key,incident.waiting_reason])) || ids.has(incident.incident_id) || queries.has(key) || !validAlertTime(incident.opened_at) || !validAlertTime(incident.last_observed_at) || !persistentBlockerReasons.has(incident.waiting_reason))throw new TypeError('invalid saved active alert');
  ids.add(incident.incident_id);queries.add(key);
  return {incident_id:incident.incident_id,query,waiting_reason:incident.waiting_reason,opened_at:incident.opened_at,last_observed_at:incident.last_observed_at};
 });
 return {version:1,session:saved.session,sequence:saved.sequence,pending:cleanPending,active:cleanActive};
}
