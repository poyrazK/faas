/** One owner-scoped cached observation. Scraping performs no database I/O. */
import { performance } from 'node:perf_hooks';
import type { EventAcknowledgmentIntentHealth } from './event-acknowledgment-intent-health.js';
import { intentScope } from './event-acknowledgment-intent-store.js';

export interface EventAcknowledgmentIntentHealthMetricsOptions {
  /** Whole milliseconds 100..300000; default 30000. Bound the host poll/query intervals separately. */
  readonly maxAgeMs?: number;
}
export interface EventAcknowledgmentIntentHealthMetricsSample {
  readonly observedAt: string;
  readonly observationAgeSeconds: number;
  readonly complete: boolean;
  readonly truncated: boolean;
  readonly scanLimit: number;
  readonly invalidRows: number;
  readonly outstanding: number;
  readonly due: number;
  readonly stalled: number;
  readonly parked: number;
}
export interface EventAcknowledgmentIntentHealthMetricsSnapshot {
  readonly readSuccess: boolean;
  /** Complete, fresh, advancing evidence at recording, still fresh at snapshot time. */
  readonly evidenceEligible: boolean;
  readonly maxObservationAgeSeconds: number;
  /** Absent before a successful read or after failure; missing counts are never fabricated as zero. */
  readonly health?: Readonly<EventAcknowledgmentIntentHealthMetricsSample>;
}
export interface EventAcknowledgmentIntentHealthPrometheusOptions {
  /** Default gregale_event_acknowledgment_recovery; ASCII identifier, 1..128 characters. */
  readonly prefix?: string;
}
export const EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE = 'text/plain; version=0.0.4; charset=utf-8';
function invalid(): never { throw new TypeError('invalid acknowledgment recovery health metrics input'); }
/** Inspect selected own data properties without invoking getters or retaining input objects. */
function dataObject(value: unknown, keys: readonly string[], optional: readonly string[] = [], exact = true): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid();
  const names = Reflect.ownKeys(value), out: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
  if (names.length > (exact ? keys.length : 32) || exact && names.some(key => typeof key !== 'string' || !keys.includes(key))) invalid();
  for (const key of keys) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (!descriptor) { if (!optional.includes(key)) invalid(); }
    else { if (!Object.hasOwn(descriptor, 'value')) invalid(); out[key] = descriptor.value; }
  }
  return out;
}
function count(value: unknown, maximum: number): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0 || value > maximum) invalid();
  return value;
}
function flag(value: unknown): boolean { if (typeof value !== 'boolean') invalid(); return value; }
function age(value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0 || value > Number.MAX_SAFE_INTEGER) invalid();
  return value;
}
function instant(value: unknown): number {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/.test(value)) invalid();
  const at = new Date(value);
  if (!Number.isFinite(at.getTime()) || at.getUTCFullYear() < 1 || at.toISOString() !== value) invalid();
  return at.getTime();
}
function sample(data: Record<string, unknown>): Omit<EventAcknowledgmentIntentHealthMetricsSample, 'observationAgeSeconds'> {
  const scanLimit = count(data.scanLimit, 10000);
  if (scanLimit === 0) invalid();
  const complete = flag(data.complete), truncated = flag(data.truncated), invalidRows = count(data.invalidRows, scanLimit);
  const outstanding = count(data.outstanding, scanLimit), due = count(data.due, scanLimit);
  const stalled = count(data.stalled, scanLimit), parked = count(data.parked, scanLimit);
  instant(data.observedAt);
  if (complete !== (!truncated && invalidRows === 0) || invalidRows > outstanding || due + parked > outstanding
    || stalled > due || truncated && outstanding !== scanLimit) invalid();
  return { observedAt: data.observedAt as string, complete, truncated, scanLimit, invalidRows, outstanding, due, stalled, parked };
}
function healthSample(health: EventAcknowledgmentIntentHealth, account: string, app: string) {
  const data = dataObject(health, ['accountId', 'appId', 'observedAt', 'coverage', 'complete', 'truncated', 'scanLimit',
    'invalidRows', 'outstanding', 'pending', 'unconfirmed', 'parked', 'due', 'leased', 'scheduled', 'expiredClaims', 'stalled'], [], false);
  const [owner, target] = intentScope(data.accountId as string, data.appId as string);
  if (owner !== account || target !== app || data.coverage !== 'bounded_retained_unfinished_intents') invalid();
  const out = sample(data), pending = count(data.pending, out.scanLimit), unconfirmed = count(data.unconfirmed, out.scanLimit);
  const leased = count(data.leased, out.scanLimit), scheduled = count(data.scheduled, out.scanLimit);
  if (pending + unconfirmed + out.parked !== out.outstanding || out.due + leased + scheduled + out.parked !== out.outstanding
    || count(data.expiredClaims, out.scanLimit) > out.due + scheduled) invalid();
  return out;
}
/** Fixed memory for one normalized owner scope. Feed sequential completed reads explicitly.
 * Failed/malformed reads clear cached counts, retaining the database high-water mark.
 * No timers, SQL, listeners, notification state or identity labels are installed. */
