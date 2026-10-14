import { withRealtimeSettlementRetries } from './realtime-settlement-retries.js';
import { createRealtimeSettlementBatcher } from './realtime-settlement-batcher.js';
import type { RealtimeConsumerGroupBatchSettlementRequest } from './generated/models/RealtimeConsumerGroupBatchSettlementRequest.js';
import type { RealtimeConsumerGroupBatchSettlementResult } from './generated/models/RealtimeConsumerGroupBatchSettlementResult.js';
import { setMaxListeners } from 'node:events';
import type { RealtimeWorkerHeartbeat } from './generated/models/RealtimeWorkerHeartbeat.js';
import { ApiError } from './generated/core/ApiError.js';
import type { RealtimeConsumerGroupWorkerClient } from './realtime-worker-credentials.js';
import type { RealtimeConsumerGroupDrainState } from './generated/models/RealtimeConsumerGroupDrainState.js';
import type { RealtimeConsumerGroupBatch } from './generated/models/RealtimeConsumerGroupBatch.js';
import type { RealtimeConsumerGroup } from './generated/models/RealtimeConsumerGroup.js';
import type { RealtimeConsumerGroupSettlement } from './generated/models/RealtimeConsumerGroupSettlement.js';
import type { RealtimeConsumerGroupSettlementResult } from './generated/models/RealtimeConsumerGroupSettlementResult.js';
import { RealtimeService } from './generated/services/RealtimeService.js';
import type { RealtimeConsumerGroupClaim } from './generated/models/RealtimeConsumerGroupClaim.js';
import type { RealtimeConsumerGroupMessage } from './generated/models/RealtimeConsumerGroupMessage.js';
import type { CancelablePromise } from './generated/core/CancelablePromise.js';

export interface RealtimeConsumerGroupTarget {
  slug: string;
  id: string;
  channel: string;
  group: string;
}

export interface RealtimeConsumerGroupProcessingOptions {
  /** Opt-in readiness reporting every 15 seconds; scoped credentials need heartbeat permission. */
  heartbeat?: boolean;
  /** Release valid leases after handlers stop on caller shutdown. Requires release permission.
   * Default false. Cannot undo handler side effects; handlers must await all work. */
  releaseOnShutdown?: boolean;
  /** Opt-in receipt-backed retries for terminal single/batch settlements, 0–3. Default 0.
   * Retries transport errors and HTTP 408/500/502/503/504 with the same UUID key. */
  settlementRetries?: number;
  /** Opt-in complete/retry batching, 1–32. Default 1 uses single settlement. Renewals stay immediate. */
  settlementBatchSize?: number;
  /** Maximum batching delay, 0–25 ms. Default 10; leases keep renewing while queued. */
  settlementFlushMs?: number;
  signal?: AbortSignal;
  /** Long-poll wait, 0–20 seconds. Default 10; 0 restores immediate polling. */
  waitSeconds?: number;
  /** Maximum in-flight handlers, 1–32. Default 1; completion may be out of order. */
  concurrency?: number;
  /** Maximum claims per request, 1–16, never more than available handler slots. */
  batchSize?: number;
  /** Defaults to 1 second when no claim is ready. */
  pollIntervalMs?: number;
  /** Omit explicit delay and use the configured server retry policy. Default false. */
  useGroupRetryPolicy?: boolean;
  /** Explicit retry delay (0–300 seconds); defaults to 1 unless using the group policy. */
  retryAfterSeconds?: number;
  /** The signal aborts on shutdown or lost lease. Handlers must be idempotent. */
  onMessage: (message: RealtimeConsumerGroupMessage, context: {
    signal: AbortSignal;
    claim: RealtimeConsumerGroupClaim;
  }) => void | Promise<void>;
  /** Safe printable ASCII failure code, 1–64 bytes. Defaults to handler_failed.
   * Do not return exception messages or secrets. Called only after handler failure. */
  failureCode?: (error: unknown, message: RealtimeConsumerGroupMessage) => string;
  onHandlerError?: (error: unknown, message: RealtimeConsumerGroupMessage) => void | Promise<void>;
}

export interface RealtimeConsumerGroupWorkerOptions extends RealtimeConsumerGroupTarget, RealtimeConsumerGroupProcessingOptions {
  worker: string;
}

