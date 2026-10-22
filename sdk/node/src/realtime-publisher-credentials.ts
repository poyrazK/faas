import { OpenAPI } from './generated/core/OpenAPI.js';
import { request } from './generated/core/request.js';
import type { CancelablePromise } from './generated/core/CancelablePromise.js';
import type { RealtimePublisherMessageRequest } from './generated/models/RealtimePublisherMessageRequest.js';
import type { RealtimePublisherPublishResponse } from './generated/models/RealtimePublisherPublishResponse.js';

export interface RealtimePublisherClientOptions {
  endpointId: string;
  credential: string | (() => string | Promise<string>);
  baseUrl?: string;
}
export interface RealtimePublisherClient {
  /** Reuse the same key and decoded content when retrying an uncertain outcome. */
  publish(channel: string, message: RealtimePublisherMessageRequest, idempotencyKey: string): CancelablePromise<RealtimePublisherPublishResponse>;
}

/** Isolated publisher auth; no global account token/header/cookie inheritance. */
export function createRealtimePublisherClient(options: RealtimePublisherClientOptions): RealtimePublisherClient {
  const config = {
    BASE: options.baseUrl ?? OpenAPI.BASE,
    VERSION: OpenAPI.VERSION,
    WITH_CREDENTIALS: false,
    CREDENTIALS: 'omit' as const,
    TOKEN: async () => {
      const secret = typeof options.credential === 'function' ? await options.credential() : options.credential;
      if (!/^grp_[A-Za-z0-9_-]{43}$/.test(secret)) throw new Error('A current grp_ publisher credential is required');
      return secret;
    },
  };
  return {
    publish: (channel, message, idempotencyKey) => request(config, {
      method: 'POST',
      url: '/v1/realtime/publishers/endpoints/{endpoint_id}/channels/{channel}/publish',
      path: { endpoint_id: options.endpointId, channel },
      headers: { 'Idempotency-Key': idempotencyKey },
      body: message,
      mediaType: 'application/json',
      errors: { 400: 'Invalid publish request', 401: 'Publisher credential unavailable',
        403: 'Publish grant denied', 409: 'Idempotency content conflict', 429: 'Publisher limit reached' },
    }),
  };
}
