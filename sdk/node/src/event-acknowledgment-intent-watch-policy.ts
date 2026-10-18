// Internal normalized policy and confirmation state, following the Go recovery watch.
import { inspect } from 'node:util';
import { intentHealthOptions, type EventAcknowledgmentIntentHealth, type EventAcknowledgmentIntentHealthOptions } from './event-acknowledgment-intent-health.js';
import type { EventAcknowledgmentWorkerOperationOptions } from './event-acknowledgment-protocol.js';

export interface EventAcknowledgmentIntentWatchOptions extends EventAcknowledgmentWorkerOperationOptions {
  health?: Pick<EventAcknowledgmentIntentHealthOptions, 'scanLimit' | 'stallAfterMs'>;
  /** Integer counts 0..scanLimit; zero disables. Enable at least one threshold. */
  dueThreshold?: number;
  stalledThreshold?: number;
  /** Integer count 0..scanLimit; zero disables. Enables version 2 in checkpoint mode. */
  parkedThreshold?: number;
  /** Whole milliseconds 100..60000; default 5000, after the prior poll/callbacks finish. */
  pollIntervalMs?: number;
  /** Cooperative read deadline, whole milliseconds 10..30000; default 5000. */
  queryTimeoutMs?: number;
  /** >= max(2*pollIntervalMs, queryTimeoutMs), <=300000; default max(3*poll, 2*query). */
  maxAgeMs?: number;
  /** Whole milliseconds pollIntervalMs..86400000; default max(30000, pollIntervalMs). */
  sustainMs?: number;
}
export type EventAcknowledgmentIntentWatchMetric = 'due_backlog' | 'stalled_recovery' | 'parked_intents';
export type EventAcknowledgmentIntentAlertChange = 'unknown' | 'confirmation_lost' | 'firing' | 'resolved';
export type EventAcknowledgmentIntentAlertReason = 'read_failed' | 'incomplete' | 'stale' | 'out_of_order'
  | 'sample_gap' | 'threshold_exceeded' | 'threshold_cleared';
