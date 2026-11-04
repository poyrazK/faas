// Explicit alternative to the version 2 unleased integration; see README.
import { eventAcknowledgmentWorkerFromHeaders, eventConsumerRequestFromHeaders,
  claimEventConsumerProcessing, inspectEventConsumerProcessing } from '@gregale/sdk-node/preview';
import { acceptAuthenticatedDelivery } from './consumer.mjs';

/** Authenticate/authorize first, as for the ordinary ingress. Commit protected original inputs before 202. */
export async function acceptAuthenticatedDeliveryForPreparation(headers, body, scope, queue) {
  return acceptAuthenticatedDelivery(headers, body, scope, {
    enqueueProtected: record => queue.enqueueProtected({ ...record, version: 3, mode: 'durable-intent-processing' }),
  });
}
/** New coordinated jobs select a distinct version explicitly; never relabel older jobs. */
export async function acceptAuthenticatedDeliveryForCoordinatedPreparation(headers, body, scope, queue) {
  return acceptAuthenticatedDelivery(headers, body, scope, {
    enqueueProtected: record => queue.enqueueProtected({ ...record, version: 4, mode: 'durable-intent-prepared' }),
  });
}
function ownedProcessingDelivery(job, scope, version, mode) {
  const record = job.record, signal = job.signal;
  const target = { app: scope.app, accountId: scope.accountId.toLowerCase(), appId: scope.appId.toLowerCase() };
  if (!(signal instanceof AbortSignal)) throw new TypeError('invalid owned processing job');
  signal.throwIfAborted();
  if (record.version !== version || record.mode !== mode
    || record.owner?.app !== target.app || record.owner?.accountId !== target.accountId || record.owner?.appId !== target.appId
    || typeof record.bodyBase64 !== 'string' || record.bodyBase64.length > 1398104) throw new TypeError('invalid protected processing job');
  const original = Buffer.from(record.bodyBase64, 'base64');
  if (original.toString('base64') !== record.bodyBase64) throw new TypeError('invalid original body encoding');
  return { original, signal, target };
}
/** Preparation must be side-effect-free, bounded and observe signal. It owns serialized local renewals. */
export async function processOwnedPreparedDelivery(job, scope, pool, prepare, handler, leaseDurationMs = 30000) {
  if (typeof prepare !== 'function' || typeof handler !== 'function') throw new TypeError('invalid owned processing callbacks');
  const { original, signal, target } = ownedProcessingDelivery(job, scope, 3, 'durable-intent-processing');
  const worker = eventAcknowledgmentWorkerFromHeaders(target.app, job.record.headers, original, target.accountId, target.appId);
  const input = eventConsumerRequestFromHeaders(job.record.headers, original, target.accountId, target.appId);
  const lease = await claimEventConsumerProcessing(pool, input, leaseDurationMs, { signal });
  let prepared;
  if (!lease.completed) {
    const renew = () => lease.renew(pool, leaseDurationMs, { signal });
    await renew();
    prepared = await prepare(original, { signal, renew });
    signal.throwIfAborted();
    // prepare must join any renewal work before returning; no background renewal
    // may overlap the final transaction. This renew leaves a completion margin.
    await renew();
  }
  const result = await worker.runDurableWithProcessingLease(pool, lease,
    (tx, workSignal) => handler(tx, workSignal, original, prepared), { signal });
  signal.throwIfAborted();
  await job.ack(); // The durable intent host now owns platform callback delivery.
  return result;
}
/** The coordinator owns renewal; options.onEvent can use a bound metrics.record observer.
 * Preparation must join its work and observe signal. */
export async function processOwnedCoordinatedDelivery(job, scope, pool, prepare, handler, options) {
  if (typeof prepare !== 'function' || typeof handler !== 'function') throw new TypeError('invalid owned processing callbacks');
  const { original, signal, target } = ownedProcessingDelivery(job, scope, 4, 'durable-intent-prepared');
  const worker = eventAcknowledgmentWorkerFromHeaders(target.app, job.record.headers, original, target.accountId, target.appId);
  const result = await worker.runPreparedDurable(pool,
    workSignal => prepare(original, { signal: workSignal }),
    (tx, workSignal, prepared) => handler(tx, workSignal, original, prepared), { ...options, signal });
  signal.throwIfAborted();
  await job.ack(); // Intent delivery is independent; keep the job after any uncertain result.
  return result;
}
/** Observe only: no callback, claim, job acknowledgment or inferred recovery decision. */
export async function inspectOwnedProcessingDelivery(job, scope, pool) {
  const version = job.record.version;
  const mode = version === 3 ? 'durable-intent-processing' : version === 4 ? 'durable-intent-prepared' : undefined;
  if (mode === undefined) throw new TypeError('unsupported processing job version');
  const { original, signal, target } = ownedProcessingDelivery(job, scope, version, mode);
  const input = eventConsumerRequestFromHeaders(job.record.headers, original, target.accountId, target.appId);
  return inspectEventConsumerProcessing(pool, input, { signal });
}
