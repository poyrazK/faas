import { CancelablePromise } from './generated/core/CancelablePromise.js';
import type { RealtimeConsumerGroupSettlement } from './generated/models/RealtimeConsumerGroupSettlement.js';
import type { RealtimeConsumerGroupSettlementResult } from './generated/models/RealtimeConsumerGroupSettlementResult.js';
import type { RealtimeConsumerGroupBatchSettlementRequest } from './generated/models/RealtimeConsumerGroupBatchSettlementRequest.js';
import type { RealtimeConsumerGroupBatchSettlementResult } from './generated/models/RealtimeConsumerGroupBatchSettlementResult.js';

type Client = {
  settle(body: RealtimeConsumerGroupSettlement): CancelablePromise<RealtimeConsumerGroupSettlementResult>;
  settleBatch(body: RealtimeConsumerGroupBatchSettlementRequest): CancelablePromise<RealtimeConsumerGroupBatchSettlementResult>;
};
type Pending = {
  body: RealtimeConsumerGroupSettlement;
  resolve: (result: RealtimeConsumerGroupSettlementResult) => void;
  reject: (error: unknown) => void;
};

/** Per-worker bounded coalescing. Renewals/releases bypass the queue; optional receipt retries run on the underlying client. */
export function createRealtimeSettlementBatcher(client: Client, maxSize: number, delayMs: number, signal: AbortSignal): Client['settle'] {
  let pending: Pending[] = [];
  let timer: ReturnType<typeof setTimeout> | undefined;
  const flush = async (): Promise<void> => {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    const batch = pending.splice(0, maxSize);
    if (batch.length === 0) return;
    try {
      if (signal.aborted) throw signal.reason ?? new Error('Worker stopped');
      const request = client.settleBatch({ settlements: batch.map(item => item.body) });
      const abort = () => request.cancel();
      signal.addEventListener('abort', abort, { once: true });
      if (signal.aborted) abort();
      let response: RealtimeConsumerGroupBatchSettlementResult;
      try { response = await request; }
      finally { signal.removeEventListener('abort', abort); }
      if (response.results.length !== batch.length || response.results.some((item, index) => {
        const input = batch[index]!.body;
        return item.sequence !== input.sequence || item.action !== input.action ||
          !(item.status === 'lease_lost' || input.action === 'complete' && item.status === 'completed' ||
            input.action === 'retry' && (item.status === 'ready' || item.status === 'dead'));
      })) throw new Error('Invalid batch settlement response');
      response.results.forEach((item, index) => {
        if (item.status === 'lease_lost') batch[index]!.reject(new Error('Consumer-group claim lease lost during batch settlement'));
        else batch[index]!.resolve({ status: item.status as RealtimeConsumerGroupSettlementResult['status'],
          ...(item.available_at ? { available_at: item.available_at } : {}),
          ...(item.lease_until ? { lease_until: item.lease_until } : {}) });
      });
    } catch (error) { batch.forEach(item => item.reject(error)); }
  };
  return body => {
    if (body.action === 'renew' || body.action === 'release') return client.settle(body);
    return new CancelablePromise<RealtimeConsumerGroupSettlementResult>((resolve, reject, onCancel) => {
      if (signal.aborted) { reject(signal.reason ?? new Error('Worker stopped')); return; }
      const item: Pending = { body, resolve, reject };
      pending.push(item);
      onCancel(() => {
        pending = pending.filter(entry => entry !== item);
        if (pending.length === 0 && timer !== undefined) { clearTimeout(timer); timer = undefined; }
      });
      if (pending.length >= maxSize) void flush();
      else if (timer === undefined) timer = setTimeout(() => { void flush(); }, delayMs);
    });
  };
}