export interface EventAcknowledgmentIntentAlert {
  readonly accountId: string;
  readonly appId: string;
  readonly metric: EventAcknowledgmentIntentWatchMetric;
  readonly change: EventAcknowledgmentIntentAlertChange;
  readonly reason: EventAcknowledgmentIntentAlertReason;
  /** Local observation-completion time. health.observedAt is the database clock. */
  readonly at: string;
  /** Zero without health is not evidence of an empty backlog. */
  readonly value: number;
  readonly threshold: number;
  readonly health?: EventAcknowledgmentIntentHealth;
}
/** A failed local callback stops the watch. Raw callback errors are never retained. */
export class EventAcknowledgmentIntentWatchCallbackError extends Error {
  readonly code = 'event_acknowledgment_intent_watch_callback_failed';
  constructor(readonly alert: EventAcknowledgmentIntentAlert) {
    super('acknowledgment intent watch callback failed; local notification is unconfirmed');
    this.name = 'EventAcknowledgmentIntentWatchCallbackError';
  }
  toJSON(): object { return { name: this.name, code: this.code, alert: this.alert }; }
  [inspect.custom](): object { return this.toJSON(); }
}
function integer(value: number, minimum: number, maximum: number): boolean {
  return Number.isInteger(value) && value >= minimum && value <= maximum;
}
export function intentWatchPolicy(options: EventAcknowledgmentIntentWatchOptions) {
  const health = intentHealthOptions(options.health === undefined ? {} : options.health);
  const dueThreshold = options.dueThreshold === undefined ? 0 : options.dueThreshold;
  const stalledThreshold = options.stalledThreshold === undefined ? 0 : options.stalledThreshold;
  const parkedThreshold = options.parkedThreshold === undefined ? 0 : options.parkedThreshold;
  const pollIntervalMs = options.pollIntervalMs === undefined ? 5000 : options.pollIntervalMs;
  const queryTimeoutMs = options.queryTimeoutMs === undefined ? 5000 : options.queryTimeoutMs;
  const maxAgeMs = options.maxAgeMs === undefined ? Math.max(3 * pollIntervalMs, 2 * queryTimeoutMs) : options.maxAgeMs;
  const sustainMs = options.sustainMs === undefined ? Math.max(30000, pollIntervalMs) : options.sustainMs;
  if (!integer(dueThreshold, 0, health.scanLimit) || !integer(stalledThreshold, 0, health.scanLimit)
    || !integer(parkedThreshold, 0, health.scanLimit) || dueThreshold === 0 && stalledThreshold === 0 && parkedThreshold === 0
    || !integer(pollIntervalMs, 100, 60000)
    || !integer(queryTimeoutMs, 10, 30000) || !integer(maxAgeMs, Math.max(2 * pollIntervalMs, queryTimeoutMs), 300000)
    || !integer(sustainMs, pollIntervalMs, 86400000)) throw new TypeError('invalid acknowledgment intent watch policy');
  // Preserve the version 1 policy shape and field order when parked alerts are disabled.
  return Object.freeze({ health, dueThreshold, stalledThreshold, ...(parkedThreshold === 0 ? {} : { parkedThreshold }),
    pollIntervalMs, queryTimeoutMs, maxAgeMs, sustainMs });
}
export type IntentWatchPolicy = ReturnType<typeof intentWatchPolicy>;
export type IntentWatchLoss = Exclude<EventAcknowledgmentIntentAlertReason, 'threshold_exceeded' | 'threshold_cleared'>;
export function intentWatchEvidence(health: EventAcknowledgmentIntentHealth | undefined, previousObserved: number | undefined,
  previousPoll: number | undefined, wall: number, local: number, maxAge: number): IntentWatchLoss | undefined {
  if (!health) return 'read_failed';
  if (!health.complete || health.truncated || health.invalidRows !== 0) return 'incomplete';
  const observed = Date.parse(health.observedAt);
  if (!Number.isFinite(observed) || observed > wall || wall - observed > maxAge) return 'stale';
  if (previousObserved !== undefined && observed <= previousObserved) return 'out_of_order';
  if (previousPoll !== undefined && local - previousPoll > maxAge) return 'sample_gap';
  return undefined;
}
interface Transition { change: EventAcknowledgmentIntentAlertChange; reason: EventAcknowledgmentIntentAlertReason }
export class IntentWatchCondition {
  #state: 'unknown' | 'clear' | 'pending' | 'firing' = 'unknown';
  #reason?: IntentWatchLoss;
  #first?: number;
  #samples = 0;
  #clearing = false;
  constructor(readonly metric: EventAcknowledgmentIntentWatchMetric, readonly threshold: number) {}
  #reset(): void { this.#first = undefined; this.#samples = 0; this.#clearing = false; }
  update(value: number, reason: IntentWatchLoss | undefined, local: number, sustain: number): Transition | undefined {
    if (reason !== undefined) {
      const change = this.#state === 'firing' ? 'confirmation_lost' : 'unknown';
      const emit = this.#state !== 'unknown' || this.#reason !== reason;
      this.#state = 'unknown'; this.#reason = reason; this.#reset();
      return emit ? { change, reason } : undefined;
    }
    this.#reason = undefined;
    const breach = value >= this.threshold;
    if (this.#state === 'firing') {
      if (breach) { this.#reset(); return undefined; }
      if (!this.#clearing) { this.#first = local; this.#samples = 1; this.#clearing = true; return undefined; }
      this.#samples++;
      if (this.#samples >= 2 && local - this.#first! >= sustain) {
        this.#state = 'clear'; this.#reset();
        return { change: 'resolved', reason: 'threshold_cleared' };
      }
      return undefined;
    }
    if (!breach) { this.#state = 'clear'; this.#reset(); return undefined; }
    if (this.#state !== 'pending') { this.#state = 'pending'; this.#first = local; this.#samples = 1; return undefined; }
    this.#samples++;
    if (this.#samples >= 2 && local - this.#first! >= sustain) {
      this.#state = 'firing'; this.#reset();
      return { change: 'firing', reason: 'threshold_exceeded' };
    }
    return undefined;
  }
}
