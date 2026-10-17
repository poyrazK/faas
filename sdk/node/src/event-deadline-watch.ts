import { randomUUID } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { EventsService } from './generated/services/EventsService.js';
import type { EventSubscriptionDeadlineWindow } from './generated/models/EventSubscriptionDeadlineWindow.js';
import { deadlineBoundaries, deadlineRatioValue } from './event-deadline-ratio.js';
import { count, deadlineEvidence, deadlinePolicy, deadlineTarget, duration, timestamp,
  type EventDeadlinePolicyOptions, type EventDeadlineTarget } from './event-deadline-policy.js';
import { acknowledgeEventDeadlineAlerts, clone, DEADLINE_QUEUE_MAX, validateEventDeadlineCheckpoint,
  type EventDeadlineAcknowledgement, type EventDeadlineAlert, type EventDeadlineCheckpoint,
  type EventDeadlineCondition } from './event-deadline-checkpoint.js';

export interface EventDeadlineConfirmation {
  metric: string;
  phase: 'unknown' | 'clear' | 'firing' | 'resume_pending' | 'warning_pending' | 'recovery_pending' | 'notification_blocked';
  reason: string;
  freshSamples: number;
  requiredSamples: 2;
  elapsedMs: number;
  requiredMs: number;
  remainingMs: number;
}
export interface EventDeadlineWatchResult {
  stopReason: 'watching' | 'timed_out' | 'interrupted' | 'callback_failure';
  observations: number;
  evidenceStatus: 'unknown' | 'eligible';
  evidenceReason: string;
  evidence?: EventSubscriptionDeadlineWindow;
  checkpoint: EventDeadlineCheckpoint;
  /** Only transitions produced by the latest observation; replay checkpoint.pending after a crash. */
  alerts: EventDeadlineAlert[];
  confirmations: EventDeadlineConfirmation[];
}
export interface EventDeadlineWatchOptions extends EventDeadlinePolicyOptions {
  intervalMs?: number;
  timeoutMs?: number;
  maxAgeMs?: number;
  requestTimeoutMs?: number;
  signal?: AbortSignal;
  resume?: EventDeadlineCheckpoint;
  /** Persist checkpoint before delivering alerts. Snapshots are detached; errors stop the watch. */
  onProgress?: (progress: EventDeadlineWatchResult, signal: AbortSignal) => void | Promise<void>;
  /** Runs between observations; persist acknowledgment results in the next progress snapshot. */
  pollAcknowledgements?: (checkpoint: EventDeadlineCheckpoint, signal: AbortSignal) => readonly EventDeadlineAcknowledgement[] | Promise<readonly EventDeadlineAcknowledgement[]>;
}
export class EventDeadlineWatchCallbackError extends Error {
  constructor(readonly result: EventDeadlineWatchResult, cause: unknown) {
    super('deadline watch callback failed; persist result.checkpoint before notification retry', { cause });
    this.name = 'EventDeadlineWatchCallbackError';
  }
}
interface Proof { first?: number; samples: number; lastObserved?: bigint; lastLocal?: number; mode?: string; resume: boolean }
function confirmation(c: EventDeadlineCondition, p: Proof, now: number, policy: EventDeadlineCheckpoint['policy'],
  reason: string, blocked: boolean): EventDeadlineConfirmation {
  const recovering = c.incidentId !== undefined && deadlineBoundaries(c).recovered;
  const requiredMs = recovering ? policy.recoverySustainMs : policy.sustainMs;
  const elapsedMs = p.first === undefined ? 0 : Math.max(0, now - p.first);
  let phase: EventDeadlineConfirmation['phase'] = p.resume ? 'resume_pending' : c.state;
  if (p.first !== undefined) phase = blocked ? 'notification_blocked' : recovering ? 'recovery_pending' : 'warning_pending';
  return { metric: c.metric, phase, reason, freshSamples: p.samples, requiredSamples: 2,
    elapsedMs, requiredMs, remainingMs: p.first === undefined ? 0 : Math.max(0, requiredMs - elapsedMs) };
}
function alert(checkpoint: EventDeadlineCheckpoint, c: EventDeadlineCondition, transition: 'opened' | 'resolved', at: string): EventDeadlineAlert {
  checkpoint.sequence++;
  if (transition === 'opened') c.incidentId = `${checkpoint.session}-${checkpoint.sequence}`;
  const out: EventDeadlineAlert = { sequence: checkpoint.sequence, incidentId: c.incidentId!, metric: c.metric,
    transition, at, value: c.value!, threshold: c.threshold, recoveryThreshold: c.recoveryThreshold,
    ...(c.ratioSample === undefined ? {} : { ratioSample: { ...c.ratioSample } }) };
  checkpoint.pending.push(out);
  return out;
}
function reset(p: Proof): void { p.first = undefined; p.samples = 0; }
function apply(checkpoint: EventDeadlineCheckpoint, c: EventDeadlineCondition, p: Proof, now: number, observed: string):
  { reason: string; blocked: boolean; alert?: EventDeadlineAlert } {
  const active = c.incidentId !== undefined;
  const { breached, recovered } = deadlineBoundaries(c);
  const candidate = active ? (p.resume ? breached || recovered : recovered) : breached;
  if (!candidate) {
    reset(p);
    c.state = active ? p.resume ? 'unknown' : 'firing' : 'clear';
    return { reason: active ? p.resume ? 'fresh_confirmation_required' : 'recovery_boundary_not_confirmed' : 'threshold_not_breached', blocked: false };
  }
  // Crossing between warning and recovery bands while resuming starts a new proof.
  const mode = active && recovered ? 'recovery' : 'warning';
  const previousMode = p.mode;
  if (previousMode !== mode) reset(p);
  p.mode = mode;
  p.first ??= now; p.samples++;
  const required = mode === 'recovery' ? checkpoint.policy.recoverySustainMs : checkpoint.policy.sustainMs;
  const confirmed = p.samples >= 2 && now - p.first >= required;
  c.state = active && !p.resume ? 'firing' : 'unknown';
  if (!confirmed) return { reason: `${mode}_confirmation_pending`, blocked: false };
  if (!active) {
    const reserved = checkpoint.conditions.filter(v => v.incidentId !== undefined).length;
    if (checkpoint.pending.length + reserved + 2 > DEADLINE_QUEUE_MAX
      || checkpoint.sequence > Number.MAX_SAFE_INTEGER - reserved - 2) return { reason: 'notification_outbox_full', blocked: true };
    const opened = alert(checkpoint, c, 'opened', observed);
    c.state = 'firing'; p.resume = false; reset(p);
    return { reason: 'recovery_boundary_not_confirmed', blocked: false, alert: opened };
  }
  if (recovered) {
    const resolved = alert(checkpoint, c, 'resolved', observed);
    delete c.incidentId; c.state = 'clear'; p.resume = false; reset(p);
    return { reason: 'threshold_not_breached', blocked: false, alert: resolved };
  }
  c.state = 'firing'; p.resume = false; reset(p);
  return { reason: 'recovery_boundary_not_confirmed', blocked: false };
}

