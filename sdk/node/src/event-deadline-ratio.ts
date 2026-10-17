import type { EventDeadlineRule } from './event-deadline-policy.js';

export const EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX = 10_000;
/** Inclusive integer basis points: 100 = 1%; omit recovery for warning minus one. */
export interface EventDeadlineRatioOptions {
  warningBasisPoints: number;
  recoveryBasisPoints?: number;
  minimumRecipients: number;
}
export interface EventDeadlineRatioSample {
  expiredRecipients: number;
  deadlineRecipients: number;
}
function safeCount(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}
export function deadlineRatioRule(value: unknown, withinMs: number): EventDeadlineRule {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new TypeError('invalid expired ratio options');
  const o = value as Record<string, unknown>;
  if (!safeCount(o.warningBasisPoints) || o.warningBasisPoints < 1 || o.warningBasisPoints > EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX
    || !safeCount(o.minimumRecipients) || o.minimumRecipients < 1
    || (o.recoveryBasisPoints !== undefined && (!safeCount(o.recoveryBasisPoints) || o.recoveryBasisPoints >= o.warningBasisPoints))) {
    throw new TypeError('ratio requires integer warning 1..10000, lower nonnegative recovery and a positive safe-integer minimum cohort');
  }
  return { metric: `deadline_window.expired_ratio_basis_points.${withinMs}.minimum_recipients.${o.minimumRecipients}`,
    cohort: 'expired', threshold: o.warningBasisPoints,
    recoveryThreshold: o.recoveryBasisPoints === undefined ? o.warningBasisPoints - 1 : o.recoveryBasisPoints as number,
    minimumRecipients: o.minimumRecipients };
}
export function validDeadlineRatioSample(rule: EventDeadlineRule, sample: unknown): sample is EventDeadlineRatioSample {
  if (!sample || typeof sample !== 'object' || Array.isArray(sample) || !safeCount(rule.minimumRecipients) || rule.minimumRecipients < 1) return false;
  const s = sample as Record<string, unknown>;
  return safeCount(s.expiredRecipients) && safeCount(s.deadlineRecipients) && s.deadlineRecipients >= rule.minimumRecipients
    && s.expiredRecipients <= s.deadlineRecipients;
}
/** Approximate basis points for display only; decisions use the exact sample. */
export function deadlineRatioValue(sample: EventDeadlineRatioSample): number {
  const scaled = BigInt(sample.expiredRecipients) * BigInt(EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX);
  const total = BigInt(sample.deadlineRecipients);
  return Number(scaled / total) + Number(scaled % total) / Number(total);
}
export function validDeadlineValue(rule: EventDeadlineRule, value: unknown, sample: unknown): boolean {
  if (rule.minimumRecipients === undefined) return safeCount(value) && sample === undefined;
  return validDeadlineRatioSample(rule, sample) && typeof value === 'number' && Number.isFinite(value)
    && value >= 0 && value <= EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX && value === deadlineRatioValue(sample);
}
export function deadlineBoundaries(c: EventDeadlineRule & { value?: number; ratioSample?: EventDeadlineRatioSample }):
  { breached: boolean; recovered: boolean } {
  if (c.value === undefined) return { breached: false, recovered: false };
  if (c.minimumRecipients === undefined) return { breached: c.value >= c.threshold, recovered: c.value <= c.recoveryThreshold };
  if (!validDeadlineRatioSample(c, c.ratioSample)) return { breached: false, recovered: false };
  const scaled = BigInt(c.ratioSample.expiredRecipients) * BigInt(EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX);
  const total = BigInt(c.ratioSample.deadlineRecipients);
  return { breached: scaled >= BigInt(c.threshold) * total, recovered: scaled <= BigInt(c.recoveryThreshold) * total };
}
