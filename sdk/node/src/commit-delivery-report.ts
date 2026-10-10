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
  before?: string; limit?: number; subscriptionId?: string; timeoutMs?: number; signal?: AbortSignal;
} = {}): Promise<CommitDeliveryReport> {
  const { before, limit = 50, subscriptionId, timeoutMs = 120000, signal } = options;
  if (!Number.isInteger(limit) || limit < 1 || limit > 200 || !Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 86400000
    || (subscriptionId !== undefined && (!/^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(subscriptionId)
      || subscriptionId === '00000000-0000-0000-0000-000000000000'))) throw new TypeError('invalid delivery report options');
  const report: CommitDeliveryReport = { source_id: source, subscription_id: subscriptionId?.toLowerCase(), items: [], counts: {}, read_complete: false };
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
    const history = await read(QueuesService.listCommitPublications({ source, before, limit }));
    report.next_before = history.next_before;
    report.items = history.items.map(acceptance => ({ acceptance, evidence_status: 'not_read', subscribers: [] }));
    for (const item of report.items) {
      const publication = item.acceptance.publication;
      if (!publication) { item.evidence_status = 'read_error'; throw new Error('publication history lacks publication identity'); }
      try {
        const page = await read(EventsService.getEventReceipt({ source: publication.source, id: publication.id,
          expectedAcceptedAt: publication.accepted_at, limit: 200 }));
        if (page.event_source !== publication.source || page.event_id !== publication.id) throw new Error('publication evidence identity mismatch');
        item.next_after = page.next_after;
        item.evidence_status = page.next_after || !page.snapshot_captured ? 'partial' : 'available';
        for (const row of page.recipients) {
          if ((row.origin && row.origin !== 'acceptance') || (report.subscription_id && row.subscription_id.toLowerCase() !== report.subscription_id)) continue;
          const status = commitSubscriberStatus(row);
          item.subscribers.push({ publication, subscription_id: row.subscription_id, status, recipient: row });
          report.counts[status] = (report.counts[status] ?? 0) + 1;
        }
        if (report.subscription_id && !item.subscribers.length && item.evidence_status === 'available') {
          item.subscribers.push({ publication, subscription_id: report.subscription_id, status: 'not_targeted' });
          report.counts.not_targeted = (report.counts.not_targeted ?? 0) + 1;
        }
      } catch (error) {
        const status = (error as { status?: number }).status;
        if (!controller.signal.aborted && (status === 404 || status === 409)) {
          item.evidence_status = status === 404 ? 'unavailable' : 'acceptance_replaced';
        } else { item.evidence_status = 'read_error'; throw error; }
      }
    }
    report.read_complete = true;
    return report;
  } catch (error) { throw new CommitDeliveryReportError(report, error); }
  finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); }
}