export class EventAcknowledgmentIntentHealthMetrics {
  readonly #account: string;
  readonly #app: string;
  readonly #maxAgeMs: number;
  #previousObserved?: number;
  #sample?: { health: Omit<EventAcknowledgmentIntentHealthMetricsSample, 'observationAgeSeconds'>;
    wall: number; local: number; ageMs: number; eligible: boolean };
  constructor(accountId: string, appId: string, options: EventAcknowledgmentIntentHealthMetricsOptions = {}) {
    try {
      [this.#account, this.#app] = intentScope(accountId, appId);
      const config = dataObject(options, ['maxAgeMs'], ['maxAgeMs']);
      this.#maxAgeMs = config.maxAgeMs === undefined ? 30000 : count(config.maxAgeMs, 300000);
      if (this.#maxAgeMs < 100) invalid();
    } catch { invalid(); }
  }
  readonly record = (health: EventAcknowledgmentIntentHealth): void => {
    try {
      const copied = healthSample(health, this.#account, this.#app), observed = instant(copied.observedAt);
      const wall = Date.now(), local = performance.now(), ageMs = Math.max(0, wall - observed);
      const fresh = observed <= wall && ageMs <= this.#maxAgeMs;
      const advancing = this.#previousObserved === undefined || observed > this.#previousObserved;
      this.#sample = { health: copied, wall, local, ageMs, eligible: copied.complete && fresh && advancing };
      // Fresh incomplete observations also advance the high-water mark, as in the watches.
      if (fresh && advancing) this.#previousObserved = observed;
    } catch { this.recordReadFailure(); invalid(); }
  };
  readonly recordReadFailure = (): void => { this.#sample = undefined; };
  snapshot(): EventAcknowledgmentIntentHealthMetricsSnapshot {
    const saved = this.#sample, maxObservationAgeSeconds = this.#maxAgeMs / 1000;
    if (!saved) return Object.freeze({ readSuccess: false, evidenceEligible: false, maxObservationAgeSeconds });
    const wall = Date.now(), local = performance.now(), elapsed = local - saved.local;
    const ageMs = Math.max(0, wall - instant(saved.health.observedAt), saved.ageMs + Math.max(0, elapsed));
    return Object.freeze({ readSuccess: true, maxObservationAgeSeconds,
      evidenceEligible: saved.eligible && wall >= saved.wall && elapsed >= 0 && ageMs <= this.#maxAgeMs,
      health: Object.freeze({ ...saved.health, observationAgeSeconds: ageMs / 1000 }) });
  }
}
function exposition(input: EventAcknowledgmentIntentHealthMetricsSnapshot, options: EventAcknowledgmentIntentHealthPrometheusOptions) {
  try {
    const config = dataObject(options, ['prefix'], ['prefix']);
    const prefix = config.prefix === undefined ? 'gregale_event_acknowledgment_recovery' : config.prefix;
    if (typeof prefix !== 'string' || prefix.length < 1 || prefix.length > 128
      || !/^[a-zA-Z_]/.test(prefix) || /[^a-zA-Z0-9_]/.test(prefix)) invalid();
    const data = dataObject(input, ['readSuccess', 'evidenceEligible', 'maxObservationAgeSeconds', 'health'], ['health']);
    const readSuccess = flag(data.readSuccess), evidenceEligible = flag(data.evidenceEligible);
    const maxAge = age(data.maxObservationAgeSeconds);
    if (maxAge < 0.1 || maxAge > 300 || readSuccess !== (data.health !== undefined)) invalid();
    let health: EventAcknowledgmentIntentHealthMetricsSample | undefined;
    if (data.health !== undefined) {
      const values = dataObject(data.health, ['observedAt', 'observationAgeSeconds', 'complete', 'truncated', 'scanLimit',
        'invalidRows', 'outstanding', 'due', 'stalled', 'parked']);
      health = { ...sample(values), observationAgeSeconds: age(values.observationAgeSeconds) };
    }
    if (evidenceEligible && (!health?.complete || health.observationAgeSeconds > maxAge)) invalid();
    return { prefix, readSuccess, evidenceEligible, maxAge, health };
  } catch { return invalid(); }
}
/** Pure Prometheus text 0.0.4 with fixed gauge names, no labels/identities or explicit sample timestamps.
 * Counts after incomplete reads are lower bounds. Missing reads omit all sample gauges.
 * Use fresh snapshot() output each scrape and gate counts on complete, fresh evidence. */
export function formatEventAcknowledgmentIntentHealthMetrics(snapshot: EventAcknowledgmentIntentHealthMetricsSnapshot,
  options: EventAcknowledgmentIntentHealthPrometheusOptions = {}): string {
  const data = exposition(snapshot, options), lines: string[] = [];
  function gauge(suffix: string, help: string, value: number): void {
    const name = `${data.prefix}_${suffix}`;
    lines.push(`# HELP ${name} ${help}`, `# TYPE ${name} gauge`, `${name} ${value}`);
  }
  gauge('read_success', 'Latest read supplied valid scoped metadata; zero before a read or after failure.', Number(data.readSuccess));
  gauge('evidence_eligible', 'Complete fresh advancing evidence remains fresh at snapshot time.', Number(data.evidenceEligible));
  gauge('max_observation_age_seconds', 'Configured maximum observation age in seconds.', data.maxAge);
  if (data.health) {
    const h = data.health;
    gauge('complete', 'Latest bounded sample has complete coverage.', Number(h.complete));
    gauge('truncated', 'Latest sample exceeded the scan limit; counts are lower bounds.', Number(h.truncated));
    gauge('scan_limit', 'Maximum sampled unfinished intents.', h.scanLimit);
    gauge('invalid_rows', 'Sampled rows with invalid timestamp metadata.', h.invalidRows);
    gauge('outstanding', 'Sampled retained unfinished intents including parked work.', h.outstanding);
    gauge('due', 'Sampled due intents excluding live claims and parked work.', h.due);
    gauge('stalled', 'Sampled stalled eligible intents excluding parked work.', h.stalled);
    gauge('parked', 'Sampled retained intents whose recovery claim budget is exhausted.', h.parked);
    gauge('observation_timestamp_seconds', 'Database observation time in Unix seconds.', instant(h.observedAt) / 1000);
    gauge('observation_age_seconds', 'Conservative observation age using wall and local monotonic elapsed time.', h.observationAgeSeconds);
  }
  return `${lines.join('\n')}\n`;
}
