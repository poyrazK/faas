import { intentWatchPolicy, type IntentWatchPolicy, type EventAcknowledgmentIntentWatchMetric } from './event-acknowledgment-intent-watch-policy.js';
import { intentScope } from './event-acknowledgment-intent-store.js';

export const EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX = 1024;
export interface EventAcknowledgmentIntentCheckpointCondition {
  metric: EventAcknowledgmentIntentWatchMetric;
  threshold: number;
  state: 'unknown' | 'clear' | 'firing';
  value?: number;
  observedAt?: string;
  incidentId?: string;
}
export interface EventAcknowledgmentIntentNotification {
  sequence: number;
  incidentId: string;
  metric: EventAcknowledgmentIntentWatchMetric;
  transition: 'opened' | 'resolved';
  at: string;
  value: number;
  threshold: number;
}
export interface EventAcknowledgmentIntentAcknowledgment { incidentId: string; sequence: number }
/** Application-persisted metadata. Version 2 requires parked alerts enabled; version 1 omits them.
 * Timers and unfinished confirmation proof are never serialized. */
export interface EventAcknowledgmentIntentCheckpoint {
  kind: 'gregale.node.event-acknowledgment-intent-watch';
  version: 1 | 2;
  target: { accountId: string; appId: string };
  policy: IntentWatchPolicy;
  session: string;
  sequence: number;
  lastObservedAt?: string;
  conditions: EventAcknowledgmentIntentCheckpointCondition[];
  pending: EventAcknowledgmentIntentNotification[];
  acknowledged: EventAcknowledgmentIntentNotification[];
}
function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}
function fail(): never { throw new TypeError('invalid Node acknowledgment intent watch checkpoint'); }
function integer(value: unknown): value is number { return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0; }
export function intentCheckpointTime(value: unknown): number {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/.test(value)) return fail();
  const date = new Date(value), ms = date.getTime();
  if (!Number.isFinite(ms) || date.getUTCFullYear() < 1 || date.toISOString() !== value) return fail();
  return ms;
}
function opening(value: unknown, session: string): number {
  if (typeof value !== 'string' || value.length > 53 || !value.startsWith(session + '-')) return fail();
  const raw = value.slice(session.length + 1), sequence = Number(raw);
  if (!integer(sequence) || sequence === 0 || String(sequence) !== raw) return fail();
  return sequence;
}
export function intentCheckpointRules(policy: IntentWatchPolicy): { metric: EventAcknowledgmentIntentWatchMetric; threshold: number }[] {
  const rules: { metric: EventAcknowledgmentIntentWatchMetric; threshold: number }[] = [];
  if (policy.dueThreshold > 0) rules.push({ metric: 'due_backlog', threshold: policy.dueThreshold });
  if (policy.stalledThreshold > 0) rules.push({ metric: 'stalled_recovery', threshold: policy.stalledThreshold });
  if (policy.parkedThreshold !== undefined) rules.push({ metric: 'parked_intents', threshold: policy.parkedThreshold });
  return rules;
}
export function intentCheckpointVersion(policy: IntentWatchPolicy): 1 | 2 {
  return policy.parkedThreshold === undefined ? 1 : 2;
}
function checkpointPolicy(input: unknown): IntentWatchPolicy {
  if (!record(input)) return fail();
  const health = input.health;
  if (!record(health) || !['scanLimit', 'stallAfterMs'].every(k => typeof health[k] === 'number')
    || !['dueThreshold', 'stalledThreshold', 'pollIntervalMs', 'queryTimeoutMs', 'maxAgeMs', 'sustainMs'].every(k => typeof input[k] === 'number')) return fail();
  return intentWatchPolicy({ health: { scanLimit: health.scanLimit as number, stallAfterMs: health.stallAfterMs as number },
    dueThreshold: input.dueThreshold as number, stalledThreshold: input.stalledThreshold as number,
    parkedThreshold: input.parkedThreshold as number | undefined,
    pollIntervalMs: input.pollIntervalMs as number, queryTimeoutMs: input.queryTimeoutMs as number,
    maxAgeMs: input.maxAgeMs as number, sustainMs: input.sustainMs as number });
}
/** Strict bounded validation and known-field reconstruction before resume or acknowledgment. */
export function validateEventAcknowledgmentIntentCheckpoint(input: unknown): EventAcknowledgmentIntentCheckpoint {
  try { return validate(input); } catch { return fail(); }
}
function validate(input: unknown): EventAcknowledgmentIntentCheckpoint {
  if (!record(input) || input.kind !== 'gregale.node.event-acknowledgment-intent-watch' || input.version !== 1 && input.version !== 2
    || !record(input.target) || typeof input.session !== 'string' || input.session.length !== 36 || input.session !== input.session.toLowerCase()
    || !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(input.session)
    || input.session === '00000000-0000-0000-0000-000000000000' || !integer(input.sequence)) return fail();
  const [accountId, appId] = intentScope(input.target.accountId as string, input.target.appId as string);
  const policy = checkpointPolicy(input.policy), rules = intentCheckpointRules(policy), session = input.session, sequence = input.sequence;
  if (input.version !== intentCheckpointVersion(policy)) return fail();
  const lastObserved = input.lastObservedAt === undefined ? undefined : intentCheckpointTime(input.lastObservedAt);
  if (sequence > 0 && lastObserved === undefined) return fail();
  if (!Array.isArray(input.conditions) || input.conditions.length !== rules.length || !Array.isArray(input.pending)
    || !Array.isArray(input.acknowledged) || input.pending.length > EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX
    || input.acknowledged.length > EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX) return fail();
  const active = new Map<string, EventAcknowledgmentIntentWatchMetric>();
  const conditions = input.conditions.map((c: unknown, i): EventAcknowledgmentIntentCheckpointCondition => {
    const rule = rules[i]!;
    if (!record(c) || c.metric !== rule.metric || c.threshold !== rule.threshold
      || !['unknown', 'clear', 'firing'].includes(c.state as string) || (c.value === undefined) !== (c.observedAt === undefined)) return fail();
    if (c.value !== undefined && (!integer(c.value) || c.value > policy.health.scanLimit || lastObserved === undefined
      || intentCheckpointTime(c.observedAt) > lastObserved)) return fail();
    if (c.incidentId !== undefined) {
      if (opening(c.incidentId, session) > sequence || active.has(c.incidentId as string) || c.state === 'clear' || c.value === undefined) return fail();
      active.set(c.incidentId as string, rule.metric);
    } else if (c.state === 'firing') return fail();
    return { ...rule, state: c.state as EventAcknowledgmentIntentCheckpointCondition['state'],
      ...(c.value === undefined ? {} : { value: c.value as number, observedAt: c.observedAt as string }),
      ...(c.incidentId === undefined ? {} : { incidentId: c.incidentId as string }) };
  });
  if (sequence > 0 && conditions.some(c => c.value === undefined)
    || input.pending.length + active.size > EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX || sequence > Number.MAX_SAFE_INTEGER - active.size) return fail();
  const seen = new Map<number, EventAcknowledgmentIntentNotification>();
  const lineage = new Map<string, { metric: EventAcknowledgmentIntentWatchMetric; opened?: number; resolved?: number; openingPending?: boolean; resolutionAcked?: boolean }>();
  const notifications = (rows: unknown[], pending: boolean): EventAcknowledgmentIntentNotification[] => {
    let last = 0;
    return rows.map(a => {
      if (!record(a) || !integer(a.sequence) || a.sequence <= last || a.sequence > sequence || seen.has(a.sequence)
        || !['opened', 'resolved'].includes(a.transition as string)) return fail();
      const rule = rules.find(r => r.metric === a.metric), first = opening(a.incidentId, session);
      intentCheckpointTime(a.at);
      if (!rule || !integer(a.value) || a.value > policy.health.scanLimit || a.threshold !== rule.threshold || first > a.sequence
        || (a.transition === 'opened' ? first !== a.sequence || a.value < rule.threshold : first === a.sequence || a.value >= rule.threshold)) return fail();
      const id = a.incidentId as string, old = lineage.get(id) ?? { metric: rule.metric };
      if (old.metric !== rule.metric || (a.transition === 'opened' ? old.opened !== undefined : old.resolved !== undefined)) return fail();
      if (a.transition === 'opened') { old.opened = a.sequence; old.openingPending = pending; }
      else { old.resolved = a.sequence; old.resolutionAcked = !pending; }
      lineage.set(id, old); last = a.sequence;
      const notification = { sequence: a.sequence, incidentId: id, metric: rule.metric,
        transition: a.transition as EventAcknowledgmentIntentNotification['transition'], at: a.at as string, value: a.value, threshold: rule.threshold };
      seen.set(a.sequence, notification);
      return notification;
    });
  };
  const pending = notifications(input.pending, true), acknowledged = notifications(input.acknowledged, false);
  for (const [id, row] of lineage) {
    if (row.opened !== undefined && row.resolved !== undefined && row.resolved <= row.opened
      || row.resolved !== undefined && active.has(id) || row.opened !== undefined && row.resolved === undefined && !active.has(id)
      || active.has(id) && active.get(id) !== row.metric || row.openingPending && row.resolutionAcked) return fail();
  }
  for (const id of new Set([...active.keys(), ...lineage.keys()])) {
    const first = seen.get(opening(id, session));
    if (first && (first.incidentId !== id || first.transition !== 'opened')) return fail();
  }
  return { kind: 'gregale.node.event-acknowledgment-intent-watch', version: intentCheckpointVersion(policy), target: { accountId, appId },
    policy, session, sequence, ...(lastObserved === undefined ? {} : { lastObservedAt: input.lastObservedAt as string }),
    conditions, pending, acknowledged };
}
/** All-or-nothing exact-pair acknowledgments. Keeps the latest 1024 acknowledgment receipts. */
export function acknowledgeEventAcknowledgmentIntentAlerts(input: unknown,
  acknowledgments: readonly EventAcknowledgmentIntentAcknowledgment[]): EventAcknowledgmentIntentCheckpoint {
  const out = validateEventAcknowledgmentIntentCheckpoint(input);
  if (!Array.isArray(acknowledgments) || acknowledgments.length > EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX) throw new TypeError('invalid intent alert acknowledgments');
  const keys = new Set<number>();
  for (const ack of acknowledgments) {
    if (!record(ack) || !integer(ack.sequence) || keys.has(ack.sequence)
      || ![...out.pending, ...out.acknowledged].some(a => a.sequence === ack.sequence && a.incidentId === ack.incidentId)) {
      throw new TypeError('unknown, duplicate or mismatched intent alert acknowledgment');
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
    .sort((a, b) => a.sequence - b.sequence).slice(-EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX);
  out.pending = out.pending.filter(a => !keys.has(a.sequence));
  return out;
}