/** Read-only, deadline-only Node watch. This sends no external notifications or delivery actions. */
export async function watchEventSubscriptionDeadlines(targetInput: EventDeadlineTarget, options: EventDeadlineWatchOptions): Promise<EventDeadlineWatchResult> {
  const target = deadlineTarget(targetInput), policy = deadlinePolicy(options);
  const interval = options.intervalMs === undefined ? 5000 : options.intervalMs, timeout = options.timeoutMs === undefined ? 600_000 : options.timeoutMs;
  const maxAge = options.maxAgeMs === undefined ? 30_000 : options.maxAgeMs, requestTimeout = options.requestTimeoutMs === undefined ? 10_000 : options.requestTimeoutMs;
  if (!duration(interval, 60_000) || !duration(timeout) || !duration(maxAge) || !duration(requestTimeout)
    || (options.onProgress !== undefined && typeof options.onProgress !== 'function')
    || (options.pollAcknowledgements !== undefined && typeof options.pollAcknowledgements !== 'function')) throw new TypeError('invalid deadline watch timing/callback options');
  // Capture callbacks/signal before yielding; option maps were normalized into independent rules.
  const onProgress = options.onProgress, pollAcknowledgements = options.pollAcknowledgements, signal = options.signal;
  let checkpoint: EventDeadlineCheckpoint = options.resume === undefined ? {
    kind: 'gregale.node.event-deadline-watch', version: policy.rules.some(r => r.minimumRecipients !== undefined) ? 2 : 1, target, policy, session: randomUUID(), sequence: 0,
    conditions: policy.rules.map(rule => ({ ...rule, state: 'unknown' })), pending: [], acknowledged: [],
  } : validateEventDeadlineCheckpoint(options.resume);
  if (JSON.stringify(checkpoint.target) !== JSON.stringify(target) || JSON.stringify(checkpoint.policy) !== JSON.stringify(policy)) {
    throw new TypeError('deadline checkpoint target or effective policy mismatch');
  }
  const proofs: Proof[] = checkpoint.conditions.map(c => ({ samples: 0, lastObserved: timestamp(c.observedAt), resume: !!c.incidentId }));
  for (const c of checkpoint.conditions) c.state = 'unknown';
  const result: EventDeadlineWatchResult = { stopReason: 'watching', observations: 0, evidenceStatus: 'unknown', evidenceReason: 'awaiting_observation',
    checkpoint, alerts: [], confirmations: checkpoint.conditions.map((c, i) => confirmation(c, proofs[i]!, 0, policy, 'fresh_confirmation_required', false)) };
  const controller = new AbortController(), abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true });
  if (signal?.aborted) abort();
  const timer = setTimeout(abort, timeout);
  try {
    while (!controller.signal.aborted) {
      if (pollAcknowledgements) {
        try { checkpoint = acknowledgeEventDeadlineAlerts(checkpoint, await pollAcknowledgements(clone(checkpoint), controller.signal)); }
        catch (cause) { result.checkpoint = checkpoint; result.stopReason = 'callback_failure'; throw new EventDeadlineWatchCallbackError(clone(result), cause); }
      }
      if (controller.signal.aborted) break;
      let evidence: EventSubscriptionDeadlineWindow | undefined, reason = '';
      const request = EventsService.getEventSubscriptionDeadlineWindow({ slug: target.app, subscriptionId: target.subscriptionId, withinMillis: policy.withinMs });
      const cancel = () => request.cancel();
      controller.signal.addEventListener('abort', cancel, { once: true });
      const requestTimer = setTimeout(cancel, requestTimeout);
      try { evidence = await request; }
      catch { reason = 'deadline_read_failed'; }
      finally { clearTimeout(requestTimer); controller.signal.removeEventListener('abort', cancel); }
      if (controller.signal.aborted) break;
      const local = performance.now(), wall = BigInt(Date.now()) * 1_000_000n + 999_999n;
      const observed = evidence && timestamp(evidence.summary?.observed_at);
      if (!reason && !deadlineEvidence(evidence, target, policy.withinMs)) reason = 'invalid_or_incomplete_deadline_evidence';
      if (!reason && observed !== undefined) {
        if (observed > wall) reason = 'future_observation';
        else if (wall - observed > BigInt(maxAge) * 1_000_000n) reason = 'stale_observation';
      }
      result.observations++; result.alerts = []; result.confirmations = [];
      result.evidenceStatus = reason ? 'unknown' : 'eligible'; result.evidenceReason = reason || 'complete_deadline_window';
      // Do not retain unvalidated/unbounded server bodies in progress or checkpoint snapshots.
      if (reason) delete result.evidence; else result.evidence = clone(evidence!);
      for (const [i, c] of checkpoint.conditions.entries()) {
        const p = proofs[i]!;
        let loss = reason;
        if (!loss && observed !== undefined && p.lastObserved !== undefined && observed <= p.lastObserved) {
          loss = observed === p.lastObserved ? 'repeated_observation' : 'out_of_order_observation';
        }
        if (!loss && p.lastLocal !== undefined && local - p.lastLocal > maxAge) loss = 'sample_gap';
        if (!loss && c.minimumRecipients !== undefined && evidence!.summary.deadline_recipients < c.minimumRecipients) {
          loss = 'insufficient_deadline_recipients';
        }
        // A gap resets proof but the current valid advancing observation becomes the new high-water mark.
        if ((!loss || loss === 'sample_gap') && observed !== undefined) { p.lastObserved = observed; p.lastLocal = local; }
        if (loss) {
          reset(p); c.state = 'unknown'; p.resume = !!c.incidentId;
          result.confirmations.push(confirmation(c, p, local, policy, loss, false));
          continue;
        }
        const v = evidence!;
        if (c.minimumRecipients !== undefined) {
          c.ratioSample = { expiredRecipients: v.summary.already_expired_count, deadlineRecipients: v.summary.deadline_recipients };
          c.value = deadlineRatioValue(c.ratioSample);
        } else {
          const counts = c.cohort === 'upcoming' ? v.upcoming_waiting_reasons : v.expired_waiting_reasons;
          const value = c.reason ? counts[c.reason] : c.cohort === 'upcoming' ? v.upcoming_recipients : v.summary.already_expired_count;
          if (!count(value)) throw new Error('validated deadline count unavailable');
          c.value = value;
        }
        c.observedAt = v.summary.observed_at;
        const update = apply(checkpoint, c, p, local, c.observedAt);
        if (update.alert) result.alerts.push(update.alert);
        result.confirmations.push(confirmation(c, p, local, policy, update.reason, update.blocked));
      }
      result.checkpoint = checkpoint;
      if (onProgress) {
        try { await onProgress(clone(result), controller.signal); }
        catch (cause) { result.stopReason = 'callback_failure'; throw new EventDeadlineWatchCallbackError(clone(result), cause); }
      }
      await pause(interval, controller.signal);
    }
    result.stopReason = signal?.aborted ? 'interrupted' : 'timed_out';
    result.checkpoint = checkpoint;
    return clone(result);
  } finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); }
}
function pause(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise(resolve => {
    const finish = () => { clearTimeout(timer); signal.removeEventListener('abort', finish); resolve(); };
    const timer = setTimeout(finish, ms);
    signal.addEventListener('abort', finish, { once: true });
    if (signal.aborted) finish();
  });
}
