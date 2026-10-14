import type { RealtimeConsumerGroupBatchSettlementRequest } from './generated/models/RealtimeConsumerGroupBatchSettlementRequest.js';
import type { RealtimeConsumerGroupBatchSettlementResult } from './generated/models/RealtimeConsumerGroupBatchSettlementResult.js';
import type { RealtimeWorkerHeartbeat } from './generated/models/RealtimeWorkerHeartbeat.js';
import type { RealtimeWorkerHeartbeatRequest } from './generated/models/RealtimeWorkerHeartbeatRequest.js';
import { OpenAPI } from './generated/core/OpenAPI.js';
import { request } from './generated/core/request.js';
import type { CancelablePromise } from './generated/core/CancelablePromise.js';
import type { RealtimeConsumerGroup } from './generated/models/RealtimeConsumerGroup.js';
import type { RealtimeConsumerGroupDrainState } from './generated/models/RealtimeConsumerGroupDrainState.js';
import type { RealtimeConsumerGroupPauseState } from './generated/models/RealtimeConsumerGroupPauseState.js';
import type { RealtimeConsumerGroupBatch } from './generated/models/RealtimeConsumerGroupBatch.js';
import type { RealtimeConsumerGroupClaim } from './generated/models/RealtimeConsumerGroupClaim.js';
import type { RealtimeConsumerGroupSettlement } from './generated/models/RealtimeConsumerGroupSettlement.js';
import type { RealtimeConsumerGroupSettlementResult } from './generated/models/RealtimeConsumerGroupSettlementResult.js';
import type { RealtimeConsumerGroupDeadLetter } from './generated/models/RealtimeConsumerGroupDeadLetter.js';

export interface RealtimeConsumerGroupWorkerClient {
  heartbeat(body: RealtimeWorkerHeartbeatRequest): CancelablePromise<RealtimeWorkerHeartbeat>;
  status(): CancelablePromise<RealtimeConsumerGroup>;
  claim(worker?: string, waitSeconds?: number): CancelablePromise<RealtimeConsumerGroupClaim>;
  claimBatch(limit: number, worker?: string, waitSeconds?: number): CancelablePromise<RealtimeConsumerGroupBatch>;
  settle(body: RealtimeConsumerGroupSettlement): CancelablePromise<RealtimeConsumerGroupSettlementResult>;
  settleBatch(body: RealtimeConsumerGroupBatchSettlementRequest): CancelablePromise<RealtimeConsumerGroupBatchSettlementResult>;
  deadLetters(): CancelablePromise<RealtimeConsumerGroupDeadLetter[]>;
  replay(sequence: string): CancelablePromise<void>;
  resync(expectedSequence: string, afterSequence: string): CancelablePromise<void>;
  deleteGroup(): CancelablePromise<void>;
  drain(): CancelablePromise<RealtimeConsumerGroupDrainState>;
  pause(): CancelablePromise<RealtimeConsumerGroupPauseState>;
  resume(): CancelablePromise<RealtimeConsumerGroupPauseState>;
}

export interface RealtimeConsumerGroupWorkerClientOptions {
  groupId: string;
  /** A fresh secret provider is called for each request, allowing rotation. */
  credential: string | (() => string | Promise<string>);
  baseUrl?: string;
}

/**
 * Isolated, group-bound backend worker client. Never changes global OpenAPI
 * authentication or inherits account authorization headers/cookies/basic auth.
 */
export function createRealtimeConsumerGroupWorkerClient(
  options: RealtimeConsumerGroupWorkerClientOptions,
): RealtimeConsumerGroupWorkerClient {
  const config = {
    BASE: options.baseUrl ?? OpenAPI.BASE,
    VERSION: OpenAPI.VERSION,
    WITH_CREDENTIALS: false,
    CREDENTIALS: 'omit' as const,
    TOKEN: async () => {
      const secret = typeof options.credential === 'function' ? await options.credential() : options.credential;
      if (!/^grw_[A-Za-z0-9_-]{43}$/.test(secret)) throw new Error('A current grw_ worker credential is required');
      return secret;
    },
  };
  const path = { group_id: options.groupId };
  const url = '/v1/realtime/workers/groups/{group_id}';
  const errors = { 401: 'Worker credential unavailable', 403: 'Worker permission denied',
    404: 'Group unavailable', 409: 'Claim lost or group conflict', 429: 'Worker capacity or authentication limit' };
  return {
    heartbeat: (body) => request(config, { method: 'POST', url: `${url}/heartbeats`, path, errors, body, mediaType: 'application/json' }),
    status: () => request(config, { method: 'GET', url, path, errors }),
    claim: (worker, waitSeconds = 0) => request(config, { method: 'POST', url: `${url}/claims`, path, errors,
      body: { wait_seconds: waitSeconds, ...(worker ? { worker } : {}) }, mediaType: 'application/json' }),
    claimBatch: (limit, worker, waitSeconds = 0) => request(config, { method: 'POST', url: `${url}/claims/batch`, path, errors,
      body: { limit, wait_seconds: waitSeconds, ...(worker ? { worker } : {}) }, mediaType: 'application/json' }),
    settleBatch: (body) => request(config, { method: 'POST', url: `${url}/claims/settle/batch`, path, errors, body, mediaType: 'application/json' }),
    settle: (body) => request(config, { method: 'POST', url: `${url}/claims/settle`, path, errors,
      body, mediaType: 'application/json' }),
    deadLetters: () => request(config, { method: 'GET', url: `${url}/dead-letters`, path, errors }),
    replay: (sequence) => request(config, { method: 'POST', url: `${url}/dead-letters/{sequence}/replay`,
      path: { ...path, sequence }, errors }),
    resync: (expectedSequence, afterSequence) => request(config, { method: 'POST', url: `${url}/resync`, path, errors,
      body: { expected_sequence: expectedSequence, after_sequence: afterSequence }, mediaType: 'application/json' }),
    drain: () => request(config, { method: 'POST', url: `${url}/drain`, path, errors }),
    pause: () => request(config, { method: 'POST', url: `${url}/pause`, path, errors }),
    resume: () => request(config, { method: 'POST', url: `${url}/resume`, path, errors }),
    deleteGroup: () => request(config, { method: 'DELETE', url, path, errors }),
  };
}