interface RealtimeGroupProcessingClient {
  heartbeat(worker?: string): CancelablePromise<RealtimeWorkerHeartbeat>;
  status(): CancelablePromise<RealtimeConsumerGroup>;
  claim(worker?: string, waitSeconds?: number): CancelablePromise<RealtimeConsumerGroupClaim>;
  claimBatch(limit: number, worker?: string, waitSeconds?: number): CancelablePromise<RealtimeConsumerGroupBatch>;
  settle(body: RealtimeConsumerGroupSettlement): CancelablePromise<RealtimeConsumerGroupSettlementResult>;
  settleBatch(body: RealtimeConsumerGroupBatchSettlementRequest): CancelablePromise<RealtimeConsumerGroupBatchSettlementResult>;
}

function waitForGroupWork(ms: number, signal?: AbortSignal): Promise<boolean> {
  if (signal?.aborted) return Promise.resolve(false);
  return new Promise((resolve) => {
    const done = (ready: boolean) => {
      clearTimeout(timer);
      signal?.removeEventListener('abort', aborted);
      resolve(ready);
    };
    const aborted = () => done(false);
    const timer = setTimeout(() => done(true), ms);
    signal?.addEventListener('abort', aborted, { once: true });
  });
}

async function groupRequest<T>(request: CancelablePromise<T>, signal?: AbortSignal): Promise<T> {
  const aborted = () => request.cancel();
  if (signal?.aborted) request.cancel();
  else signal?.addEventListener('abort', aborted, { once: true });
  try { return await request; }
  finally { signal?.removeEventListener('abort', aborted); }
}

async function processGroupClaim(
  client: RealtimeGroupProcessingClient,
  claim: RealtimeConsumerGroupClaim,
  leaseSeconds: number,
  options: RealtimeConsumerGroupProcessingOptions,
  shutdown: { signal?: AbortSignal; graceful: () => boolean; client: RealtimeGroupProcessingClient; onReleaseError: (error: unknown) => void },
): Promise<void> {
  const handler = new AbortController();
  const renewal = new AbortController();
  let leaseError: unknown;
  const deadlineWatch = new AbortController();
  const deadline = claim.delivery_deadline == null ? undefined : Date.parse(claim.delivery_deadline);
  const expire = () => {
    leaseError = new Error('consumer-group delivery deadline reached');
    handler.abort(leaseError);
    renewal.abort();
  };
  const watchDeadline = (async () => {
    if (deadline === undefined) return;
    if (!Number.isFinite(deadline)) { expire(); return; }
    while (!deadlineWatch.signal.aborted) {
      const remaining = deadline - Date.now();
      if (remaining <= 0) { expire(); return; }
      if (!await waitForGroupWork(Math.min(remaining, 60_000), deadlineWatch.signal)) return;
    }
  })();
  const stop = () => { handler.abort(options.signal?.reason); renewal.abort(); };
  options.signal?.addEventListener('abort', stop, { once: true });
  if (options.signal?.aborted) stop();
  const renew = (async () => {
    while (await waitForGroupWork(Math.max(100, Math.floor(leaseSeconds * 1000 / 3)), renewal.signal)) {
      try {
        await groupRequest(client.settle({ sequence: claim.message.sequence, claim_token: claim.claim_token, action: 'renew' }), renewal.signal);
      } catch (error) {
        if (!renewal.signal.aborted) { leaseError = error; handler.abort(error); }
        return;
      }
    }
  })();
  let failed = false;
  let handlerError: unknown;
  try {
    if (!handler.signal.aborted) {
      try { await options.onMessage(claim.message, { signal: handler.signal, claim }); }
      catch (error) { failed = true; handlerError = error; }
    }

    if ((options.settlementBatchSize ?? 1) === 1) {
      renewal.abort();
      deadlineWatch.abort();
      await Promise.all([renew, watchDeadline]);
    }
    // Awaiting onMessage above is essential: abort alone cannot stop side effects.
    if (options.signal?.aborted) {
      renewal.abort();
      deadlineWatch.abort();
      await Promise.all([renew, watchDeadline]);
      if (options.releaseOnShutdown && shutdown.signal?.aborted && shutdown.graceful() && leaseError === undefined &&
          (deadline === undefined || deadline > Date.now())) {
        const release = new AbortController();
        const timer = setTimeout(() => release.abort(), 5000);
        try {
          await groupRequest(shutdown.client.settle({ sequence: claim.message.sequence,
            claim_token: claim.claim_token, action: 'release' }), release.signal);
        } catch (error) { shutdown.onReleaseError(error); }
        finally { clearTimeout(timer); }
      }
      return;
    }
    if (deadline !== undefined && deadline <= Date.now()) expire();
    if (leaseError !== undefined) throw leaseError;
    const failureCode = failed ? (options.failureCode?.(handlerError, claim.message) ?? 'handler_failed') : undefined;
    if (failureCode !== undefined && !/^[\x20-\x7e]{1,64}$/.test(failureCode)) {
      throw new RangeError('failureCode must contain 1–64 printable ASCII characters');
    }
    await groupRequest(client.settle(failed
        ? { sequence: claim.message.sequence, claim_token: claim.claim_token, action: 'retry',
            failure_code: failureCode, ...(options.useGroupRetryPolicy && options.retryAfterSeconds === undefined ? {} : { retry_after_seconds: options.retryAfterSeconds ?? 1 }) }
        : { sequence: claim.message.sequence, claim_token: claim.claim_token, action: 'complete' }), (options.settlementBatchSize ?? 1) > 1 ? handler.signal : options.signal);
    if (failed) await options.onHandlerError?.(handlerError, claim.message);
  } finally {
    renewal.abort();
    deadlineWatch.abort();
    await Promise.all([renew, watchDeadline]);
    options.signal?.removeEventListener('abort', stop);
  }
}

