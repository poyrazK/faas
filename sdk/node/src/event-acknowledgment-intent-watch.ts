import { performance } from 'node:perf_hooks';
import type { EventConsumerPool } from './event-consumer-transactions.js';
import { acknowledgmentWait } from './event-acknowledgment-callbacks.js';
import { operationSignal } from './event-acknowledgment-protocol.js';
import { intentPool, intentScope } from './event-acknowledgment-intent-store.js';
import { getEventAcknowledgmentIntentHealth, type EventAcknowledgmentIntentHealth } from './event-acknowledgment-intent-health.js';
import { intentWatchEvidence, intentWatchPolicy, IntentWatchCondition, EventAcknowledgmentIntentWatchCallbackError,
  type IntentWatchPolicy, type EventAcknowledgmentIntentAlert, type EventAcknowledgmentIntentWatchOptions } from './event-acknowledgment-intent-watch-policy.js';
export { EventAcknowledgmentIntentWatchCallbackError, type EventAcknowledgmentIntentWatchOptions,
  type EventAcknowledgmentIntentWatchMetric, type EventAcknowledgmentIntentAlert,
  type EventAcknowledgmentIntentAlertChange, type EventAcknowledgmentIntentAlertReason } from './event-acknowledgment-intent-watch-policy.js';

export async function readIntentWatchHealth(pool: EventConsumerPool, account: string, app: string, policy: IntentWatchPolicy,
  signal: AbortSignal): Promise<EventAcknowledgmentIntentHealth | undefined> {
  const controller = new AbortController(), abort = () => controller.abort();
  const started = performance.now();
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) abort();
  const timer = setTimeout(abort, policy.queryTimeoutMs);
  try {
    // Await the actual operation: do not race an active SQL query, release its
    // connection early or start a second poll while the driver is still busy.
    const health = await getEventAcknowledgmentIntentHealth(pool, account, app, { ...policy.health, signal: controller.signal });
    if (controller.signal.aborted || performance.now() - started >= policy.queryTimeoutMs) return undefined;
    return health;
  } catch { return undefined; }
  finally { clearTimeout(timer); signal.removeEventListener('abort', abort); controller.abort(); }
}

/** Explicit in-process watch; snapshots contain metadata only. Polls and local
 * callbacks run sequentially. Driver timeouts must bound active database calls.
 * Resolves no incidents on cancellation; rejects with the signal's abort reason. */
export async function watchEventAcknowledgmentIntentHealth(pool: EventConsumerPool, accountId: string, appId: string,
  options: EventAcknowledgmentIntentWatchOptions,
  notify: (alert: EventAcknowledgmentIntentAlert, signal: AbortSignal) => void | Promise<void>): Promise<never> {
  const parent = operationSignal(options), [account, app] = intentScope(accountId, appId);
  intentPool(pool);
  if (typeof notify !== 'function') throw new TypeError('invalid acknowledgment intent watch callback');
  // Snapshot every option before the first yield. No caller mutation can retarget a running watch.
  const policy = intentWatchPolicy(options), conditions: IntentWatchCondition[] = [];
  if (policy.dueThreshold > 0) conditions.push(new IntentWatchCondition('due_backlog', policy.dueThreshold));
  if (policy.stalledThreshold > 0) conditions.push(new IntentWatchCondition('stalled_recovery', policy.stalledThreshold));
  if (policy.parkedThreshold !== undefined) conditions.push(new IntentWatchCondition('parked_intents', policy.parkedThreshold));
  const controller = new AbortController(), signal = controller.signal, abort = () => controller.abort(parent?.reason);
  parent?.addEventListener('abort', abort, { once: true });
  if (parent?.aborted) abort();
  let previousObserved: number | undefined, previousPoll: number | undefined;
  try {
    for (;;) {
      signal.throwIfAborted();
      const health = await readIntentWatchHealth(pool, account, app, policy, signal);
      signal.throwIfAborted();
      const wall = Date.now(), local = performance.now();
      const reason = intentWatchEvidence(health, previousObserved, previousPoll, wall, local, policy.maxAgeMs);
      previousPoll = local;
      const observed = health === undefined ? undefined : Date.parse(health.observedAt);
      // Retain a high-water mark even for incomplete reads, as the Go watch does.
      if (observed !== undefined && Number.isFinite(observed) && observed <= wall && wall - observed <= policy.maxAgeMs
        && (previousObserved === undefined || observed > previousObserved)) previousObserved = observed;
      const alerts: EventAcknowledgmentIntentAlert[] = [];
      for (const condition of conditions) {
        const value = health === undefined ? 0 : condition.metric === 'due_backlog' ? health.due
          : condition.metric === 'stalled_recovery' ? health.stalled : health.parked;
        const transition = condition.update(value, reason, local, policy.sustainMs);
        if (transition) alerts.push(Object.freeze({ accountId: account, appId: app, metric: condition.metric, ...transition,
          at: new Date(wall).toISOString(), value, threshold: condition.threshold, ...(health === undefined ? {} : { health }) }));
      }
      for (const alert of alerts) {
        signal.throwIfAborted();
        try { await notify(alert, signal); }
        catch { signal.throwIfAborted(); throw new EventAcknowledgmentIntentWatchCallbackError(alert); }
        signal.throwIfAborted();
      }
      await acknowledgmentWait(policy.pollIntervalMs, signal);
    }
  } finally { parent?.removeEventListener('abort', abort); controller.abort(); }
}
