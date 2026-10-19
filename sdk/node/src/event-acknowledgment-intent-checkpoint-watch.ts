import { randomUUID } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { inspect } from 'node:util';
import type { EventConsumerPool } from './event-consumer-transactions.js';
import { acknowledgmentWait } from './event-acknowledgment-callbacks.js';
import { intentPool, intentScope } from './event-acknowledgment-intent-store.js';
import { intentWatchEvidence, intentWatchPolicy, type EventAcknowledgmentIntentWatchOptions } from './event-acknowledgment-intent-watch-policy.js';
import { readIntentWatchHealth } from './event-acknowledgment-intent-watch.js';
import { validateEventAcknowledgmentIntentCheckpoint, acknowledgeEventAcknowledgmentIntentAlerts, intentCheckpointRules, intentCheckpointVersion,
  type EventAcknowledgmentIntentCheckpoint, type EventAcknowledgmentIntentNotification,
  type EventAcknowledgmentIntentAcknowledgment } from './event-acknowledgment-intent-checkpoint.js';
import { observeIntentCheckpoint, resetIntentCheckpointProofs, intentCheckpointConfirmation,
  type EventAcknowledgmentIntentConfirmation } from './event-acknowledgment-intent-checkpoint-state.js';
import type { EventAcknowledgmentIntentHealth } from './event-acknowledgment-intent-health.js';

export interface EventAcknowledgmentIntentCheckpointWatchResult {
  stopReason: 'watching' | 'interrupted' | 'callback_failure';
  observations: number;
  evidenceStatus: 'unknown' | 'eligible';
  evidenceReason: string;
  health?: EventAcknowledgmentIntentHealth;
  checkpoint: EventAcknowledgmentIntentCheckpoint;
  /** New lifecycle notifications only. Replay checkpoint.pending for recovery. */
  alerts: EventAcknowledgmentIntentNotification[];
  confirmations: EventAcknowledgmentIntentConfirmation[];
}
export interface EventAcknowledgmentIntentCheckpointWatchOptions extends EventAcknowledgmentIntentWatchOptions {
  resume?: unknown;
  /** Persist atomically before sending pending notifications. Snapshots are detached. */
  onProgress: (progress: EventAcknowledgmentIntentCheckpointWatchResult, signal: AbortSignal) => void | Promise<void>;
  /** Exact acknowledgments only; never edit live checkpoint storage concurrently. */
  pollAcknowledgments?: (checkpoint: EventAcknowledgmentIntentCheckpoint, signal: AbortSignal) =>
    readonly EventAcknowledgmentIntentAcknowledgment[] | Promise<readonly EventAcknowledgmentIntentAcknowledgment[]>;
}
/** Carries a detached metadata result. Raw callback causes are deliberately omitted. */
export class EventAcknowledgmentIntentCheckpointWatchCallbackError extends Error {
  readonly code = 'event_acknowledgment_intent_checkpoint_callback_failed';
  readonly result: EventAcknowledgmentIntentCheckpointWatchResult;
  constructor(readonly phase: 'progress' | 'acknowledgments', result: EventAcknowledgmentIntentCheckpointWatchResult) {
    super('checkpointed recovery watch callback failed; persist result.checkpoint before notification retry');
    this.name = 'EventAcknowledgmentIntentCheckpointWatchCallbackError'; this.result = structuredClone(result);
  }
  toJSON(): object { return { name: this.name, code: this.code, phase: this.phase, pendingNotifications: this.result.checkpoint.pending.length }; }
  [inspect.custom](): object { return this.toJSON(); }
}
/** Explicit checkpoint mode. Application storage must fence one writer and commit
 * progress.checkpoint before any notification. No tables or transport are installed. */
