// Integration functions for an application-owned durable queue. See README for
// the adapter's persistence and exclusive-ownership requirements.
import { randomUUID } from 'node:crypto';
import { eventAcknowledgmentWorkerFromHeaders, recoverNextEventAcknowledgmentIntent, recoverEventAcknowledgmentIntents, getEventAcknowledgmentIntentHealth, watchEventAcknowledgmentIntentHealth, watchEventAcknowledgmentIntentHealthWithCheckpoint, EventAcknowledgmentIntentError } from '@gregale/sdk-node/preview';

const names = new Set([
  'x-gregale-event-source', 'x-gregale-event-id', 'x-gregale-event-subscription-id',
  'x-gregale-event-acceptance-id', 'x-gregale-event-acknowledgment-token',
  'x-gregale-event-acknowledgment-lease-seconds', 'x-faas-invocation-id',
  'x-faas-invocation-source', 'x-faas-invocation-attempt',
  'x-faas-invocation-replay-generation', 'x-faas-invocation-replayed-from',
  'x-faas-invocation-replay-root',
]);

function owner(scope) {
  return Object.freeze({ app: scope.app, accountId: scope.accountId.toLowerCase(), appId: scope.appId.toLowerCase() });
}
function deliveryHeaders(headers) {
  // Preserve case aliases for the SDK to reject, rather than collapsing them.
  return Object.fromEntries(Object.entries(headers).filter(([name]) => names.has(name.toLowerCase()))
    .map(([name, value]) => [name, Array.isArray(value) ? [...value] : value]));
}

/** Call only AFTER the hosting route authenticated and authorized the delivery.
 * enqueueProtected must commit protected original inputs before it resolves. */
export async function acceptAuthenticatedDelivery(headers, body, scope, queue) {
  if (!(body instanceof Uint8Array)) throw new TypeError('original body bytes required');
  const original = Buffer.from(body), savedHeaders = deliveryHeaders(headers), target = owner(scope);
  eventAcknowledgmentWorkerFromHeaders(target.app, savedHeaders, original, target.accountId, target.appId);
  await queue.enqueueProtected({
    id: randomUUID(), version: 2, mode: 'durable-intent', owner: target,
    headers: savedHeaders, bodyBase64: original.toString('base64'),
  });
  return { statusCode: 202, body: '{"accepted":true}' };
}

/** job is an exclusively owned, protected queue record, not an HTTP request.
 * The adapter aborts signal on ownership loss and fences state updates/ack. */
export async function processOwnedDelivery(job, scope, pool, handler) {
  const record = job.record, target = owner(scope), signal = job.signal;
  if (!(signal instanceof AbortSignal)) throw new TypeError('owned job signal required');
  signal.throwIfAborted();
  if (record.version !== 2 || record.mode !== 'durable-intent'
    || record.owner?.app !== target.app || record.owner?.accountId !== target.accountId || record.owner?.appId !== target.appId
    || typeof record.bodyBase64 !== 'string' || record.bodyBase64.length > 1_398_104) throw new TypeError('invalid protected delivery job');
  const original = Buffer.from(record.bodyBase64, 'base64');
  if (original.toString('base64') !== record.bodyBase64) throw new TypeError('invalid protected original body encoding');
  const worker = eventAcknowledgmentWorkerFromHeaders(target.app, record.headers, original, target.accountId, target.appId);
  if (typeof handler !== 'function') throw new TypeError('business handler required');
  // The receipt and intent serialize recovery with an uncertain earlier COMMIT.
  // A saved receipt skips the handler; only transactional writes belong in it.
  const result = await worker.runDurable(pool, (tx, workSignal) => handler(tx, workSignal, original), { signal });
  signal.throwIfAborted();
  // Local commit transfers callback responsibility to the durable intent host.
  await job.ack();
  return result;
}

/** Run from a durable recovery host, independently of the ingestion queue. */
export async function recoverOneAcknowledgment(scope, pool, signal) {
  const target = owner(scope);
  try {
    return await recoverNextEventAcknowledgmentIntent(pool, target.accountId, target.appId, {
      signal, claimDurationMs: 30000, backoffMs: 1000,
    });
  } catch (error) {
    if (error instanceof EventAcknowledgmentIntentError && error.code === 'event_acknowledgment_intent_empty') return undefined;
    throw error;
  }
}

/** One explicit finite callback recovery pass; the host schedules later invocations. */
export async function recoverAcknowledgmentsPass(scope, pool, signal) {
  const target = owner(scope);
  return recoverEventAcknowledgmentIntents(pool, target.accountId, target.appId, {
    signal, maxAttempts: 100, concurrency: 4,
    retryPolicy: { initialBackoffMs: 1000, maxBackoffMs: 300000 },
  });
}

/** A separate read-only observation. No queue job/intent is claimed or repaired. */
export async function readAcknowledgmentRecoveryHealth(scope, pool, signal) {
  const target = owner(scope);
  return getEventAcknowledgmentIntentHealth(pool, target.accountId, target.appId, {
    signal, scanLimit: 1000, stallAfterMs: 300000,
  });
}

/** Run explicitly from the monitoring host. notify is local and awaited. */
export async function watchAcknowledgmentRecovery(scope, pool, signal, notify) {
  const target = owner(scope);
  return watchEventAcknowledgmentIntentHealth(pool, target.accountId, target.appId, {
    signal, health: { scanLimit: 1000, stallAfterMs: 300000 },
    dueThreshold: 100, stalledThreshold: 1, parkedThreshold: 1, sustainMs: 30000,
  }, notify);
}

/** store and notify must provide the fenced persistence/idempotency contract in README. */
export async function watchAcknowledgmentRecoveryWithCheckpoint(scope, pool, signal, store, notify) {
  const target = owner(scope);
  const saved = await store.loadOwned(signal);
  const result = await watchEventAcknowledgmentIntentHealthWithCheckpoint(pool, target.accountId, target.appId, {
    signal, health: { scanLimit: 1000, stallAfterMs: 300000 },
    dueThreshold: 100, stalledThreshold: 1, parkedThreshold: 1, sustainMs: 30000, resume: saved ?? undefined,
    pollAcknowledgments: (checkpoint, callbackSignal) => store.listAcknowledgments(checkpoint, callbackSignal),
    onProgress: async (progress, callbackSignal) => {
      // Commit the checkpoint BEFORE any external notification attempt.
      await store.saveOwned(progress.checkpoint, callbackSignal);
      for (const notification of progress.checkpoint.pending) {
        callbackSignal.throwIfAborted();
        await notify(notification, callbackSignal); // Owner deduplicates by incidentId + sequence.
        await store.recordAcknowledgment({ incidentId: notification.incidentId, sequence: notification.sequence }, callbackSignal);
      }
    },
  });
  // Cancellation returns a final detached checkpoint. Use an independently
  // bounded shutdown context because the watch signal is already aborted.
  await store.saveOwnedOnShutdown(result.checkpoint);
  return result;
}
