import { deadlineRatioRule, type EventDeadlineRatioOptions } from './event-deadline-ratio.js';
import type { EventSubscriptionDeadlineWindow } from './generated/models/EventSubscriptionDeadlineWindow.js';

export const EVENT_DEADLINE_REASONS = [
  'circuit_open', 'circuit_probe_wait', 'circuit_recovery_rate_limited', 'subscription_paused',
  'subscription_rate_limited', 'ordering_blocked', 'capacity_consumer', 'capacity_app',
  'capacity_account', 'routing_in_progress', 'receipt_processing', 'retry_backoff', 'workflow_routing', 'ready',
] as const;
export type EventDeadlineReason = typeof EVENT_DEADLINE_REASONS[number];
export interface EventDeadlineTarget { app: string; appId: string; subscriptionId: string }
export interface EventDeadlinePolicyOptions {
  withinMs: number;
  expiredRatio?: EventDeadlineRatioOptions;
  upcoming?: number;
  expired?: number;
  upcomingReasons?: Partial<Record<EventDeadlineReason, number>>;
  expiredReasons?: Partial<Record<EventDeadlineReason, number>>;
  upcomingRecovery?: number;
  expiredRecovery?: number;
  upcomingReasonsRecovery?: Partial<Record<EventDeadlineReason, number>>;
  expiredReasonsRecovery?: Partial<Record<EventDeadlineReason, number>>;
  sustainMs?: number;
  recoverySustainMs?: number;
}
export interface EventDeadlineRule {
  metric: string;
  cohort: 'upcoming' | 'expired';
  reason?: EventDeadlineReason;
  threshold: number;
  recoveryThreshold: number;
  /** Present only on expired ratio rules; count rules retain their version-1 shape. */
  minimumRecipients?: number;
}
export interface EventDeadlinePolicy {
  withinMs: number;
  sustainMs: number;
  recoverySustainMs: number;
  rules: EventDeadlineRule[];
}
export function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}
export function count(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}
export function duration(value: unknown, max = 86_400_000): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 100 && value <= max;
}
export function uuid(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(value)
    && value !== '00000000-0000-0000-0000-000000000000';
}
export function deadlineTarget(value: EventDeadlineTarget): EventDeadlineTarget {
  if (!record(value) || typeof value.app !== 'string' || !value.app.trim() || value.app.length > 253
    || !uuid(value.appId) || !uuid(value.subscriptionId)) throw new TypeError('invalid deadline watch target');
  return { app: value.app, appId: value.appId.toLowerCase(), subscriptionId: value.subscriptionId.toLowerCase() };
}
export function deadlinePolicy(options: EventDeadlinePolicyOptions): EventDeadlinePolicy {
  if (!record(options) || !duration(options.withinMs)) throw new TypeError('withinMs must be whole milliseconds 100..86400000');
  const sustainMs = options.sustainMs === undefined ? 60_000 : options.sustainMs, recoverySustainMs = options.recoverySustainMs === undefined ? 60_000 : options.recoverySustainMs;
  if (!duration(sustainMs) || !duration(recoverySustainMs)) throw new TypeError('invalid deadline confirmation duration');
  const rules: EventDeadlineRule[] = [];
  const add = (cohort: 'upcoming' | 'expired', warning: unknown, recovery: unknown, reason?: EventDeadlineReason) => {
    if (!count(warning) || (recovery !== undefined && (!count(recovery) || recovery >= warning))) {
      throw new TypeError('deadline counts require 0 <= recovery < enabled warning, within safe integer bounds');
    }
    if (warning > 0) rules.push({ metric: `deadline_window.${cohort}_recipients.${options.withinMs}${reason ? `.reason.${reason}` : ''}`,
      cohort, ...(reason ? { reason } : {}), threshold: warning, recoveryThreshold: recovery === undefined ? warning - 1 : recovery as number });
  };
  add('upcoming', options.upcoming === undefined ? 0 : options.upcoming, options.upcomingRecovery);
  add('expired', options.expired === undefined ? 0 : options.expired, options.expiredRecovery);
  for (const [cohort, warnings, recoveries] of [
    ['upcoming', options.upcomingReasons, options.upcomingReasonsRecovery],
    ['expired', options.expiredReasons, options.expiredReasonsRecovery],
  ] as const) {
    for (const map of [warnings, recoveries]) {
      if (map !== undefined && (!record(map) || Object.entries(map).some(([key, value]) =>
        !EVENT_DEADLINE_REASONS.includes(key as EventDeadlineReason) || !count(value)))) throw new TypeError('invalid deadline reason counts');
    }
    const keys = new Set([...Object.keys(warnings ?? {}), ...Object.keys(recoveries ?? {})]);
    for (const key of [...keys].sort() as EventDeadlineReason[]) add(cohort, warnings?.[key] ?? 0, recoveries?.[key], key);
  }
  if (options.expiredRatio !== undefined) rules.push(deadlineRatioRule(options.expiredRatio, options.withinMs));
  if (!rules.length) throw new TypeError('enable at least one deadline count or expired ratio warning');
  return { withinMs: options.withinMs, sustainMs, recoverySustainMs, rules };
}

