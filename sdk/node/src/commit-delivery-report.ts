import { QueuesService } from './generated/services/QueuesService.js';
import { EventsService } from './generated/services/EventsService.js';
import type { CancelablePromise } from './generated/core/CancelablePromise.js';
import type { CommitPublicationHistoryItem } from './generated/models/CommitPublicationHistoryItem.js';
import { commitSubscriberStatus, type CommitSubscriberWaitResult } from './commit-subscriber-wait.js';

export interface CommitDeliveryReportItem {
  acceptance: CommitPublicationHistoryItem;
  evidence_status: string;
  subscribers: CommitSubscriberWaitResult[];
  next_after?: string;
}
export interface CommitDeliveryReport {
  source_id: string;
  subscription_id?: string;
  items: CommitDeliveryReportItem[];
  counts: Record<string, number>;
  next_before?: string;
  read_complete: boolean;
}
export class CommitDeliveryReportError extends Error {
  constructor(readonly report: CommitDeliveryReport, cause: unknown) {
    super('Commit delivery report read failed; partial observations are attached', { cause });
    this.name = 'CommitDeliveryReportError';
  }
}

/** Sample one history page and one guarded recipient page per publication. */
export async function getCommitDeliveryReport(source: string, options: {
  before?: string; limit?: number; subscriptionId?: string; timeoutMs?: number; signal?: AbortSignal; recipientPages?: number; resume?: CommitDeliveryReport;
} = {}): Promise<CommitDeliveryReport> {
  const { before, limit = 50, subscriptionId, timeoutMs = 120000, signal, recipientPages = 1, resume } = options;
  if (!Number.isInteger(recipientPages) || recipientPages < 1 || recipientPages > 20 || !Number.isInteger(limit) || limit < 1 || limit > 200 || !Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 86400000
    || (subscriptionId !== undefined && (!/^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(subscriptionId)
      || subscriptionId === '00000000-0000-0000-0000-000000000000'))) throw new TypeError('invalid delivery report options');
  if (resume && (before !== undefined || options.limit !== undefined || subscriptionId !== undefined)) throw new TypeError('resume fixes publication selection and subscriber filter');
  const report: CommitDeliveryReport = resume ? structuredClone(resume) : { source_id: source, subscription_id: subscriptionId?.toLowerCase(), items: [], counts: {}, read_complete: false };
  if (resume && (report.source_id !== source || !Array.isArray(report.items) || report.items.length > 200)) throw new TypeError('invalid saved report');
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
    if (resume) {
      const binding = await read(QueuesService.getCommitSource({ source }));
      if (binding.destination !== 'event') throw new TypeError('saved report requires an event source');
      const ids = new Set<string>();
      const uuid = /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;
      if (report.subscription_id && (!uuid.test(report.subscription_id) || report.subscription_id === '00000000-0000-0000-0000-000000000000')) throw new TypeError('invalid saved subscriber filter');
      for (const item of report.items) {
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
          const additions: CommitSubscriberWaitResult[] = [];
          item.evidence_status = page.next_after || !page.snapshot_captured ? 'partial' : 'available';
          for (const row of page.recipients) {
            if ((row.origin && row.origin !== 'acceptance') || (report.subscription_id && row.subscription_id.toLowerCase() !== report.subscription_id)) continue;
            const status = commitSubscriberStatus(row);
            additions.push({ publication, subscription_id: row.subscription_id.toLowerCase(), status, recipient: row });
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
          item.next_after = page.next_after;
          if (report.subscription_id && !item.subscribers.length && item.evidence_status === 'available') {
            item.subscribers.push({ publication, subscription_id: report.subscription_id, status: 'not_targeted' });
            report.counts.not_targeted = (report.counts.not_targeted ?? 0) + 1;
          }
          if (!item.next_after) break;
        } catch (error) {
          const status = (error as { status?: number }).status;
          if (!controller.signal.aborted && (status === 404 || status === 409)) {
            item.evidence_status = status === 404 ? 'unavailable' : 'acceptance_replaced';
            break;
          } else { item.evidence_status = 'read_error'; throw error; }
        }
      }
    }
    report.read_complete = true;
    return report;
  } catch (error) { throw new CommitDeliveryReportError(report, error); }
  finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); }
}
