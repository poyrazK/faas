// Integration functions for an application-owned durable queue. See README for
// the adapter's persistence and exclusive-ownership requirements.
import { randomUUID } from 'node:crypto';
import { eventAcknowledgmentWorkerFromHeaders } from '@gregale/sdk-node';

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
    id: randomUUID(), version: 1, phase: 'process', owner: target,
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
  if (record.version !== 1 || !['process', 'reconcile'].includes(record.phase)
    || record.owner?.app !== target.app || record.owner?.accountId !== target.accountId || record.owner?.appId !== target.appId
    || typeof record.bodyBase64 !== 'string' || record.bodyBase64.length > 1_398_104) throw new TypeError('invalid protected delivery job');
  const original = Buffer.from(record.bodyBase64, 'base64');
  if (original.toString('base64') !== record.bodyBase64) throw new TypeError('invalid protected original body encoding');
  const worker = eventAcknowledgmentWorkerFromHeaders(target.app, record.headers, original, target.accountId, target.appId);
  const phase = record.phase;
  let result;
  if (phase === 'process') {
    if (typeof handler !== 'function') throw new TypeError('business handler required');
    // Commit this phase BEFORE work. A crash from here onward selects receipt-only
    // reconciliation, including a lost business COMMIT or completion response.
    await job.savePhase('reconcile');
    signal.throwIfAborted();
    result = await worker.run(pool, (tx, workSignal) => handler(tx, workSignal, original), { signal });
  } else {
    result = await worker.completeReceipt(pool, { signal });
  }
  signal.throwIfAborted();
  await job.ack();
  return result;
}
