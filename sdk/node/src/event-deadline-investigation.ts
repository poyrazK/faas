import { Buffer } from 'node:buffer';
import { validateEventDeadlineCheckpoint, type EventDeadlineAcknowledgement,
  type EventDeadlineAlert } from './event-deadline-checkpoint.js';
import { count, duration, EVENT_DEADLINE_REASONS, record, timestamp, uuid,
  type EventDeadlineReason, type EventDeadlineTarget } from './event-deadline-policy.js';
import type { EventSubscriptionDeadlinePage } from './generated/models/EventSubscriptionDeadlinePage.js';
import type { EventSubscriptionDeadlineRecipient } from './generated/models/EventSubscriptionDeadlineRecipient.js';
import { EventsService } from './generated/services/EventsService.js';

export interface EventDeadlineInvestigationOptions {
  /** One page, default 50, integer 1..100. Keep unchanged on continuation. */
  limit?: number;
  /** Opaque continuation, at most 4096 UTF-8 bytes. */
  cursor?: string;
  /** Request budget in whole milliseconds, 100..5000; default 5000. */
  timeoutMs?: number;
  signal?: AbortSignal;
}
export interface EventDeadlineInvestigationQuery {
  status: 'upcoming' | 'expired';
  withinMillis?: number;
  waitingReason?: EventDeadlineReason;
  cursor?: string;
  limit: number;
}
export interface EventDeadlineInvestigation {
  target: EventDeadlineTarget;
  /** Saved notification and historical sample, not a current health assessment. */
  alert: EventDeadlineAlert;
  /** Notification state in the supplied checkpoint, not delivery state. */
  notificationState: 'pending' | 'acknowledged';
  membership: 'current_at_triage_observation';
  readOnly: true;
  query: EventDeadlineInvestigationQuery;
  page: EventSubscriptionDeadlinePage;
}

const CURSOR_MAX_BYTES = 4096;
const MIN_TIME = timestamp('0001-01-01T00:00:00Z')!;
const MAX_TIME = timestamp('9999-12-31T23:59:59.999999999Z')!;
function triageTime(raw: unknown): bigint | undefined {
  const at = timestamp(raw);
  return at !== undefined && at > MIN_TIME && at <= MAX_TIME ? at : undefined;
}
function validCursor(raw: unknown): raw is string {
  return typeof raw === 'string' && Buffer.byteLength(raw, 'utf8') <= CURSOR_MAX_BYTES;
}
function invalidPage(): never { throw new TypeError('invalid or mismatched deadline investigation page'); }

/** Investigate one retained Node checkpoint alert using one read-only current triage page. */
export async function investigateEventDeadlineAlert(checkpointInput: unknown, reference: EventDeadlineAcknowledgement,
  options: EventDeadlineInvestigationOptions = {}): Promise<EventDeadlineInvestigation> {
  const checkpoint = validateEventDeadlineCheckpoint(checkpointInput);
  if (!record(reference)) throw new TypeError('invalid deadline alert reference');
  const { incidentId, sequence } = reference;
  if (typeof incidentId !== 'string' || !count(sequence) || sequence === 0) throw new TypeError('invalid deadline alert reference');
  const matches = (a: EventDeadlineAlert) => a.incidentId === incidentId && a.sequence === sequence;
  const pending = checkpoint.pending.find(matches), alert = pending ?? checkpoint.acknowledged.find(matches);
  if (!alert) throw new TypeError('deadline alert reference is not retained in the checkpoint');
  const rule = checkpoint.policy.rules.find(r => r.metric === alert.metric);
  if (!rule) throw new TypeError('deadline alert has no matching checkpoint rule');
  if (!record(options)) throw new TypeError('invalid deadline investigation options');
  const limit = options.limit === undefined ? 50 : options.limit;
  const timeoutMs = options.timeoutMs === undefined ? 5000 : options.timeoutMs;
  const cursor = options.cursor, signal = options.signal;
  if (!count(limit) || limit < 1 || limit > 100 || !duration(timeoutMs, 5000)
    || (cursor !== undefined && !validCursor(cursor)) || (signal !== undefined && !(signal instanceof AbortSignal))) {
    throw new TypeError('invalid deadline investigation page size, cursor, timeout or signal');
  }
  const query: EventDeadlineInvestigationQuery = { status: rule.cohort, limit,
    ...(rule.cohort === 'upcoming' ? { withinMillis: checkpoint.policy.withinMs } : {}),
    ...(rule.reason === undefined ? {} : { waitingReason: rule.reason }),
    ...(cursor ? { cursor } : {}) };
  const target = checkpoint.target;
  const raw = await readPage(target, query, timeoutMs, signal);
  const page = validatePage(raw, target, query);
  return { target, alert, notificationState: pending ? 'pending' : 'acknowledged',
    membership: 'current_at_triage_observation', readOnly: true, query, page };
}

