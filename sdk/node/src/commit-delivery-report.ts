import { QueuesService } from './generated/services/QueuesService.js';
import { EventsService } from './generated/services/EventsService.js';
import type { CancelablePromise } from './generated/core/CancelablePromise.js';
import type { CommitPublicationHistoryItem } from './generated/models/CommitPublicationHistoryItem.js';
import { commitSubscriberStatus, type CommitSubscriberWaitResult } from './commit-subscriber-wait.js';

export interface CommitDeliveryReportRecovery {
  status: 'recovered' | 'pending' | 'failed' | 'unavailable';
  invocation_id?: string; state?: string; history_url?: string; retained_replay_count?: number;
}
export interface CommitDeliveryReportSubscriber extends CommitSubscriberWaitResult {
  recovery?: CommitDeliveryReportRecovery;
}
function reportRecovery(row: NonNullable<CommitSubscriberWaitResult['recipient']>): CommitDeliveryReportRecovery {
  const out: CommitDeliveryReportRecovery = { status: 'unavailable' };
  if (row.workflow_name || !row.recovery) return out;
  out.history_url = row.recovery.history_url;
  out.retained_replay_count = row.recovery.retained_replay_count;
  const latest = row.recovery.latest_replay;
  if (latest) {
    out.invocation_id = latest.invocation_id; out.state = latest.state;
    if (latest.state === 'completed') out.status = 'recovered';
    else if (['pending', 'dispatching'].includes(latest.state)) out.status = 'pending';
    else if (['failed', 'dead_letter', 'cancelled', 'expired', 'superseded'].includes(latest.state)) out.status = 'failed';
  }
  return out;
}
function recountRecovery(report: CommitDeliveryReport): void {
  const counts: Record<string, number> = Object.create(null);
  report.recovery_counts = counts;
  for (const item of report.items) for (const row of item.subscribers) {
    if (row.recovery) counts[row.recovery.status] = (counts[row.recovery.status] ?? 0) + 1;
  }
}
export interface CommitDeliveryReportItem {
  acceptance: CommitPublicationHistoryItem;
  evidence_status: string;
  subscribers: CommitDeliveryReportSubscriber[];
  observed_at?: string;
  next_after?: string;
}
export interface CommitDeliveryReport {
  source_id: string;
  subscription_id?: string;
  items: CommitDeliveryReportItem[];
  counts: Record<string, number>;
  recovery_counts?: Record<string, number>;
  next_before?: string;
  changes?: CommitDeliveryReportChange[];
  read_complete: boolean;
}
export interface CommitDeliveryReportChange {
  event_id: string; subscription_id?: string; kind: 'evidence' | 'subscriber' | 'recovery';
  previous_status: string; status: string; previous_observed_at?: string; observed_at?: string;
}
function compareReports(previous: CommitDeliveryReport, current: CommitDeliveryReport): CommitDeliveryReportChange[] {
  const changes: CommitDeliveryReportChange[] = [];
  current.items.forEach((item, index) => {
    const old = previous.items[index];
    if (!old || !item.observed_at) return;
    const common = { event_id: item.acceptance.event_id, previous_observed_at: old.observed_at, observed_at: item.observed_at };
    if (old.evidence_status !== item.evidence_status && !['not_read', 'read_error'].includes(item.evidence_status)) changes.push({ ...common, kind: 'evidence', previous_status: old.evidence_status, status: item.evidence_status });
    const recoveries = new Map(old.subscribers.map(row => [row.subscription_id.toLowerCase(), row.recovery?.status ?? 'unobserved']));
    const statuses = new Map(old.subscribers.map(row => [row.subscription_id.toLowerCase(), row.status]));
    for (const row of item.subscribers) {
      const before = statuses.get(row.subscription_id.toLowerCase()) ?? 'unobserved';
      const previousRecovery = recoveries.get(row.subscription_id.toLowerCase()) ?? 'unobserved';
      if (row.recovery && previousRecovery !== row.recovery.status) changes.push({ ...common, kind: 'recovery', subscription_id: row.subscription_id, previous_status: previousRecovery, status: row.recovery.status });
      if (before !== row.status) changes.push({ ...common, kind: 'subscriber', subscription_id: row.subscription_id, previous_status: before, status: row.status });
    }
  });
  return changes;
}

