import { EventsService } from './generated/services/EventsService.js';
import type { CommitEventPublication } from './generated/models/CommitEventPublication.js';
import type { EventReceiptRecipientResponse } from './generated/models/EventReceiptRecipientResponse.js';

export interface CommitSubscriberWaitResult {
  publication: CommitEventPublication;
  subscription_id: string;
  status: string;
  recipient?: EventReceiptRecipientResponse;
}

/** Wait for the original application subscriber only; never replay or cancel work. */
export async function waitCommitSubscriber(publication: CommitEventPublication, subscriptionId: string,
  options: { timeoutMs?: number; intervalMs?: number; signal?: AbortSignal } = {}): Promise<CommitSubscriberWaitResult> {
  const timeout = options.timeoutMs ?? 120_000;
  const interval = options.intervalMs ?? 1000;
  if (!Number.isFinite(timeout) || timeout <= 0 || timeout > 86_400_000 || !Number.isFinite(interval)
    || interval < 100 || interval > 60_000 || !/^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(subscriptionId)
    || subscriptionId === '00000000-0000-0000-0000-000000000000' || !publication.source || !publication.id || !publication.accepted_at) {
    throw new TypeError('invalid subscriber wait options');
  }
  const result: CommitSubscriberWaitResult = { publication: { ...publication }, subscription_id: subscriptionId.toLowerCase(), status: 'pending' };
  publication = result.publication;
  const controller = new AbortController();
  const abort = () => controller.abort();
  options.signal?.addEventListener('abort', abort, { once: true });
  if (options.signal?.aborted) abort();
  const deadline = setTimeout(abort, timeout);
  const stopped = () => { result.status = options.signal?.aborted ? 'interrupted' : 'timed_out'; return result; };
  try {
    for (;;) {
      let after: string | undefined;
      const seen = new Set<string>();
      let found = false;
      for (;;) {
        if (controller.signal.aborted) return stopped();
        const request = EventsService.getEventReceipt({ source: publication.source, id: publication.id,
          expectedAcceptedAt: publication.accepted_at, after, limit: 200 });
        const cancel = () => request.cancel();
        controller.signal.addEventListener('abort', cancel, { once: true });
        try {
          const page = await request;
          if (controller.signal.aborted) return stopped();
          if (page.event_source !== publication.source || page.event_id !== publication.id) throw new Error('publication evidence identity mismatch');
          const row = page.recipients.find(r => r.subscription_id.toLowerCase() === result.subscription_id && (!r.origin || r.origin === 'acceptance'));
          if (row) {
            result.recipient = row;
            result.status = commitSubscriberStatus(row);
            found = true;
            break;
          }
          if (!page.next_after) {
            if (page.snapshot_captured && page.routing_settled_at) { result.status = 'not_targeted'; return result; }
            break;
          }
          if (seen.has(page.next_after)) throw new Error('repeated publication recipient cursor');
          seen.add(page.next_after);
          after = page.next_after;
        } catch (error) {
          if (controller.signal.aborted) return stopped();
          const status = (error as { status?: number }).status;
          if (status === 404 || status === 409) { result.status = status === 404 ? 'unavailable' : 'acceptance_replaced'; return result; }
          throw error;
        } finally { controller.signal.removeEventListener('abort', cancel); }
      }
      if (found && result.status !== 'pending') return result;
      await new Promise<void>(resolve => {
        const finish = () => { clearTimeout(timer); controller.signal.removeEventListener('abort', finish); resolve(); };
        const timer = setTimeout(finish, interval);
        controller.signal.addEventListener('abort', finish, { once: true });
        if (controller.signal.aborted) finish();
      });
    }
  } finally { clearTimeout(deadline); options.signal?.removeEventListener('abort', abort); }
}

export function commitSubscriberStatus(row: EventReceiptRecipientResponse): string {
  if (row.workflow_name) return 'unsupported_recipient';
  if (row.execution) {
    if (['completed', 'failed', 'dead_letter', 'cancelled', 'expired', 'superseded'].includes(row.execution.state)) return row.execution.state;
    return ['pending', 'dispatching'].includes(row.execution.state) ? 'pending' : 'unavailable';
  }
  if (row.cancellation) return 'cancelled';
  if (row.execution_unavailable) return 'unavailable';
  if (row.routing.state === 'filtered') return 'filtered';
  if (row.routing.state === 'failed') return 'routing_failed';
  return ['pending', 'processing'].includes(row.routing.state) ? 'pending' : 'unavailable';
}
