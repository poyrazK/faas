import { randomUUID } from 'node:crypto';
import { ApiError } from './generated/core/ApiError.js';
import { CancelablePromise } from './generated/core/CancelablePromise.js';
import type { RealtimeConsumerGroupSettlement } from './generated/models/RealtimeConsumerGroupSettlement.js';
import type { RealtimeConsumerGroupSettlementResult } from './generated/models/RealtimeConsumerGroupSettlementResult.js';
import type { RealtimeConsumerGroupBatchSettlementRequest } from './generated/models/RealtimeConsumerGroupBatchSettlementRequest.js';
import type { RealtimeConsumerGroupBatchSettlementResult } from './generated/models/RealtimeConsumerGroupBatchSettlementResult.js';

type Client = {
  settle(body: RealtimeConsumerGroupSettlement): CancelablePromise<RealtimeConsumerGroupSettlementResult>;
  settleBatch(body: RealtimeConsumerGroupBatchSettlementRequest): CancelablePromise<RealtimeConsumerGroupBatchSettlementResult>;
};

function retryable(error: unknown): boolean {
  // Fetch reports connection failures as TypeError. Cancellation/auth/conflicts/capacity do not retry.
  return error instanceof TypeError || error instanceof ApiError && [408, 500, 502, 503, 504].includes(error.status);
}

function retrySettlement<T>(request: () => CancelablePromise<T>, retries: number): CancelablePromise<T> {
  return new CancelablePromise<T>((resolve, reject, onCancel) => {
    let active: CancelablePromise<T> | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    onCancel(() => { if (timer !== undefined) clearTimeout(timer); active?.cancel(); });
    const attempt = async (index: number): Promise<void> => {
      if (onCancel.isCancelled) return;
      try {
        active = request();
        const result = await active;
        if (!onCancel.isCancelled) resolve(result);
      } catch (error) {
        if (onCancel.isCancelled) return;
        if (index >= retries || !retryable(error)) { reject(error); return; }
        timer = setTimeout(() => { timer = undefined; void attempt(index + 1); }, 100 * 2 ** index);
      }
    };
    void attempt(0);
  });
}

/** Retry terminal settlements with one receipt key per request; renewal remains immediate. */
export function withRealtimeSettlementRetries<T extends Client>(client: T, retries: number): T {
  if (!Number.isInteger(retries) || retries < 0 || retries > 3) throw new RangeError('settlementRetries must be between 0 and 3');
  if (retries === 0) return client;
  return {
    ...client,
    settle: (body: RealtimeConsumerGroupSettlement) => {
      if (body.action === 'renew') return client.settle(body);
      const request = { ...body, idempotency_key: body.idempotency_key ?? randomUUID() };
      return retrySettlement(() => client.settle(request), retries);
    },
    settleBatch: (body: RealtimeConsumerGroupBatchSettlementRequest) => {
      const request = { ...body, settlements: body.settlements.map(item => ({ ...item })),
        idempotency_key: body.idempotency_key ?? randomUUID() };
      return retrySettlement(() => client.settleBatch(request), retries);
    },
  };
}
