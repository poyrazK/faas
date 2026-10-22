import { getCommitDeliveryReport, CommitDeliveryReportError } from './commit-delivery-report.js';
import type { CommitDeliveryReport, CommitDeliveryReportSubscriber } from './commit-delivery-report.js';
import type { EventReceiptRecoveryAction } from './generated/models/EventReceiptRecoveryAction.js';

export interface CommitRecoveryPlanItem {
  event_id: string; subscription_id: string; status: 'eligible' | 'skipped';
  reason?: string; action?: EventReceiptRecoveryAction;
}
export interface CommitRecoveryPlan {
  report: CommitDeliveryReport; publication_issues: Record<string, string>; items: CommitRecoveryPlanItem[]; counts: Record<string, number>;
}
export class CommitRecoveryPlanError extends Error {
  constructor(readonly plan: CommitRecoveryPlan, cause: unknown) {
    super('Commit recovery plan read failed; partial evidence is attached', { cause });
    this.name = 'CommitRecoveryPlanError';
  }
}

export function sameAcceptance(left: string, right: string): boolean {
  const pattern = /^(.+?)(?:\.([0-9]{1,9}))?(Z|[+-][0-9]{2}:[0-9]{2})$/;
  const a = pattern.exec(left), b = pattern.exec(right);
  if (!a || !b) return false;
  const instant = Date.parse(a[1]! + a[3]!);
  return Number.isFinite(instant) && instant === Date.parse(b[1]! + b[3]!) && (a[2] ?? '').padEnd(9, '0') === (b[2] ?? '').padEnd(9, '0');
}
function eligibility(row: CommitDeliveryReportSubscriber): Pick<CommitRecoveryPlanItem, 'status' | 'reason' | 'action'> {
  const skip = (reason: string): Pick<CommitRecoveryPlanItem, 'status' | 'reason'> => ({ status: 'skipped', reason });
  if (row.status === 'not_targeted') return skip('not_targeted');
  const r = row.recipient;
  if (!r) return skip('evidence_unavailable');
  if (r.workflow_name) return skip('unsupported_recipient');
  const execution = r.recovery?.latest_replay ?? r.execution;
  if (execution?.state === 'completed') return skip('completed');
  if (execution && ['pending', 'dispatching'].includes(execution.state)) return skip('delivery_active');
  if (!r.recovery_actions?.length) {
    if (r.execution_unavailable) return skip('evidence_unavailable');
    if (r.cancellation) return skip('cancelled');
    return skip('no_recovery_action');
  }
  if (r.recovery_actions.length !== 1) return skip('unsupported_action');
  const action = r.recovery_actions[0]!;
  if (action.method !== 'POST') return skip('unsupported_action');
  const uuid = /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;
  const slug = !!r.app_slug && /^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$/.test(r.app_slug) && !r.app_slug.startsWith('tag-');
  switch (action.kind) {
    case 'routing_replay': {
      const b = action.body;
      if (!slug || action.url !== `/v1/apps/${r.app_slug}/event-deliveries:replay-fanout-failure` || !b || b.allow_expired
        || b.event_source !== row.publication.source || b.event_id !== row.publication.id || (typeof b.subscription_id === 'string' ? b.subscription_id.toLowerCase() : '') !== row.subscription_id.toLowerCase()) return skip('action_identity_mismatch');
      if (b.expected_generation !== undefined && (!Number.isSafeInteger(b.expected_generation) || b.expected_generation < 0 || !b.expected_accepted_at || b.expected_generation !== r.routing?.generation)) return skip('action_identity_mismatch');
      if (b.expected_accepted_at && !sameAcceptance(b.expected_accepted_at, row.publication.accepted_at)) return skip('action_identity_mismatch');
      break;
    }
    case 'handler_replay': case 'keyed_handler_replay': {
      const suffix = action.kind === 'handler_replay' ? '/replay' : '/replay-keyed';
      if (!execution || !uuid.test(execution.invocation_id) || action.url !== `/v1/invocations/${execution.invocation_id}${suffix}` || action.body) return skip('action_identity_mismatch');
      break;
    }
    case 'dead_letter_replay': {
      const prefix = `/v1/apps/${r.app_slug}/dlq/`;
      const target = action.url.startsWith(prefix) && action.url.endsWith('/replay') ? action.url.slice(prefix.length, -7) : '';
      if (!slug || !uuid.test(target) || action.url !== `${prefix}${target}/replay` || action.body) return skip('action_identity_mismatch');
      break;
    }
    default: return skip('unsupported_action');
  }
  return { status: 'eligible', action };
}
function buildPlan(report: CommitDeliveryReport): CommitRecoveryPlan {
  const plan: CommitRecoveryPlan = { report, publication_issues: Object.create(null), items: [], counts: Object.create(null) };
  for (const item of report.items) {
    if (item.evidence_status !== 'available') plan.publication_issues[item.acceptance.event_id] = item.evidence_status;
    if (!['available', 'partial', 'read_error'].includes(item.evidence_status)) continue;
    for (const row of item.subscribers) {
      const result = eligibility(row);
      plan.items.push({ event_id: item.acceptance.event_id, subscription_id: row.subscription_id, ...result });
      plan.counts[result.status] = (plan.counts[result.status] ?? 0) + 1;
    }
  }
  return plan;
}
/** Refresh a fixed selection and describe current recovery actions; never execute them. */
export async function planCommitRecovery(source: string, saved: CommitDeliveryReport,
  options: { recipientPages?: number; timeoutMs?: number; signal?: AbortSignal } = {}): Promise<CommitRecoveryPlan> {
  try { return buildPlan(await getCommitDeliveryReport(source, { ...options, refresh: saved })); }
  catch (error) {
    if (error instanceof CommitDeliveryReportError) throw new CommitRecoveryPlanError(buildPlan(error.report), error);
    throw error;
  }
}
