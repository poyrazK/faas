/** Fixed-memory counters for live preparation events; no retained events, identities or exporter dependency. */
import type { EventAcknowledgmentPreparationEvent, EventAcknowledgmentPreparationOutcome,
  EventAcknowledgmentPreparationReason, EventAcknowledgmentPreparationStage } from './event-acknowledgment-preparation-events.js';
import { eventZeroes, outcomeZeroes, reasonZeroes, stageZeroes } from './event-acknowledgment-preparation-metric-labels.js';

type Kind = EventAcknowledgmentPreparationEvent['kind'];
/** Finite inclusive bounds; snapshots add a final null bound representing +Inf. */
export const EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS = Object.freeze(
  [1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000, 300000] as const);
export interface EventAcknowledgmentPreparationHistogram {
  readonly buckets: readonly Readonly<{ upperBoundMs: number | null; count: number }>[];
  readonly count: number;
  readonly sumMs: number;
  /** At least one bucket/count/sum clipped at Number.MAX_SAFE_INTEGER; affected values are lower bounds. */
  readonly saturated: boolean;
}
export interface EventAcknowledgmentPreparationMetricsSnapshot {
  readonly eventCounts: Readonly<Record<Kind, number>>;
  /** Shutdown observations only; never inferred from intent_committed or renewal_failed. */
  readonly operationOutcomes: Readonly<Record<EventAcknowledgmentPreparationOutcome, number>>;
  readonly preparationOutcomes: Readonly<Record<'returned' | 'threw', number>>;
  readonly localCommits: Readonly<{ newReceipt: number; replayedReceipt: number }>;
  readonly renewalFailures: Readonly<{ local: Readonly<Record<EventAcknowledgmentPreparationReason, number>>;
    platform: Readonly<Record<EventAcknowledgmentPreparationReason, number>> }>;
  readonly unknownCommitsByStage: Readonly<Record<EventAcknowledgmentPreparationStage, number>>;
  readonly preparationDuration: EventAcknowledgmentPreparationHistogram;
  readonly operationDuration: EventAcknowledgmentPreparationHistogram;
  /** Sticky arithmetic clipping flag, not a completeness/coverage claim. */
  readonly saturated: boolean;
}
function invalid(): never { throw new TypeError('invalid acknowledgment preparation metrics event'); }
function label<K extends string>(labels: Record<K, number>, value: unknown): value is K {
  return typeof value === 'string' && Object.hasOwn(labels, value);
}
function milliseconds(value: unknown): value is number { return Number.isSafeInteger(value) && (value as number) >= 0; }
function shape(data: Record<string, unknown>, required: readonly string[], optional: readonly string[] = []): void {
  if (required.some(key => !Object.hasOwn(data, key))
    || Object.keys(data).some(key => !required.includes(key) && !optional.includes(key))) invalid();
}
/** Copy only own primitive data properties; never invoke event getters or retain the source object. */
function validated(event: EventAcknowledgmentPreparationEvent): EventAcknowledgmentPreparationEvent {
  try {
    if (!event || typeof event !== 'object' || Array.isArray(event)) invalid();
    const keys = Reflect.ownKeys(event), data: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
    if (keys.length > 5) invalid();
    for (const key of keys) {
      if (typeof key !== 'string' || !['kind', 'elapsedMs', 'outcome', 'durationMs', 'lease', 'reason', 'replayed', 'stage'].includes(key)) invalid();
      const property = Object.getOwnPropertyDescriptor(event, key);
      if (!property || !Object.hasOwn(property, 'value')) invalid();
      data[key] = property.value;
    }
    if (!label(eventZeroes, data.kind) || !milliseconds(data.elapsedMs)) invalid();
    const base = ['kind', 'elapsedMs'];
    switch (data.kind) {
      case 'started': case 'claim_acquired': case 'preparation_started': shape(data, base); break;
      case 'preparation_finished':
        shape(data, [...base, 'outcome', 'durationMs']);
        if (!['returned', 'threw'].includes(data.outcome as string) || !milliseconds(data.durationMs)) invalid();
        break;
      case 'renewal_failed':
        shape(data, [...base, 'lease', 'reason']);
        if (data.lease !== 'local' && data.lease !== 'platform' || !label(reasonZeroes, data.reason)) invalid();
        break;
      case 'intent_committed':
        shape(data, [...base, 'replayed']); if (typeof data.replayed !== 'boolean') invalid(); break;
      case 'commit_unknown':
        shape(data, [...base, 'stage']); if (!label(stageZeroes, data.stage)) invalid(); break;
      case 'shutdown':
        shape(data, [...base, 'stage', 'outcome'], ['reason']);
        if (!label(stageZeroes, data.stage) || !label(outcomeZeroes, data.outcome)
          || data.reason !== undefined && !label(reasonZeroes, data.reason)) invalid();
        break;
    }
    return data as unknown as EventAcknowledgmentPreparationEvent;
  } catch { return invalid(); }
}
class Histogram {
  readonly #counts = Array<number>(EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS.length + 1).fill(0);
  #sumMs = 0;
  #saturated = false;
  #add(value: number, amount: number): number {
    if (value > Number.MAX_SAFE_INTEGER - amount) { this.#saturated = true; return Number.MAX_SAFE_INTEGER; }
    return value + amount;
  }
  observe(value: number): void {
    const bounds = EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS;
    for (let index = 0; index < this.#counts.length; index++) {
      if (index === bounds.length || value <= bounds[index]!) this.#counts[index] = this.#add(this.#counts[index]!, 1);
    }
    this.#sumMs = this.#add(this.#sumMs, value);
  }
  snapshot(): EventAcknowledgmentPreparationHistogram {
    const bounds = EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS;
    return Object.freeze({ buckets: Object.freeze(this.#counts.map((count, index) => Object.freeze({
      upperBoundMs: index === bounds.length ? null : bounds[index]!, count }))),
    count: this.#counts[bounds.length]!, sumMs: this.#sumMs, saturated: this.#saturated });
  }
}
/** One process-local observation stream. Duplicate records count again; snapshots are not recovery evidence. */
export class EventAcknowledgmentPreparationMetrics {
  readonly #events: Record<Kind, number> = { ...eventZeroes };
  readonly #outcomes: Record<EventAcknowledgmentPreparationOutcome, number> = { ...outcomeZeroes };
  readonly #preparations = { returned: 0, threw: 0 };
  readonly #commits = { newReceipt: 0, replayedReceipt: 0 };
  readonly #renewals = { local: { ...reasonZeroes }, platform: { ...reasonZeroes } };
  readonly #unknowns: Record<EventAcknowledgmentPreparationStage, number> = { ...stageZeroes };
  readonly #preparationDuration = new Histogram();
  readonly #operationDuration = new Histogram();
  #saturated = false;
  #increment<K extends string>(counts: Record<K, number>, key: K): void {
    if (counts[key] === Number.MAX_SAFE_INTEGER) this.#saturated = true;
    else counts[key]++;
  }
  /** Pass directly as onEvent. Validate the complete event before mutating any counter. */
  readonly record = (event: EventAcknowledgmentPreparationEvent): void => {
    const sample = validated(event);
    this.#increment(this.#events, sample.kind);
    switch (sample.kind) {
      case 'preparation_finished':
        this.#increment(this.#preparations, sample.outcome); this.#preparationDuration.observe(sample.durationMs); break;
      case 'renewal_failed': this.#increment(this.#renewals[sample.lease], sample.reason); break;
      case 'intent_committed': this.#increment(this.#commits, sample.replayed ? 'replayedReceipt' : 'newReceipt'); break;
      case 'commit_unknown': this.#increment(this.#unknowns, sample.stage); break;
      case 'shutdown': this.#increment(this.#outcomes, sample.outcome); this.#operationDuration.observe(sample.elapsedMs); break;
    }
  };
  /** Detached, recursively frozen JSON-safe cumulative observations. No reset, restore, merge or export side effects. */
  snapshot(): EventAcknowledgmentPreparationMetricsSnapshot {
    const preparationDuration = this.#preparationDuration.snapshot(), operationDuration = this.#operationDuration.snapshot();
    return Object.freeze({ eventCounts: Object.freeze({ ...this.#events }), operationOutcomes: Object.freeze({ ...this.#outcomes }),
      preparationOutcomes: Object.freeze({ ...this.#preparations }), localCommits: Object.freeze({ ...this.#commits }),
      renewalFailures: Object.freeze({ local: Object.freeze({ ...this.#renewals.local }), platform: Object.freeze({ ...this.#renewals.platform }) }),
      unknownCommitsByStage: Object.freeze({ ...this.#unknowns }), preparationDuration, operationDuration,
      saturated: this.#saturated || preparationDuration.saturated || operationDuration.saturated });
  }
}