export async function watchEventAcknowledgmentIntentHealthWithCheckpoint(pool: EventConsumerPool, accountId: string, appId: string,
  options: EventAcknowledgmentIntentCheckpointWatchOptions): Promise<EventAcknowledgmentIntentCheckpointWatchResult> {
  if (!options || typeof options !== 'object' || Array.isArray(options) || typeof options.onProgress !== 'function'
    || options.pollAcknowledgments !== undefined && typeof options.pollAcknowledgments !== 'function'
    || options.signal !== undefined && !(options.signal instanceof AbortSignal)) throw new TypeError('invalid checkpointed intent watch options');
  intentPool(pool);
  const [account, app] = intentScope(accountId, appId), policy = intentWatchPolicy(options);
  const parent = options.signal, onProgress = options.onProgress, pollAcknowledgments = options.pollAcknowledgments;
  let checkpoint: EventAcknowledgmentIntentCheckpoint = options.resume === undefined ? {
    kind: 'gregale.node.event-acknowledgment-intent-watch', version: intentCheckpointVersion(policy), target: { accountId: account, appId: app },
    policy, session: randomUUID(), sequence: 0, conditions: intentCheckpointRules(policy).map(rule => ({ ...rule, state: 'unknown' })),
    pending: [], acknowledged: [],
  } : validateEventAcknowledgmentIntentCheckpoint(options.resume);
  if (checkpoint.target.accountId !== account || checkpoint.target.appId !== app || JSON.stringify(checkpoint.policy) !== JSON.stringify(policy)) {
    throw new TypeError('intent checkpoint owner or effective policy mismatch');
  }
  let proofs = resetIntentCheckpointProofs(checkpoint);
  const result: EventAcknowledgmentIntentCheckpointWatchResult = { stopReason: 'watching', observations: 0,
    evidenceStatus: 'unknown', evidenceReason: 'awaiting_observation', checkpoint, alerts: [],
    confirmations: checkpoint.conditions.map((c, i) => intentCheckpointConfirmation(c, proofs[i]!, 0, policy.sustainMs, 'fresh_confirmation_required')) };
  const controller = new AbortController(), signal = controller.signal, abort = () => controller.abort(parent?.reason);
  parent?.addEventListener('abort', abort, { once: true });
  if (parent?.aborted) abort();
  let previousObserved = checkpoint.lastObservedAt === undefined ? undefined : Date.parse(checkpoint.lastObservedAt);
  let previousPoll: number | undefined;
  const callbackFailure = (phase: 'progress' | 'acknowledgments'): never => {
    signal.throwIfAborted(); result.stopReason = 'callback_failure';
    throw new EventAcknowledgmentIntentCheckpointWatchCallbackError(phase, result);
  };
  const acknowledge = async () => {
    if (!pollAcknowledgments) return;
    try {
      const acks = await pollAcknowledgments(structuredClone(checkpoint), signal);
      signal.throwIfAborted();
      checkpoint = acknowledgeEventAcknowledgmentIntentAlerts(checkpoint, acks); result.checkpoint = checkpoint;
    } catch { callbackFailure('acknowledgments'); }
  };
  const publish = async () => {
    signal.throwIfAborted();
    try { await onProgress(structuredClone(result), signal); }
    catch { callbackFailure('progress'); }
    signal.throwIfAborted();
  };
  try {
    signal.throwIfAborted();
    await acknowledge();
    await publish(); // Replay saved pending notifications before the first database read.
    for (;;) {
      signal.throwIfAborted();
      const health = await readIntentWatchHealth(pool, account, app, policy, signal);
      signal.throwIfAborted();
      const wall = Date.now(), local = performance.now();
      const reason = intentWatchEvidence(health, previousObserved, previousPoll, wall, local, policy.maxAgeMs);
      previousPoll = local;
      const observed = health === undefined ? undefined : Date.parse(health.observedAt);
      if (observed !== undefined && Number.isFinite(observed) && observed <= wall && wall - observed <= policy.maxAgeMs
        && (previousObserved === undefined || observed > previousObserved)) {
        previousObserved = observed; checkpoint.lastObservedAt = health!.observedAt;
      }
      result.observations++; result.evidenceStatus = reason === undefined ? 'eligible' : 'unknown';
      result.evidenceReason = reason ?? 'complete_retained_intents';
      if (health === undefined) delete result.health; else result.health = health;
      Object.assign(result, observeIntentCheckpoint(checkpoint, proofs, health, reason, local, new Date(wall).toISOString()));
      result.checkpoint = checkpoint;
      await publish();
      await acknowledgmentWait(policy.pollIntervalMs, signal);
      result.alerts = [];
      await acknowledge();
    }
  } catch (error) {
    if (!signal.aborted) throw error;
    result.stopReason = 'interrupted'; result.evidenceStatus = 'unknown'; result.evidenceReason = 'interrupted'; result.alerts = [];
    proofs = resetIntentCheckpointProofs(checkpoint);
    result.confirmations = checkpoint.conditions.map((c, i) => intentCheckpointConfirmation(c, proofs[i]!, 0, policy.sustainMs, 'interrupted'));
    return structuredClone(result);
  } finally { parent?.removeEventListener('abort', abort); controller.abort(); }
}
