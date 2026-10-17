// Explicit alternative to the version 2 unleased integration; see README.
import { eventAcknowledgmentWorkerFromHeaders, eventConsumerRequestFromHeaders,
  claimEventConsumerProcessing } from '@gregale/sdk-node';
import { acceptAuthenticatedDelivery } from './consumer.mjs';

/** Authenticate/authorize first, as for the ordinary ingress. Commit protected original inputs before 202. */
export async function acceptAuthenticatedDeliveryForPreparation(headers, body, scope, queue) {
  return acceptAuthenticatedDelivery(headers, body, scope, {
    enqueueProtected: record => queue.enqueueProtected({ ...record, version: 3, mode: 'durable-intent-processing' }),
  });
}
/** Preparation must be side-effect-free, bounded and observe signal. It owns serialized local renewals. */
export async function processOwnedPreparedDelivery(job, scope, pool, prepare, handler, leaseDurationMs = 30000) {
  const record = job.record, signal = job.signal;
  const target = { app: scope.app, accountId: scope.accountId.toLowerCase(), appId: scope.appId.toLowerCase() };
  if (!(signal instanceof AbortSignal) || typeof prepare !== 'function' || typeof handler !== 'function') throw new TypeError('invalid owned processing job');
  signal.throwIfAborted();
  if (record.version !== 3 || record.mode !== 'durable-intent-processing'
    || record.owner?.app !== target.app || record.owner?.accountId !== target.accountId || record.owner?.appId !== target.appId
    || typeof record.bodyBase64 !== 'string' || record.bodyBase64.length > 1398104) throw new TypeError('invalid protected processing job');
  const original = Buffer.from(record.bodyBase64, 'base64');
  if (original.toString('base64') !== record.bodyBase64) throw new TypeError('invalid original body encoding');
  const worker = eventAcknowledgmentWorkerFromHeaders(target.app, record.headers, original, target.accountId, target.appId);
  const input = eventConsumerRequestFromHeaders(record.headers, original, target.accountId, target.appId);
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
