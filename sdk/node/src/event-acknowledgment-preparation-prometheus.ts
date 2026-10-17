/** Pure Prometheus text 0.0.4 exposition; no registry, server, timers or retained observations. */
import { EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS,
  type EventAcknowledgmentPreparationHistogram, type EventAcknowledgmentPreparationMetricsSnapshot } from './event-acknowledgment-preparation-metrics.js';
import { eventZeroes, outcomeZeroes, reasonZeroes, stageZeroes } from './event-acknowledgment-preparation-metric-labels.js';

export const EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE = 'text/plain; version=0.0.4; charset=utf-8';
export interface EventAcknowledgmentPreparationPrometheusOptions {
  /** Default: gregale_event_acknowledgment_preparation. ASCII identifier, 1..128 characters; an underscore separates suffixes. */
  readonly prefix?: string;
}
const defaultPrefix = 'gregale_event_acknowledgment_preparation';
function invalid(): never { throw new TypeError('invalid acknowledgment preparation metrics exposition input'); }
/** Copy exact own data properties, rejecting accessors without calling them. */
function dataObject(value: unknown, keys: readonly string[], optional: readonly string[] = []): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid();
  const names = Reflect.ownKeys(value), copy: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
  if (names.length > keys.length || names.some(key => typeof key !== 'string' || !keys.includes(key))) invalid();
  for (const key of keys) {
    const property = Object.getOwnPropertyDescriptor(value, key);
    if (!property) { if (!optional.includes(key)) invalid(); }
    else {
      if (!Object.hasOwn(property, 'value')) invalid();
      copy[key] = property.value;
    }
  }
  return copy;
}
function count(value: unknown): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) invalid();
  return value;
}
function flag(value: unknown): boolean { if (typeof value !== 'boolean') invalid(); return value; }
function counters<K extends string>(value: unknown, labels: Readonly<Record<K, number>>): Record<K, number> {
  const keys = Object.keys(labels) as K[], data = dataObject(value, keys);
  const copy = Object.create(null) as Record<K, number>;
  for (const key of keys) copy[key] = count(data[key]);
  return copy;
}
function histogram(value: unknown): EventAcknowledgmentPreparationHistogram {
  const data = dataObject(value, ['buckets', 'count', 'sumMs', 'saturated']);
  const bounds = EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS;
  const raw = data.buckets;
  // Inspect array descriptors too: sparse arrays, extra keys and accessor entries are not snapshots.
  if (!Array.isArray(raw) || Object.getOwnPropertyDescriptor(raw, 'length')?.value !== bounds.length + 1) invalid();
  // Exact key count plus every required index below leaves no room for extra keys or symbols.
  if (Reflect.ownKeys(raw).length !== bounds.length + 2) invalid();
  let previous = 0;
  const buckets = Array.from({ length: bounds.length + 1 }, (_, index) => {
    const property = Object.getOwnPropertyDescriptor(raw, String(index));
    if (!property || !Object.hasOwn(property, 'value')) invalid();
    const bucket = dataObject(property.value, ['upperBoundMs', 'count']);
    const upperBoundMs = index === bounds.length ? null : bounds[index]!;
    if (bucket.upperBoundMs !== upperBoundMs) invalid();
    const current = count(bucket.count);
    if (current < previous) invalid();
    previous = current;
    return { upperBoundMs, count: current };
  });
  const total = count(data.count), sumMs = count(data.sumMs), saturated = flag(data.saturated);
  if (previous !== total || total === 0 && sumMs !== 0) invalid();
  return { buckets, count: total, sumMs, saturated };
}
function validated(snapshot: EventAcknowledgmentPreparationMetricsSnapshot,
  options: EventAcknowledgmentPreparationPrometheusOptions): { prefix: string; snapshot: EventAcknowledgmentPreparationMetricsSnapshot } {
  try {
    const config = dataObject(options, ['prefix'], ['prefix']);
    const prefix = config.prefix === undefined ? defaultPrefix : config.prefix;
    // Check every character, including final newlines; never interpolate arbitrary label or HELP strings.
    if (typeof prefix !== 'string' || prefix.length < 1 || prefix.length > 128
      || !/^[a-zA-Z_]/.test(prefix) || /[^a-zA-Z0-9_]/.test(prefix)) invalid();
    const data = dataObject(snapshot, ['eventCounts', 'operationOutcomes', 'preparationOutcomes', 'localCommits',
      'renewalFailures', 'unknownCommitsByStage', 'preparationDuration', 'operationDuration', 'saturated']);
    const renewals = dataObject(data.renewalFailures, ['local', 'platform']);
    const preparationDuration = histogram(data.preparationDuration), operationDuration = histogram(data.operationDuration);
    const saturated = flag(data.saturated);
    if (!saturated && (preparationDuration.saturated || operationDuration.saturated)) invalid();
    return { prefix, snapshot: {
      eventCounts: counters(data.eventCounts, eventZeroes), operationOutcomes: counters(data.operationOutcomes, outcomeZeroes),
      preparationOutcomes: counters(data.preparationOutcomes, { returned: 0, threw: 0 }),
      localCommits: counters(data.localCommits, { newReceipt: 0, replayedReceipt: 0 }),
      renewalFailures: { local: counters(renewals.local, reasonZeroes), platform: counters(renewals.platform, reasonZeroes) },
      unknownCommitsByStage: counters(data.unknownCommitsByStage, stageZeroes), preparationDuration, operationDuration, saturated,
    } };
  } catch { return invalid(); }
}
/**
 * Format one complete cumulative snapshot, including zeroes, HELP/TYPE metadata and a final newline.
 * Durations use seconds; null histogram bounds become +Inf. Fixed labels only, no timestamps or OpenMetrics EOF.
 * Throws a generic TypeError for malformed current-contract snapshots/options before returning any text.
 */