/**
 * Pull only enough claims to fill available handler slots, renew each lease, and complete only
 * after the handler succeeds. Handler failures request a bounded server retry.
 * Network/auth/lease errors terminate; callers may restart the worker. Shutdown
 * leaves outstanding work to lease expiry unless releaseOnShutdown is enabled.
 * Release requests have a five-second budget; errors reject the worker shutdown.
 * Uses backend account API credentials.
 */
async function runRealtimeGroupWorker(client: RealtimeGroupProcessingClient, worker: string | undefined,
  options: RealtimeConsumerGroupProcessingOptions): Promise<void> {
  const pollMs = options.pollIntervalMs ?? 1000;
  const retrySeconds = options.retryAfterSeconds ?? 1;
  const concurrency = options.concurrency ?? 1;
  const waitSeconds = options.waitSeconds ?? 10;
  const batchSize = options.batchSize ?? Math.min(concurrency, 16);
  const settlementBatchSize = options.settlementBatchSize ?? 1;
  const settlementFlushMs = options.settlementFlushMs ?? 10;
  if (!Number.isInteger(settlementBatchSize) || settlementBatchSize < 1 || settlementBatchSize > 32) throw new RangeError('settlementBatchSize must be between 1 and 32');
  if (!Number.isInteger(settlementFlushMs) || settlementFlushMs < 0 || settlementFlushMs > 25) throw new RangeError('settlementFlushMs must be between 0 and 25');
  if (!Number.isFinite(pollMs) || pollMs < 100 || pollMs > 60_000) throw new RangeError('pollIntervalMs must be between 100 and 60000');
  if (!Number.isInteger(retrySeconds) || retrySeconds < 0 || retrySeconds > 300) throw new RangeError('retryAfterSeconds must be between 0 and 300');
  if (!Number.isInteger(concurrency) || concurrency < 1 || concurrency > 32) throw new RangeError('concurrency must be between 1 and 32');
  if (!Number.isInteger(batchSize) || batchSize < 1 || batchSize > 16) throw new RangeError('batchSize must be between 1 and 16');
  if (!Number.isInteger(waitSeconds) || waitSeconds < 0 || waitSeconds > 20) throw new RangeError('waitSeconds must be between 0 and 20');
  client = withRealtimeSettlementRetries(client, options.settlementRetries ?? 0);
  const controller = new AbortController();
  // Each active handler and its current request registers cancellation listeners.
  setMaxListeners(concurrency * 3 + 4, controller.signal);
  const abort = () => controller.abort(options.signal?.reason);
  options.signal?.addEventListener('abort', abort, { once: true });
  if (options.signal?.aborted) abort();
  const active = new Set<Promise<void>>();
  let failed = false;
  let failure: unknown;
  let blocker: Error | undefined;
  let releaseFailed = false;
  let releaseError: unknown;
  const fail = (error: unknown) => {
    if (!failed) { failed = true; failure = error; }
    controller.abort(error);
  };
  const processing = { ...options, signal: controller.signal };
  const processingClient = settlementBatchSize === 1 ? client : { ...client, settle: createRealtimeSettlementBatcher(client, settlementBatchSize, settlementFlushMs, controller.signal) };
  let heartbeatLoop: Promise<void> | undefined;
  try {
    if (controller.signal.aborted) return;
    const group = await groupRequest(client.status(), controller.signal);
    if (options.heartbeat) {
      await groupRequest(client.heartbeat(worker), controller.signal);
      heartbeatLoop = (async () => {
        while (await waitForGroupWork(15_000, controller.signal)) {
          await groupRequest(client.heartbeat(worker), controller.signal);
        }
      })().catch((error) => { if (!controller.signal.aborted) fail(error); });
    }
    while (!controller.signal.aborted) {
      if (blocker) { await Promise.all(active); break; }
      const slots = concurrency - active.size;
      if (slots === 0) { await Promise.race(active); continue; }
      // Preserve the original one-message route for the default worker.
      let batch: RealtimeConsumerGroupBatch;
      try {
        if (concurrency === 1 && batchSize === 1) {
          const claim = await groupRequest(client.claim(worker, waitSeconds), controller.signal);
          batch = { claims: claim ? [claim] : [], history_unavailable: false, capacity_blocked: false, paused: false, ordering_blocked: false, rate_throttled: false, retry_after_milliseconds: 0, filter_scan_limited: false, circuit_open: false, deduplication_blocked: false };
        } else {
          batch = await groupRequest(client.claimBatch(Math.min(batchSize, slots), worker, waitSeconds), controller.signal);
        }
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 429 || !['realtime_group_rate_limited', 'realtime_group_circuit_open', 'realtime_group_deduplication_full'].includes(error.body?.code)) throw error;
        const seconds = error.body?.retry_after_seconds;
        if (!Number.isFinite(seconds) || seconds <= 0) throw error;
        if (!await waitForGroupWork(Math.min(seconds * 1000, error.body?.code === 'realtime_group_deduplication_full' ? pollMs : 60_000), controller.signal)) break;
        continue;
      }
      if (batch.history_unavailable) blocker = new Error('Consumer-group retention gap requires explicit recovery');
      else if (batch.capacity_blocked) blocker = new Error('Consumer-group outstanding snapshot capacity reached');
      for (const claim of batch.claims) {
        let task: Promise<void>;
        task = processGroupClaim(processingClient, claim, group.lease_seconds, processing, {
          signal: options.signal, graceful: () => !failed, client, onReleaseError: (error) => {
            if (!releaseFailed) { releaseFailed = true; releaseError = error; }
          },
        })
          .catch(fail).finally(() => { active.delete(task); });
        active.add(task);
      }
      if ((batch.rate_throttled || batch.circuit_open || batch.deduplication_blocked) && !blocker) {
        const delay = batch.retry_after_milliseconds;
        if (!Number.isFinite(delay) || delay <= 0) throw new Error('Invalid consumer-group claim retry hint');
        const claimDelay = Math.min(delay, batch.deduplication_blocked ? pollMs : 60_000);
        if (batch.deduplication_blocked && active.size > 0) {
          // A local settlement can make an existing snapshot ready while new IDs wait.
          const pause = new AbortController();
          const stopPause = () => pause.abort();
          controller.signal.addEventListener('abort', stopPause, { once: true });
          if (controller.signal.aborted) pause.abort();
          try { await Promise.race([waitForGroupWork(claimDelay, pause.signal), ...active]); }
          finally { pause.abort(); controller.signal.removeEventListener('abort', stopPause); }
        } else if (!await waitForGroupWork(claimDelay, controller.signal)) break;
      } else if (batch.claims.length === 0 && !blocker) {
        // Wait for the polling interval or an active handler to free capacity.
        const pause = new AbortController();
        const stopPause = () => pause.abort();
        controller.signal.addEventListener('abort', stopPause, { once: true });
        if (controller.signal.aborted) pause.abort();
        try { await Promise.race([waitForGroupWork(pollMs, pause.signal), ...active]); }
        finally { pause.abort(); controller.signal.removeEventListener('abort', stopPause); }
      }
    }
  } catch (error) {
    if (!controller.signal.aborted) fail(error);
  } finally {
    controller.abort();
    await Promise.all(active);
    await heartbeatLoop;
    options.signal?.removeEventListener('abort', abort);
  }
  if (releaseFailed) throw releaseError;
  if (!options.signal?.aborted) {
    if (failed) throw failure;
    if (blocker) throw blocker;
  }
}

