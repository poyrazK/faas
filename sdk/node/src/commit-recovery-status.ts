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
export interface CommitRecoveryStatusItem {
  selection?: CommitRecoveryStatusSelection;
  attempt: CommitRecoveryApplyItem; status: string; reason?: string; attribution: string;
  observed_at?: string; expected_generation?: number; generation?: number; generation_relation?: string;
  recipient?: EventReceiptRecipientResponse;
}
export interface CommitRecoveryStatusResult {
  source_id: string; subscription_id?: string; remaining: number; items: CommitRecoveryStatusItem[]; counts: Record<string, number>; complete: boolean;
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
  let execution = recipient.recovery?.latest_replay ?? recipient.execution;
  const ack = row.attempt.result as { id?: unknown } | undefined;
  if (row.attempt.status === 'queued' && typeof ack?.id === 'string' && /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(ack.id)) {
    const matching = [recipient.execution, recipient.recovery?.latest_replay].find(item => item?.invocation_id.toLowerCase() === (ack.id as string).toLowerCase());
    if (matching) { execution = matching; row.attribution = 'matches_acknowledged_execution'; }
  }
  let state: string | undefined = execution?.state ?? recipient.routing?.state;
  if (!execution && recipient.cancellation) state = 'cancelled';
  if (!execution && recipient.execution_unavailable && state !== 'cancelled') { row.status = 'unresolved'; row.reason = recipient.execution_unavailable; return; }
  row.reason = state || 'evidence_unavailable';
  row.status = state === 'completed' ? 'completed' : ['pending', 'processing', 'dispatching'].includes(state ?? '') ? 'active'
    : ['failed', 'dead_letter', 'cancelled', 'expired', 'superseded'].includes(state ?? '') ? 'failed' : 'unresolved';
}
/** Read guarded evidence only. An unknown attempt remains unknown even when delivery advances. */
export async function getCommitRecoveryStatus(source: string, reviewed: CommitRecoveryPlan, applied: CommitRecoveryApplyResult,
  options: { limit?: number; recipientPages?: number; timeoutMs?: number; signal?: AbortSignal; resume?: CommitRecoveryStatusResult } = {}): Promise<CommitRecoveryStatusResult> {
  const { limit = 50, recipientPages = 1, timeoutMs = 120000, signal } = options;
  const plan = structuredClone(reviewed), result = structuredClone(applied), previous = options.resume ? structuredClone(options.resume) : undefined;
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
    if (previous.items.some(row => !row.selection)) throw new TypeError('saved status lacks selection snapshots; run recovery-status without --resume');
    if (previous.source_id.toLowerCase() !== source.toLowerCase() || (previous.subscription_id ?? '').toLowerCase() !== (out.subscription_id ?? '').toLowerCase() || previous.items.length !== out.items.length) throw new TypeError('saved status selection mismatch');
    for (let i = 0; i < previous.items.length; i++) {
      const row = previous.items[i]!, expected = out.items[i]!;
      if (!row.selection || !sameSelection(row.selection, expected.selection!) || canonical(row.attempt) !== canonical(expected.attempt) || row.expected_generation !== expected.expected_generation) throw new TypeError('saved status does not match reviewed plan and apply result');
      if (row.observed_at !== undefined && (typeof row.observed_at !== 'string' || !sameAcceptance(row.observed_at, row.observed_at))) throw new TypeError('invalid saved observation timestamp');
      if (!['completed', 'active', 'failed', 'unresolved', 'unsupported_recipient', 'read_error', 'not_observed'].includes(row.status) || !['unproven', 'matches_acknowledged_execution'].includes(row.attribution) || (row.attribution === 'matches_acknowledged_execution' && row.attempt.status !== 'queued')) throw new TypeError('invalid saved observation or attribution');
    }
    out.items = previous.items;
  }
  recount(out);
  const pending = ['not_observed', 'read_error'].flatMap(status => out.items.map((row, index) => row.status === status ? index : -1).filter(index => index >= 0));
  const controller = new AbortController(), abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true }); if (signal?.aborted) abort();
  const timer = setTimeout(abort, timeoutMs);
  let cause: unknown;
  try {
    await getCommitDeliveryReport(source, { refresh: plan.report, recipientPages: 1, timeoutMs, signal: controller.signal });
    for (const index of pending.slice(0, limit)) {
      if (controller.signal.aborted) { cause ??= new Error('status interrupted'); break; }
      const old = out.items[index]!;
      const row: CommitRecoveryStatusItem = { attempt: old.attempt, selection: old.selection, expected_generation: old.expected_generation, status: 'unresolved', reason: 'evidence_unavailable', attribution: 'unproven' };
      out.items[index] = row;
      const original = publications.get(row.attempt.event_id.toLowerCase())!;
      try {
        const report = await getCommitDeliveryReport(source, { refresh: { ...plan.report, subscription_id: row.attempt.subscription_id.toLowerCase(), items: [{ ...original, subscribers: [] }] }, recipientPages, timeoutMs, signal: controller.signal });
        observe(row, report.items[0]);
      } catch (error) {
        if (error instanceof CommitDeliveryReportError) observe(row, error.report.items[0]);
        row.status = 'read_error'; row.reason = 'evidence_read_failed'; out.complete = false; cause ??= error;
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
    out.counts[row.status] = (out.counts[row.status] ?? 0) + 1;
    if (['not_observed', 'read_error'].includes(row.status)) out.remaining++;
  }
  out.complete = out.remaining === 0;
}