async function readPage(target: EventDeadlineTarget, query: EventDeadlineInvestigationQuery,
  timeoutMs: number, signal?: AbortSignal): Promise<unknown> {
  signal?.throwIfAborted();
  const request = EventsService.listEventSubscriptionDeadlines({ slug: target.app, subscriptionId: target.subscriptionId, ...query });
  const cancel = () => request.cancel();
  let timedOut = false;
  const timer = setTimeout(() => { timedOut = true; cancel(); }, timeoutMs);
  signal?.addEventListener('abort', cancel, { once: true });
  try {
    // Account for an abort during request construction/listener installation too.
    if (signal?.aborted) cancel();
    const page = await request;
    signal?.throwIfAborted();
    if (timedOut) throw new DOMException('deadline investigation read timed out', 'TimeoutError');
    return page;
  } catch (error) {
    signal?.throwIfAborted();
    if (timedOut) throw new DOMException('deadline investigation read timed out', 'TimeoutError');
    throw error;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener('abort', cancel);
  }
}

function validatePage(raw: unknown, target: EventDeadlineTarget, query: EventDeadlineInvestigationQuery): EventSubscriptionDeadlinePage {
  if (!record(raw)) return invalidPage();
  const observed = triageTime(raw.observed_at), window = triageTime(raw.window_at);
  if (raw.coverage !== 'retained_captured_pending_application_deadlines' || raw.read_only !== true
    || !uuid(raw.app_id) || raw.app_id.toLowerCase() !== target.appId
    || !uuid(raw.subscription_id) || raw.subscription_id.toLowerCase() !== target.subscriptionId
    || observed === undefined || window === undefined || window > observed || (!query.cursor && window !== observed)
    || !Array.isArray(raw.recipients) || raw.recipients.length > query.limit || typeof raw.has_more !== 'boolean'
    || (raw.next_cursor !== undefined && !validCursor(raw.next_cursor))
    || raw.has_more !== (typeof raw.next_cursor === 'string' && raw.next_cursor.length > 0)
    || (raw.has_more && raw.recipients.length !== query.limit)
    || (query.cursor !== undefined && raw.next_cursor === query.cursor)) return invalidPage();
  let previous: { deadline: bigint; accepted: bigint } | undefined;
  const recipients = raw.recipients.map(row => {
    const validated = validateRecipient(row, window, observed, query);
    if (previous && (validated.deadline < previous.deadline
      || (validated.deadline === previous.deadline && validated.accepted < previous.accepted))) return invalidPage();
    previous = validated;
    return validated.recipient;
  });
  // Return only known metadata: unrecognized server fields (including payloads) are discarded.
  return { coverage: raw.coverage, app_id: target.appId, subscription_id: target.subscriptionId,
    observed_at: raw.observed_at as string, window_at: raw.window_at as string, read_only: true,
    recipients, has_more: raw.has_more, ...(raw.next_cursor ? { next_cursor: raw.next_cursor as string } : {}) };
}

function validateRecipient(raw: unknown, window: bigint, observed: bigint, query: EventDeadlineInvestigationQuery):
  { recipient: EventSubscriptionDeadlineRecipient; deadline: bigint; accepted: bigint } {
  if (!record(raw)) return invalidPage();
  const accepted = triageTime(raw.accepted_at), deadline = triageTime(raw.deadline_at);
  if (typeof raw.event_source !== 'string' || !raw.event_source || typeof raw.event_id !== 'string' || !raw.event_id
    || !EVENT_DEADLINE_REASONS.includes(raw.waiting_reason as EventDeadlineReason)
    || accepted === undefined || deadline === undefined || accepted > window || deadline <= accepted
    || raw.expired !== (deadline <= observed) || (query.waitingReason !== undefined && raw.waiting_reason !== query.waitingReason)
    || (query.status === 'expired' && deadline > window)
    || (query.status === 'upcoming' && (deadline <= window || deadline >= window + BigInt(query.withinMillis!) * 1_000_000n))
    || (raw.next_attempt_at !== undefined && triageTime(raw.next_attempt_at) === undefined)
    || (raw.lease_until !== undefined && triageTime(raw.lease_until) === undefined)) return invalidPage();
  return { deadline, accepted, recipient: {
    event_source: raw.event_source, event_id: raw.event_id, accepted_at: raw.accepted_at as string,
    deadline_at: raw.deadline_at as string, expired: raw.expired as boolean, waiting_reason: raw.waiting_reason as EventDeadlineReason,
    ...(raw.next_attempt_at === undefined ? {} : { next_attempt_at: raw.next_attempt_at as string }),
    ...(raw.lease_until === undefined ? {} : { lease_until: raw.lease_until as string }),
  } };
}
