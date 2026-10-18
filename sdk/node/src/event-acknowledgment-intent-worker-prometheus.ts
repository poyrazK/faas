/** Pure worker gauges; shared Go names/HELP, with Node pool-cleanup diagnostics. */
import { workerMetricAge, workerMetricCount, workerMetricData, workerMetricInstant, workerMetricResult,
  type EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot, type EventAcknowledgmentIntentRecoveryWorkerPassSample,
} from './event-acknowledgment-intent-worker-metrics.js';

export interface EventAcknowledgmentIntentRecoveryWorkerPrometheusOptions {
  /** Default gregale_event_acknowledgment_recovery_worker; ASCII identifier 1..128 characters. */
  readonly prefix?: string;
}
function invalid(): never { throw new TypeError('invalid acknowledgment recovery worker metrics input'); }
function flag(value: unknown): boolean { if (typeof value !== 'boolean') invalid(); return value; }
function exposition(input: EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot, options: EventAcknowledgmentIntentRecoveryWorkerPrometheusOptions) {
  try {
    const config = workerMetricData(options, ['prefix'], ['prefix']);
    const prefix = config.prefix === undefined ? 'gregale_event_acknowledgment_recovery_worker' : config.prefix;
    if (typeof prefix !== 'string' || prefix.length < 1 || prefix.length > 128 || !/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(prefix)) invalid();
    const data = workerMetricData(input, ['clockValid', 'inFlight', 'overdue', 'maxIdleSeconds', 'maxPassSeconds',
      'activityAt', 'activityAgeSeconds', 'lastStartedAt', 'lastPass'], ['lastStartedAt', 'lastPass']);
    const clockValid = flag(data.clockValid), inFlight = flag(data.inFlight), overdue = flag(data.overdue);
    const maxIdleSeconds = workerMetricAge(data.maxIdleSeconds), maxPassSeconds = workerMetricAge(data.maxPassSeconds);
    const activityAt = workerMetricInstant(data.activityAt), activityAgeSeconds = workerMetricAge(data.activityAgeSeconds);
    if (maxIdleSeconds < 0.1 || maxIdleSeconds > 86400 || maxPassSeconds < 1 || maxPassSeconds > 86400
      || overdue !== (clockValid && activityAgeSeconds > (inFlight ? maxPassSeconds : maxIdleSeconds))) invalid();
    const lastStartedAt = data.lastStartedAt === undefined ? undefined : workerMetricInstant(data.lastStartedAt);
    let lastPass: Readonly<EventAcknowledgmentIntentRecoveryWorkerPassSample> | undefined;
    if (data.lastPass !== undefined) {
      const p = workerMetricData(data.lastPass, ['startedAt', 'completedAt', 'durationSeconds', 'completionAgeSeconds', 'result'], ['result']);
      const started = workerMetricInstant(p.startedAt), completed = workerMetricInstant(p.completedAt);
      lastPass = Object.freeze({ startedAt: p.startedAt as string, completedAt: p.completedAt as string,
        durationSeconds: workerMetricAge(p.durationSeconds), completionAgeSeconds: workerMetricAge(p.completionAgeSeconds),
        ...(p.result === undefined ? {} : { result: workerMetricResult(p.result) }) });
      if (clockValid && (completed < started || inFlight && (lastStartedAt === undefined || lastStartedAt < completed))) invalid();
      if (!inFlight && (activityAt !== completed || lastStartedAt !== started || activityAgeSeconds !== lastPass.completionAgeSeconds)) invalid();
    }
    if (lastStartedAt === undefined ? inFlight || lastPass !== undefined :
      inFlight ? activityAt !== lastStartedAt : lastPass === undefined) invalid();
    return { prefix, clockValid, inFlight, overdue, maxIdleSeconds, maxPassSeconds, activityAt, activityAgeSeconds, lastStartedAt, lastPass };
  } catch { return invalid(); }
}

