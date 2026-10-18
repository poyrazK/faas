import { EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX, type EventAcknowledgmentIntentCheckpoint,
  type EventAcknowledgmentIntentCheckpointCondition, type EventAcknowledgmentIntentNotification } from './event-acknowledgment-intent-checkpoint.js';
import type { EventAcknowledgmentIntentHealth } from './event-acknowledgment-intent-health.js';
import type { IntentWatchLoss } from './event-acknowledgment-intent-watch-policy.js';

export interface EventAcknowledgmentIntentConfirmation {
  metric: string;
  phase: 'unknown' | 'clear' | 'firing' | 'resume_pending' | 'warning_pending' | 'recovery_pending' | 'notification_blocked';
  reason: string;
  freshSamples: number;
  requiredSamples: 2;
  elapsedMs: number;
  requiredMs: number;
  remainingMs: number;
}
export interface IntentCheckpointProof { first?: number; samples: number; mode?: 'warning' | 'recovery'; resume: boolean }
function reset(p: IntentCheckpointProof): void { p.first = undefined; p.samples = 0; p.mode = undefined; }
export function resetIntentCheckpointProofs(checkpoint: EventAcknowledgmentIntentCheckpoint): IntentCheckpointProof[] {
  return checkpoint.conditions.map(c => { c.state = 'unknown'; return { samples: 0, resume: c.incidentId !== undefined }; });
}
export function intentCheckpointConfirmation(c: EventAcknowledgmentIntentCheckpointCondition, p: IntentCheckpointProof,
  local: number, sustain: number, reason: string, blocked = false): EventAcknowledgmentIntentConfirmation {
  const elapsedMs = p.first === undefined ? 0 : Math.max(0, local - p.first);
  let phase: EventAcknowledgmentIntentConfirmation['phase'] = p.resume ? 'resume_pending' : c.state;
  if (p.first !== undefined) phase = blocked ? 'notification_blocked' : p.mode === 'recovery' ? 'recovery_pending' : 'warning_pending';
  return { metric: c.metric, phase, reason, freshSamples: p.samples, requiredSamples: 2,
    elapsedMs, requiredMs: sustain, remainingMs: p.first === undefined ? 0 : Math.max(0, sustain - elapsedMs) };
}
function notification(checkpoint: EventAcknowledgmentIntentCheckpoint, c: EventAcknowledgmentIntentCheckpointCondition,
  transition: 'opened' | 'resolved', at: string): EventAcknowledgmentIntentNotification {
  checkpoint.sequence++;
  if (transition === 'opened') c.incidentId = checkpoint.session + '-' + checkpoint.sequence;
  const out = { sequence: checkpoint.sequence, incidentId: c.incidentId!, metric: c.metric,
    transition, at, value: c.value!, threshold: c.threshold };
  checkpoint.pending.push(out);
  return out;
}
function apply(checkpoint: EventAcknowledgmentIntentCheckpoint, c: EventAcknowledgmentIntentCheckpointCondition,
  p: IntentCheckpointProof, local: number, at: string): { reason: string; blocked?: boolean; alert?: EventAcknowledgmentIntentNotification } {
  const active = c.incidentId !== undefined, breach = c.value! >= c.threshold;
  const candidate = active ? p.resume || !breach : breach;
  if (!candidate) {
    reset(p); c.state = active ? 'firing' : 'clear';
    return { reason: active ? 'threshold_not_cleared' : 'threshold_not_breached' };
  }
  const mode = active && !breach ? 'recovery' : 'warning';
  if (mode !== p.mode) reset(p);
  p.mode = mode; p.first ??= local; p.samples = Math.min(2, p.samples + 1);
  c.state = active && !p.resume ? 'firing' : 'unknown';
  if (p.samples < 2 || local - p.first < checkpoint.policy.sustainMs) return { reason: mode + '_confirmation_pending' };
  if (!active) {
    const reserved = checkpoint.conditions.filter(v => v.incidentId !== undefined).length;
    if (checkpoint.pending.length + reserved + 2 > EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX) return { reason: 'notification_outbox_full', blocked: true };
    if (checkpoint.sequence > Number.MAX_SAFE_INTEGER - reserved - 2) return { reason: 'notification_sequence_exhausted', blocked: true };
    const alert = notification(checkpoint, c, 'opened', at);
    c.state = 'firing'; p.resume = false; reset(p);
    return { reason: 'threshold_not_cleared', alert };
  }
  if (!breach) {
    // Each active incident reserved one queue/sequence slot when it opened.
    const alert = notification(checkpoint, c, 'resolved', at);
    delete c.incidentId; c.state = 'clear'; p.resume = false; reset(p);
    return { reason: 'threshold_not_breached', alert };
  }
  c.state = 'firing'; p.resume = false; reset(p);
  return { reason: 'incident_reconfirmed' };
}
export function observeIntentCheckpoint(checkpoint: EventAcknowledgmentIntentCheckpoint, proofs: IntentCheckpointProof[],
  health: EventAcknowledgmentIntentHealth | undefined, reason: IntentWatchLoss | undefined, local: number, at: string):
  { alerts: EventAcknowledgmentIntentNotification[]; confirmations: EventAcknowledgmentIntentConfirmation[] } {
  const alerts: EventAcknowledgmentIntentNotification[] = [], confirmations: EventAcknowledgmentIntentConfirmation[] = [];
  for (const [i, c] of checkpoint.conditions.entries()) {
    const p = proofs[i]!;
    if (reason !== undefined) {
      reset(p); c.state = 'unknown'; p.resume = c.incidentId !== undefined;
      confirmations.push(intentCheckpointConfirmation(c, p, local, checkpoint.policy.sustainMs, reason));
      continue;
    }
    c.value = c.metric === 'due_backlog' ? health!.due : c.metric === 'stalled_recovery' ? health!.stalled : health!.parked;
    c.observedAt = health!.observedAt;
    const update = apply(checkpoint, c, p, local, at);
    if (update.alert) alerts.push(update.alert);
    confirmations.push(intentCheckpointConfirmation(c, p, local, checkpoint.policy.sustainMs, update.reason, update.blocked));
  }
  return { alerts, confirmations };
}