/** Account-authenticated worker; retained for callers using backend account keys. */
export async function consumeRealtimeConsumerGroup(options: RealtimeConsumerGroupWorkerOptions): Promise<void> {
 const target: RealtimeConsumerGroupTarget = { slug: options.slug, id: options.id, channel: options.channel, group: options.group };
 return runRealtimeGroupWorker({
  heartbeat: (worker) => RealtimeService.heartbeatRealtimeConsumerGroup({ ...target, requestBody: { worker: worker ?? options.worker, ready: true, status_code: 'ready' } }),
  status: () => RealtimeService.getRealtimeConsumerGroup(target),
  claim: (worker, waitSeconds) => RealtimeService.claimRealtimeConsumerGroup({ ...target, requestBody: { worker: worker ?? options.worker, wait_seconds: waitSeconds } }),
  claimBatch: (limit, worker, waitSeconds) => RealtimeService.claimRealtimeConsumerGroupBatch({ ...target, requestBody: { worker: worker ?? options.worker, limit, wait_seconds: waitSeconds } }),
  settleBatch: (requestBody) => RealtimeService.settleRealtimeConsumerGroupBatch({ ...target, requestBody }),
  settle: (requestBody) => RealtimeService.settleRealtimeConsumerGroupClaim({ ...target, requestBody }),
 }, options.worker, options);
}