/** Parse RFC3339 without discarding sub-millisecond observation ordering. */
export function timestamp(raw: unknown): bigint | undefined {
  if (typeof raw !== 'string') return undefined;
  const m = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|([+-])(\d{2}):(\d{2}))$/.exec(raw);
  if (!m) return undefined;
  const base = Date.parse(`${m[1]}T${m[2]}.000Z`);
  if (!Number.isFinite(base) || new Date(base).toISOString() !== `${m[1]}T${m[2]}.000Z`) return undefined;
  const hours = Number(m[6] ?? 0), minutes = Number(m[7] ?? 0);
  if (hours > 23 || minutes > 59) return undefined;
  const offset = (hours * 60 + minutes) * 60_000 * (m[5] === '-' ? -1 : 1);
  return BigInt(base - offset) * 1_000_000n + BigInt((m[3] ?? '').padEnd(9, '0'));
}
function partition(value: unknown, total: number): boolean {
  if (!record(value) || Object.keys(value).length !== EVENT_DEADLINE_REASONS.length) return false;
  let remaining = total;
  for (const key of EVENT_DEADLINE_REASONS) {
    const n = value[key];
    if (!count(n) || n > remaining) return false;
    remaining -= n;
  }
  return remaining === 0;
}
export function deadlineEvidence(value: unknown, target: EventDeadlineTarget, within: number): value is EventSubscriptionDeadlineWindow {
  if (!record(value) || !record(value.summary)) return false;
  const s = value.summary, at = timestamp(s.observed_at), end = timestamp(value.window_end_at);
  if (value.coverage !== 'all_retained_captured_pending_application_deadline_window'
    || s.coverage !== 'all_retained_captured_pending_application_recipients' || !uuid(s.app_id) || !uuid(s.subscription_id)
    || s.app_id.toLowerCase() !== target.appId || s.subscription_id.toLowerCase() !== target.subscriptionId
    || s.read_only !== true || s.complete !== true || at === undefined || end === undefined
    || value.within_millis !== within || end !== at + BigInt(within) * 1_000_000n
    || !count(s.pending_recipients) || !count(s.deadline_recipients) || s.deadline_recipients > s.pending_recipients
    || !count(s.already_expired_count) || s.already_expired_count > s.deadline_recipients
    || !count(value.upcoming_recipients) || value.upcoming_recipients > s.deadline_recipients - s.already_expired_count
    || !partition(value.upcoming_waiting_reasons, value.upcoming_recipients)
    || !partition(value.expired_waiting_reasons, s.already_expired_count)) return false;
  const earliest = timestamp(s.earliest_deadline_at), upcoming = timestamp(value.earliest_upcoming_deadline_at);
  if ((s.deadline_recipients === 0) !== (s.earliest_deadline_at === undefined)
    || (s.deadline_recipients > 0 && (earliest === undefined || (earliest <= at) !== (s.already_expired_count > 0)))
    || (value.upcoming_recipients === 0) !== (value.earliest_upcoming_deadline_at === undefined)
    || (value.upcoming_recipients > 0 && (upcoming === undefined || upcoming <= at || upcoming >= end))) return false;
  if (earliest !== undefined && earliest > at && (earliest < end ? upcoming !== earliest : value.upcoming_recipients !== 0)) return false;
  return deadlineControls(s, at);
}
function deadlineControls(s: Record<string, unknown>, at: bigint): boolean {
  const c = s.current;
  if (!record(c) || timestamp(c.observed_at) !== at || typeof c.enabled !== 'boolean' || typeof c.rate_limited !== 'boolean'
    || !count(c.revision) || !count(c.interval_millis) || !count(c.burst) || !count(c.available_tokens)
    || typeof s.paused !== 'boolean' || !count(s.legacy_rate_per_second) || s.legacy_rate_per_second > 100
    || !['closed', 'open', 'half_open', 'draining'].includes(s.circuit_state as string)) return false;
  if (c.interval_millis < 10 || c.interval_millis > 3_600_000 || c.burst < 1 || c.burst > 100
    || c.available_tokens > c.burst || c.rate_limited !== (c.enabled && c.available_tokens === 0)
    || (!c.enabled && c.available_tokens !== 0)) return false;
  if (c.rate_limited) {
    const next = timestamp(c.next_available_at);
    if (next === undefined || next <= at || next - at > BigInt(c.interval_millis) * 1_000_000n) return false;
  } else if (c.next_available_at !== undefined) return false;
  const scopes = s.capacity_blocking_scopes, constraints = s.constraints;
  const known = ['subscription_paused', 'legacy_rate_limit', 'circuit_open', 'circuit_half_open', 'circuit_draining',
    'capacity_consumer', 'capacity_app', 'capacity_account', 'active_leases', 'scheduled_retries'];
  return Array.isArray(scopes) && new Set(scopes).size === scopes.length && scopes.every(v => ['consumer', 'app', 'account'].includes(v))
    && Array.isArray(constraints) && new Set(constraints).size === constraints.length && constraints.every(v => known.includes(v));
}