/** Format a fresh snapshot per scrape, using the health Prometheus content type.
 * Fixed gauges have no identity labels or explicit sample timestamps. Missing
 * returned progress omits outcome gauges; failures never fabricate zero counts. */
export function formatEventAcknowledgmentIntentRecoveryWorkerMetrics(snapshot: EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot,
  options: EventAcknowledgmentIntentRecoveryWorkerPrometheusOptions = {}): string {
  const data = exposition(snapshot, options), lines: string[] = [];
  function gauge(suffix: string, help: string, value: number): void {
    const name = `${data.prefix}_${suffix}`;
    lines.push(`# HELP ${name} ${help}`, `# TYPE ${name} gauge`, `${name} ${value}`);
  }
  gauge('clock_valid', 'Local activity clock has no detected regression since monitoring began.', Number(data.clockValid));
  gauge('overdue', 'Valid local activity age exceeds the idle or in-flight pass bound.', Number(data.overdue));
  gauge('in_flight', 'One explicitly monitored recovery pass has started and has not returned.', Number(data.inFlight));
  gauge('max_idle_seconds', 'Maximum expected gap before the first or next recovery pass starts.', data.maxIdleSeconds);
  gauge('max_pass_seconds', 'Maximum expected duration of a monitored recovery pass; not a deadline.', data.maxPassSeconds);
  gauge('activity_timestamp_seconds', 'Latest actual pass start or joined return, initially monitoring construction time.', data.activityAt / 1000);
  gauge('activity_age_seconds', 'Conservative age of actual activity using wall and local monotonic elapsed time.', data.activityAgeSeconds);
  gauge('last_pass_result_valid', 'Latest joined pass has valid bounded scalar progress; zero before any return.', Number(data.lastPass?.result !== undefined));
  if (data.lastStartedAt !== undefined) gauge('last_started_timestamp_seconds', 'Latest actual monitored recovery pass start in Unix seconds.', data.lastStartedAt / 1000);
  if (data.lastPass) {
    const p = data.lastPass;
    gauge('last_completed_timestamp_seconds', 'Latest monitored recovery pass joined return in Unix seconds, regardless of outcome.', workerMetricInstant(p.completedAt) / 1000);
    gauge('last_pass_duration_seconds', 'Monotonic elapsed duration of the latest joined recovery pass.', p.durationSeconds);
    gauge('last_completed_age_seconds', 'Conservative age of the latest joined recovery pass return.', p.completionAgeSeconds);
    if (p.result) {
      const r = p.result;
      gauge('last_pass_success', 'Latest pass stopped at its work limit or an empty probe; not callback or business success.', Number(r.stopReason === 'limit' || r.stopReason === 'empty'));
      gauge('last_pass_attempts', 'Admissions in the latest joined pass, including empty probes.', workerMetricCount(r.attempts, 10000));
      gauge('last_pass_confirmed', 'Confirmed settlements in the latest joined pass.', r.confirmed);
      gauge('last_pass_unconfirmed', 'Known unconfirmed settlements in the latest joined pass.', r.unconfirmed);
      gauge('last_pass_parked', 'Known parking outcomes in the latest joined pass; not total retained parked work.', r.parked);
      gauge('last_pass_superseded', 'Superseded outcomes in the latest joined pass.', r.superseded);
      gauge('last_pass_empty', 'Empty probes in the latest joined pass; not proof of global drain.', r.empty);
      gauge('last_pass_claim_lost', 'Lost recovery claims in the latest joined pass.', r.claimLost);
      gauge('last_pass_interrupted', 'Interrupted admissions in the latest joined pass.', r.interrupted);
      gauge('last_pass_failed', 'Failed admissions in the latest joined pass, excluding unknown commits.', r.failed);
      gauge('last_pass_commit_unknown', 'Admissions with unknown commit outcome in the latest joined pass.', r.commitUnknown);
      gauge('last_pass_cleanup_failures', 'Admissions with failed pool release in the latest joined pass; may overlap known outcomes.', r.cleanupFailures);
    }
  }
  return `${lines.join('\n')}\n`;
}