/** Scoped worker client; requires status, claim, renew, complete and retry grants.
 * Its worker identity is fixed by the issued credential. No account key is needed.
 */
export async function consumeRealtimeConsumerGroupWithCredential(
 options: RealtimeConsumerGroupProcessingOptions & { client: RealtimeConsumerGroupWorkerClient },
): Promise<void> {
 return runRealtimeGroupWorker({ ...options.client, heartbeat: () => options.client.heartbeat({ ready: true, status_code: 'ready' }) }, undefined, options);
}

export interface RealtimeConsumerGroupDrainWaitOptions {
  signal?: AbortSignal;
  /** Timeout covers drain initiation, status requests and polling. Default 30 seconds. */
  timeoutMs?: number;
  /** Default 1 second. */
  pollIntervalMs?: number;
}

async function waitForRealtimeGroupDrain(
  client: { drain(): CancelablePromise<RealtimeConsumerGroupDrainState>; status(): CancelablePromise<RealtimeConsumerGroup> },
  options: RealtimeConsumerGroupDrainWaitOptions,
): Promise<RealtimeConsumerGroup> {
  const timeoutMs = options.timeoutMs ?? 30_000;
  const pollMs = options.pollIntervalMs ?? 1000;
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 2_147_483_647) throw new RangeError('timeoutMs must be a positive supported timer duration');
  if (!Number.isFinite(pollMs) || pollMs < 100 || pollMs > 60_000) throw new RangeError('pollIntervalMs must be between 100 and 60000');
  const controller = new AbortController();
  const abort = () => controller.abort(options.signal?.reason);
  options.signal?.addEventListener('abort', abort, { once: true });
  if (options.signal?.aborted) abort();
  const timer = setTimeout(() => controller.abort(new Error('Timed out waiting for consumer-group drain; drain mode remains active')), timeoutMs);
  try {
    if (controller.signal.aborted) throw controller.signal.reason;
    await groupRequest(client.drain(), controller.signal);
    while (!controller.signal.aborted) {
      const group = await groupRequest(client.status(), controller.signal);
      if (controller.signal.aborted) break;
      if (!group.draining) throw new Error('Consumer-group drain was cancelled by pause or resume');
      if (group.drained) return group;
      await waitForGroupWork(pollMs, controller.signal);
    }
    throw controller.signal.reason;
  } catch (error) {
    if (controller.signal.aborted) throw controller.signal.reason;
    throw error;
  } finally {
    clearTimeout(timer);
    options.signal?.removeEventListener('abort', abort);
  }
}

/** Enter persistent drain mode and wait for no unexpired leases. Does not prove
 * handler side effects stopped. Timeout/cancellation never resumes the group. */
export function drainRealtimeConsumerGroup(
  options: RealtimeConsumerGroupTarget & RealtimeConsumerGroupDrainWaitOptions,
): Promise<RealtimeConsumerGroup> {
  const target = { slug: options.slug, id: options.id, channel: options.channel, group: options.group };
  return waitForRealtimeGroupDrain({
    drain: () => RealtimeService.drainRealtimeConsumerGroup(target),
    status: () => RealtimeService.getRealtimeConsumerGroup(target),
  }, options);
}

/** Scoped credentials require drain and status grants. Resume remains explicit. */
export function drainRealtimeConsumerGroupWithCredential(
  options: RealtimeConsumerGroupDrainWaitOptions & { client: RealtimeConsumerGroupWorkerClient },
): Promise<RealtimeConsumerGroup> {
  return waitForRealtimeGroupDrain(options.client, options);
}