export function formatEventAcknowledgmentPreparationMetrics(snapshot: EventAcknowledgmentPreparationMetricsSnapshot,
  options: EventAcknowledgmentPreparationPrometheusOptions = {}): string {
  const { prefix, snapshot: data } = validated(snapshot, options), lines: string[] = [];
  function family(suffix: string, help: string, type: 'counter' | 'histogram' | 'gauge'): string {
    const name = `${prefix}_${suffix}`;
    lines.push(`# HELP ${name} ${help}`, `# TYPE ${name} ${type}`);
    return name;
  }
  function counter(suffix: string, help: string, label: string, values: Readonly<Record<string, number>>): void {
    const name = family(suffix, help, 'counter');
    for (const [key, value] of Object.entries(values)) lines.push(`${name}{${label}="${key}"} ${value}`);
  }
  counter('events_total', 'Accepted preparation lifecycle observations by event kind.', 'kind', data.eventCounts);
  counter('operations_total', 'Prepared operation shutdown observations by outcome.', 'outcome', data.operationOutcomes);
  counter('callbacks_total', 'Preparation callback settlement observations by outcome.', 'outcome', data.preparationOutcomes);
  counter('local_commits_total', 'Confirmed local receipt and acknowledgment intent commits by receipt disposition.', 'receipt',
    { new: data.localCommits.newReceipt, replayed: data.localCommits.replayedReceipt });
  const renewals = family('renewal_failures_total', 'Observed lease renewal failures by lease and reason.', 'counter');
  for (const lease of ['local', 'platform'] as const) {
    for (const [reason, value] of Object.entries(data.renewalFailures[lease])) {
      lines.push(`${renewals}{lease="${lease}",reason="${reason}"} ${value}`);
    }
  }
  counter('unknown_commits_total', 'Uncertain local commit observations by stage.', 'stage', data.unknownCommitsByStage);
  function duration(suffix: string, help: string, value: EventAcknowledgmentPreparationHistogram): void {
    const name = family(suffix, help, 'histogram');
    for (const bucket of value.buckets) {
      lines.push(`${name}_bucket{le="${bucket.upperBoundMs === null ? '+Inf' : bucket.upperBoundMs / 1000}"} ${bucket.count}`);
    }
    lines.push(`${name}_sum ${value.sumMs / 1000}`, `${name}_count ${value.count}`);
  }
  duration('callback_duration_seconds', 'Preparation callback settlement duration in seconds.', data.preparationDuration);
  duration('operation_duration_seconds', 'Prepared operation duration through shutdown cleanup in seconds.', data.operationDuration);
  const saturated = family('saturated', 'Sticky safe integer clipping flag by scope; clipped values are lower bounds.', 'gauge');
  lines.push(`${saturated}{scope="collector"} ${Number(data.saturated)}`,
    `${saturated}{scope="callback_duration"} ${Number(data.preparationDuration.saturated)}`,
    `${saturated}{scope="operation_duration"} ${Number(data.operationDuration.saturated)}`);
  return `${lines.join('\n')}\n`;
}
