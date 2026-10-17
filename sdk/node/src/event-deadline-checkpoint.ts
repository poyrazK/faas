import { deadlineBoundaries, validDeadlineValue, type EventDeadlineRatioSample } from './event-deadline-ratio.js';
import { count, deadlinePolicy, deadlineTarget, record, timestamp, uuid,
  type EventDeadlinePolicy, type EventDeadlineRule, type EventDeadlineTarget } from './event-deadline-policy.js';

export interface EventDeadlineCondition extends EventDeadlineRule {
  state: 'unknown' | 'clear' | 'firing';
  value?: number;
  ratioSample?: EventDeadlineRatioSample;
  observedAt?: string;
  incidentId?: string;
}
export interface EventDeadlineAlert {
  sequence: number;
  incidentId: string;
  metric: string;
  transition: 'opened' | 'resolved';
  at: string;
  value: number;
  threshold: number;
  recoveryThreshold: number;
  ratioSample?: EventDeadlineRatioSample;
}
export interface EventDeadlineAcknowledgement { incidentId: string; sequence: number }
/** Node deadline-only format; not interchangeable with Go/CLI delivery-health checkpoints. */
export interface EventDeadlineCheckpoint {
  kind: 'gregale.node.event-deadline-watch';
  version: 1 | 2;
  target: EventDeadlineTarget;
  policy: EventDeadlinePolicy;
  session: string;
  sequence: number;
  conditions: EventDeadlineCondition[];
  pending: EventDeadlineAlert[];
  acknowledged: EventDeadlineAlert[];
}
export const DEADLINE_QUEUE_MAX = 1024;
export function clone<T>(value: T): T { return structuredClone(value); }
function fail(): never { throw new TypeError('invalid Node deadline watch checkpoint'); }
function incidentSequence(value: unknown, session: string): number {
  if (typeof value !== 'string' || !value.startsWith(`${session}-`)) return fail();
  const raw = value.slice(session.length + 1), n = Number(raw);
  if (!count(n) || n === 0 || String(n) !== raw) return fail();
  return n;
}
function sameRule(a: Record<string, unknown>, b: EventDeadlineRule): boolean {
  return a.metric === b.metric && a.cohort === b.cohort && a.reason === b.reason
    && a.threshold === b.threshold && a.recoveryThreshold === b.recoveryThreshold && a.minimumRecipients === b.minimumRecipients;
}
/** Validate detached JSON before resuming or acknowledging any notification. */
export function validateEventDeadlineCheckpoint(input: unknown): EventDeadlineCheckpoint {
  if (!record(input) || input.kind !== 'gregale.node.event-deadline-watch' || (input.version !== 1 && input.version !== 2)
    || !uuid(input.session) || !count(input.sequence) || !record(input.target) || !record(input.policy)) return fail();
  const target = deadlineTarget(input.target as unknown as EventDeadlineTarget), p = input.policy;
  if (!durationPolicy(p) || !Array.isArray(p.rules) || p.rules.length === 0 || p.rules.length > (input.version === 1 ? 30 : 31)) return fail();
  // Rebuild through public option validation, including deterministic metric identities/order.
  const options: Parameters<typeof deadlinePolicy>[0] = { withinMs: p.withinMs as number,
    sustainMs: p.sustainMs as number, recoverySustainMs: p.recoverySustainMs as number,
    upcomingReasons: {}, expiredReasons: {}, upcomingReasonsRecovery: {}, expiredReasonsRecovery: {} };
  for (const rule of p.rules) {
    if (!record(rule) || !['upcoming', 'expired'].includes(rule.cohort as string) || !count(rule.threshold) || rule.threshold === 0
      || !count(rule.recoveryThreshold) || rule.recoveryThreshold >= rule.threshold) return fail();
    const cohort = rule.cohort as 'upcoming' | 'expired';
    if (rule.minimumRecipients !== undefined) {
      if (input.version !== 2 || cohort !== 'expired' || rule.reason !== undefined || options.expiredRatio !== undefined
        || !count(rule.minimumRecipients) || rule.minimumRecipients === 0) return fail();
      options.expiredRatio = { warningBasisPoints: rule.threshold, recoveryBasisPoints: rule.recoveryThreshold,
        minimumRecipients: rule.minimumRecipients };
    } else if (rule.reason === undefined) {
      if (options[cohort] !== undefined) return fail();
      options[cohort] = rule.threshold;
      options[cohort === 'upcoming' ? 'upcomingRecovery' : 'expiredRecovery'] = rule.recoveryThreshold;
    } else {
      if (typeof rule.reason !== 'string') return fail();
      const warnings = options[cohort === 'upcoming' ? 'upcomingReasons' : 'expiredReasons'] as Record<string, number>;
      const recoveries = options[cohort === 'upcoming' ? 'upcomingReasonsRecovery' : 'expiredReasonsRecovery'] as Record<string, number>;
      if (Object.hasOwn(warnings, rule.reason)) return fail();
      // Define own properties so untrusted keys cannot change object prototypes.
      Object.defineProperty(warnings, rule.reason, { value: rule.threshold, enumerable: true, configurable: true });
      Object.defineProperty(recoveries, rule.reason, { value: rule.recoveryThreshold, enumerable: true, configurable: true });
    }
  }
  const policy = deadlinePolicy(options);
  if ((input.version === 2) !== policy.rules.some(r => r.minimumRecipients !== undefined)) return fail();
  if (p.rules.length !== policy.rules.length || p.rules.some((r, i) => !sameRule(r, policy.rules[i]!))) return fail();
  if (!Array.isArray(input.conditions) || input.conditions.length !== policy.rules.length
    || !Array.isArray(input.pending) || input.pending.length > DEADLINE_QUEUE_MAX
    || !Array.isArray(input.acknowledged) || input.acknowledged.length > DEADLINE_QUEUE_MAX) return fail();
  const active = new Map<string, string>();
  for (const [i, c] of input.conditions.entries()) {
    if (!record(c) || !sameRule(c, policy.rules[i]!) || !['unknown', 'clear', 'firing'].includes(c.state as string)
      || (c.value !== undefined && !validDeadlineValue(policy.rules[i]!, c.value, c.ratioSample))
      || (c.value === undefined && c.ratioSample !== undefined) || (c.observedAt !== undefined && timestamp(c.observedAt) === undefined)
      || (c.value === undefined) !== (c.observedAt === undefined)) return fail();
    if (c.incidentId !== undefined) {
      if (incidentSequence(c.incidentId, input.session) > input.sequence || active.has(c.incidentId as string)
        || c.state === 'clear' || c.observedAt === undefined) return fail();
      active.set(c.incidentId as string, c.metric as string);
    } else if (c.state === 'firing') return fail();
  }
  if (input.pending.length + active.size > DEADLINE_QUEUE_MAX || input.sequence > Number.MAX_SAFE_INTEGER - active.size) return fail();
  const seen = new Set<number>(), lineage = new Map<string, { metric: string; opened?: number; resolved?: number }>();
  for (const list of [input.pending, input.acknowledged]) {
    let last = 0;
    for (const a of list) {
      if (!record(a) || !count(a.sequence) || a.sequence <= last || a.sequence > input.sequence || seen.has(a.sequence)
        || timestamp(a.at) === undefined || !['opened', 'resolved'].includes(a.transition as string)) return fail();
      const rule = policy.rules.find(r => r.metric === a.metric), opening = incidentSequence(a.incidentId, input.session);
      if (!rule || !validDeadlineValue(rule, a.value, a.ratioSample) || a.threshold !== rule.threshold
        || a.recoveryThreshold !== rule.recoveryThreshold || opening > a.sequence) return fail();
      const boundaries = deadlineBoundaries({ ...rule, value: a.value as number,
        ratioSample: a.ratioSample as EventDeadlineRatioSample | undefined });
      if (a.transition === 'opened' ? opening !== a.sequence || !boundaries.breached
        : opening === a.sequence || !boundaries.recovered) return fail();
      const id = a.incidentId as string, old = lineage.get(id) ?? { metric: rule.metric };
      if (old.metric !== rule.metric || (a.transition === 'opened' ? old.opened !== undefined : old.resolved !== undefined)) return fail();
      if (a.transition === 'opened') old.opened = a.sequence; else old.resolved = a.sequence;
      lineage.set(id, old);
      seen.add(a.sequence); last = a.sequence;
    }
  }
  for (const [id, row] of lineage) {
    if ((row.opened !== undefined && row.resolved !== undefined && row.resolved <= row.opened)
      || (row.resolved !== undefined && active.has(id)) || (active.has(id) && active.get(id) !== row.metric)
      || (row.opened !== undefined && row.resolved === undefined && !active.has(id))) return fail();
  }
  for (const id of [...lineage.keys(), ...active.keys()]) {
    const opening = incidentSequence(id, input.session);
    if (seen.has(opening) && lineage.get(id)?.opened !== opening) return fail();
  }
  // Reconstruct known fields; persisted timers and unknown payloads are never retained.
  const conditions = input.conditions.map((c, i): EventDeadlineCondition => ({ ...policy.rules[i]!,
    state: c.state, ...(c.value === undefined ? {} : { value: c.value, observedAt: c.observedAt }),
    ...(c.incidentId === undefined ? {} : { incidentId: c.incidentId }),
    ...(c.ratioSample === undefined ? {} : { ratioSample: { expiredRecipients: c.ratioSample.expiredRecipients,
      deadlineRecipients: c.ratioSample.deadlineRecipients } }) }));
  const alerts = (rows: Record<string, unknown>[]): EventDeadlineAlert[] => rows.map(a => ({
    sequence: a.sequence as number, incidentId: a.incidentId as string, metric: a.metric as string,
    transition: a.transition as EventDeadlineAlert['transition'], at: a.at as string,
    value: a.value as number, threshold: a.threshold as number, recoveryThreshold: a.recoveryThreshold as number,
    ...(a.ratioSample === undefined ? {} : { ratioSample: {
      expiredRecipients: (a.ratioSample as EventDeadlineRatioSample).expiredRecipients,
      deadlineRecipients: (a.ratioSample as EventDeadlineRatioSample).deadlineRecipients } }),
  }));
  return clone({ kind: 'gregale.node.event-deadline-watch', version: input.version, target, policy,
    session: input.session, sequence: input.sequence, conditions,
    pending: alerts(input.pending), acknowledged: alerts(input.acknowledged) } as EventDeadlineCheckpoint);
}
function durationPolicy(p: Record<string, unknown>): boolean {
  return ['withinMs', 'sustainMs', 'recoverySustainMs'].every(k => typeof p[k] === 'number'
    && Number.isInteger(p[k]) && (p[k] as number) >= 100 && (p[k] as number) <= 86_400_000);
}
/** All-or-nothing exact-pair acknowledgments; retains the latest 1024 receipts. */
export function acknowledgeEventDeadlineAlerts(input: unknown, acknowledgements: readonly EventDeadlineAcknowledgement[]): EventDeadlineCheckpoint {
  const out = validateEventDeadlineCheckpoint(input);
  if (!Array.isArray(acknowledgements) || acknowledgements.length > DEADLINE_QUEUE_MAX) throw new TypeError('invalid deadline acknowledgments');
  const keys = new Set<number>();
  for (const ack of acknowledgements) {
    if (!record(ack) || !count(ack.sequence) || keys.has(ack.sequence)
      || ![...out.pending, ...out.acknowledged].some(a => a.sequence === ack.sequence && a.incidentId === ack.incidentId)) {
      throw new TypeError('unknown, duplicate or mismatched deadline acknowledgment');
    }
    keys.add(ack.sequence);
  }
  for (const a of out.pending) {
    if (keys.has(a.sequence) && a.transition === 'resolved'
      && out.pending.some(open => open.incidentId === a.incidentId && open.transition === 'opened' && !keys.has(open.sequence))) {
      throw new TypeError('acknowledge opening before or together with resolution');
    }
  }
  out.acknowledged = [...out.acknowledged, ...out.pending.filter(a => keys.has(a.sequence))]
    .sort((a, b) => a.sequence - b.sequence).slice(-DEADLINE_QUEUE_MAX);
  out.pending = out.pending.filter(a => !keys.has(a.sequence));
  return out;
}
