import { getCommitDeliveryReport, CommitDeliveryReportError } from './commit-delivery-report.js';
import type { CommitDeliveryReportItem } from './commit-delivery-report.js';
import type { CommitRecoveryPlan } from './commit-recovery-plan.js';
import type { CommitRecoveryApplyItem, CommitRecoveryApplyResult } from './commit-recovery-apply.js';
import type { EventReceiptRecipientResponse } from './generated/models/EventReceiptRecipientResponse.js';

export interface CommitRecoveryStatusItem {
  attempt: CommitRecoveryApplyItem; status: string; reason?: string; attribution: string;
  observed_at?: string; expected_generation?: number; generation?: number; generation_relation?: string;
  recipient?: EventReceiptRecipientResponse;
}
export interface CommitRecoveryStatusResult {
  source_id: string; items: CommitRecoveryStatusItem[]; counts: Record<string, number>; complete: boolean;
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
  options: { limit?: number; recipientPages?: number; timeoutMs?: number; signal?: AbortSignal } = {}): Promise<CommitRecoveryStatusResult> {
  const { limit = 50, recipientPages = 1, timeoutMs = 120000, signal } = options;
  const plan = structuredClone(reviewed), result = structuredClone(applied);
  const out: CommitRecoveryStatusResult = { source_id: source, items: [], counts: Object.create(null), complete: false };
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
    out.items.push({ attempt: item, status: 'not_observed', reason: 'status_budget', attribution: 'unproven',
      expected_generation: generation !== undefined && Number.isSafeInteger(generation) && generation >= 0 ? generation : undefined });
  }
  const controller = new AbortController(), abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true }); if (signal?.aborted) abort();
  const timer = setTimeout(abort, timeoutMs);
  let cause: unknown;
  try {
    await getCommitDeliveryReport(source, { refresh: plan.report, recipientPages: 1, timeoutMs, signal: controller.signal });
    out.complete = result.items.length <= limit;
    for (let i = 0; i < out.items.length && i < limit; i++) {
      const row = out.items[i]!;
      if (controller.signal.aborted) { row.reason = 'interrupted'; out.complete = false; cause ??= new Error('status interrupted'); continue; }
      row.status = 'unresolved'; row.reason = 'evidence_unavailable';
      const original = publications.get(row.attempt.event_id.toLowerCase())!;
      try {
        const report = await getCommitDeliveryReport(source, { refresh: { ...plan.report, subscription_id: row.attempt.subscription_id.toLowerCase(), items: [{ ...original, subscribers: [] }] }, recipientPages, timeoutMs, signal: controller.signal });
        observe(row, report.items[0]);
      } catch (error) {
        if (error instanceof CommitDeliveryReportError) observe(row, error.report.items[0]);
        row.status = 'read_error'; row.reason = 'evidence_read_failed'; out.complete = false; cause ??= error;
      }
    }
  } catch (error) { out.complete = false; cause = error; for (const row of out.items) row.reason = 'validation_read_failed'; }
  finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); }
  for (const row of out.items) out.counts[row.status] = (out.counts[row.status] ?? 0) + 1;
  if (cause) throw new CommitRecoveryStatusError(out, cause);
  return out;
}