export class CommitDeliveryReportError extends Error {
  constructor(readonly report: CommitDeliveryReport, cause: unknown) {
    super('Commit delivery report read failed; partial observations are attached', { cause });
    this.name = 'CommitDeliveryReportError';
  }
}

/** Sample one history page and one guarded recipient page per publication. */
export async function getCommitDeliveryReport(source: string, options: {
  before?: string; limit?: number; subscriptionId?: string; timeoutMs?: number; signal?: AbortSignal; recipientPages?: number; resume?: CommitDeliveryReport; refresh?: CommitDeliveryReport;
} = {}): Promise<CommitDeliveryReport> {
  const { before, limit = 50, subscriptionId, timeoutMs = 120000, signal, recipientPages = 1, resume, refresh } = options;
  if (!Number.isInteger(recipientPages) || recipientPages < 1 || recipientPages > 20 || !Number.isInteger(limit) || limit < 1 || limit > 200 || !Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 86400000
    || (subscriptionId !== undefined && (!/^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(subscriptionId)
      || subscriptionId === '00000000-0000-0000-0000-000000000000'))) throw new TypeError('invalid delivery report options');
  if (resume && refresh) throw new TypeError('choose resume or refresh');
  const saved = resume ?? refresh;
  if (saved && (before !== undefined || options.limit !== undefined || subscriptionId !== undefined)) throw new TypeError('resume fixes publication selection and subscriber filter');
  const report: CommitDeliveryReport = saved ? structuredClone(saved) : { source_id: source, subscription_id: subscriptionId?.toLowerCase(), items: [], counts: {}, read_complete: false };
  if (saved && (report.source_id !== source || !Array.isArray(report.items) || report.items.length > 200)) throw new TypeError('invalid saved report');
  if (resume) delete report.changes;
  const baseline = refresh ? structuredClone(report) : undefined;
  if (refresh) {
    report.changes = [];
    for (const item of report.items) { item.subscribers = []; item.evidence_status = 'not_read'; delete item.next_after; delete item.observed_at; }
  }
  report.read_complete = false;
  function recount() { report.counts = Object.create(null); for (const item of report.items) for (const row of item.subscribers) report.counts[row.status] = (report.counts[row.status] ?? 0) + 1; }
  recount();
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true });
  if (signal?.aborted) abort();
  const timer = setTimeout(abort, timeoutMs);
  async function read<T>(request: CancelablePromise<T>): Promise<T> {
    const cancel = () => request.cancel();
    controller.signal.addEventListener('abort', cancel, { once: true });
    if (controller.signal.aborted) cancel();
    try { return await request; } finally { controller.signal.removeEventListener('abort', cancel); }
  }
  try {
    if (saved) {
      const binding = await read(QueuesService.getCommitSource({ source }));
      if (binding.destination !== 'event') throw new TypeError('saved report requires an event source');
      const ids = new Set<string>();
      const uuid = /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;
      if (report.subscription_id && (!uuid.test(report.subscription_id) || report.subscription_id === '00000000-0000-0000-0000-000000000000')) throw new TypeError('invalid saved subscriber filter');
      for (const item of (baseline ?? report).items) {
        const a = item.acceptance, p = a.publication;
        if (!p || a.source_id.toLowerCase() !== source.toLowerCase() || !uuid.test(a.event_id) || ids.has(a.event_id.toLowerCase())
          || p.source !== `app.${binding.app_id}` || p.id !== `commit.${source.toLowerCase()}.${a.event_id.toLowerCase()}` || !p.accepted_at
          || !['available', 'partial', 'unavailable', 'acceptance_replaced', 'not_read', 'read_error'].includes(item.evidence_status)) throw new TypeError('saved publication identity mismatch');
        ids.add(a.event_id.toLowerCase());
        const subs = new Set<string>();
        for (const row of item.subscribers) {
          const id = row.subscription_id.toLowerCase();
          if (!uuid.test(id) || subs.has(id) || (report.subscription_id && id !== report.subscription_id.toLowerCase())) throw new TypeError('invalid saved subscriber identity');
          subs.add(id);
        }
      }
    } else {
      const history = await read(QueuesService.listCommitPublications({ source, before, limit }));
      report.next_before = history.next_before;
      report.items = history.items.map(acceptance => ({ acceptance, evidence_status: 'not_read', subscribers: [] }));
    }
    for (const item of report.items) {
      if (resume && ['available', 'unavailable', 'acceptance_replaced'].includes(item.evidence_status)) continue;
      const seen = new Set<string>();
      for (let pageNumber = 0; pageNumber < recipientPages; pageNumber++) {
        const publication = item.acceptance.publication;
        if (!publication) { item.evidence_status = 'read_error'; throw new Error('publication history lacks publication identity'); }
        try {
          const after = item.next_after;
          if (seen.has(after ?? '')) throw new Error('repeated publication recipient cursor');
          seen.add(after ?? '');
          const page = await read(EventsService.getEventReceipt({
            source: publication.source, id: publication.id,
            expectedAcceptedAt: publication.accepted_at, after, limit: 200
          }));
          if (page.event_source !== publication.source || page.event_id !== publication.id || (page.next_after && (page.next_after === after || seen.has(page.next_after)))) throw new Error('publication evidence identity mismatch');
          const additions: CommitDeliveryReportSubscriber[] = [];
          item.evidence_status = page.next_after || !page.snapshot_captured ? 'partial' : 'available';
          for (const row of page.recipients) {
            if ((row.origin && row.origin !== 'acceptance') || (report.subscription_id && row.subscription_id.toLowerCase() !== report.subscription_id)) continue;
            const status = commitSubscriberStatus(row);
            additions.push({ publication, subscription_id: row.subscription_id.toLowerCase(), status, recipient: row, recovery: reportRecovery(row) });
          }
          for (const row of additions) {
            const index = item.subscribers.findIndex(saved => saved.subscription_id.toLowerCase() === row.subscription_id);
            const previous = index >= 0 ? item.subscribers[index] : undefined;
            if (!previous) item.subscribers.push(row);
            else {
              const old = previous.status;
              report.counts[old] = (report.counts[old] ?? 0) - 1;
              if (report.counts[old] === 0) delete report.counts[old];
              item.subscribers[index] = row;
            }
            report.counts[row.status] = (report.counts[row.status] ?? 0) + 1;
          }
          item.observed_at = new Date().toISOString();
          item.next_after = page.next_after;
          if (report.subscription_id && !item.subscribers.length && item.evidence_status === 'available') {
            item.subscribers.push({ publication, subscription_id: report.subscription_id, status: 'not_targeted' });
            report.counts.not_targeted = (report.counts.not_targeted ?? 0) + 1;
          }
          if (!item.next_after) break;
        } catch (error) {
          const status = (error as { status?: number }).status;
          if (!controller.signal.aborted && (status === 404 || status === 409)) {
            item.observed_at = new Date().toISOString();
            item.evidence_status = status === 404 ? 'unavailable' : 'acceptance_replaced';
            break;
          } else { item.evidence_status = 'read_error'; throw error; }
        }
      }
    }
    if (baseline) report.changes = compareReports(baseline, report);
    report.read_complete = true;
    return report;
  } catch (error) { if (baseline) report.changes = compareReports(baseline, report); throw new CommitDeliveryReportError(report, error); }
  finally { recountRecovery(report); clearTimeout(timer); signal?.removeEventListener('abort', abort); }
}
