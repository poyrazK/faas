/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerListResponse } from '../models/APIConsumerListResponse.js';
import type { APIConsumerRateCardListResponse } from '../models/APIConsumerRateCardListResponse.js';
import type { APIConsumerRateCardResponse } from '../models/APIConsumerRateCardResponse.js';
import type { APIConsumerResponse } from '../models/APIConsumerResponse.js';
import type { APIConsumerUsageQuoteResponse } from '../models/APIConsumerUsageQuoteResponse.js';
import type { APIConsumerUsageResponse } from '../models/APIConsumerUsageResponse.js';
import type { ConsumerKeyListResponse } from '../models/ConsumerKeyListResponse.js';
import type { ConsumerKeyResponse } from '../models/ConsumerKeyResponse.js';
import type { CreateAPIConsumerRateCardRequest } from '../models/CreateAPIConsumerRateCardRequest.js';
import type { CreateAPIConsumerRequest } from '../models/CreateAPIConsumerRequest.js';
import type { CreateConsumerKeyRequest } from '../models/CreateConsumerKeyRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class ConsumersService {
  /**
   * List stable API consumer identities for an app.
   * @returns APIConsumerListResponse Consumer identities (never includes credentials).
   * @throws ApiError
   */
  public static listApiConsumers({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<APIConsumerListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Register a stable API consumer identity.
   * @returns APIConsumerResponse The created consumer identity.
   * @throws ApiError
   */
  public static createApiConsumer({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateAPIConsumerRequest,
  }): CancelablePromise<APIConsumerResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumers',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Fetch one API consumer identity.
   * @returns APIConsumerResponse The consumer identity.
   * @throws ApiError
   */
  public static getApiConsumer({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Target API consumer identity UUID.
     */
    consumerId: string,
  }): CancelablePromise<APIConsumerResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Revoke an API consumer identity.
   * @returns APIConsumerResponse The revoked consumer identity.
   * @throws ApiError
   */
  public static revokeApiConsumer({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Target API consumer identity UUID.
     */
    consumerId: string,
  }): CancelablePromise<APIConsumerResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/consumers/{consumer_id}',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read durable minute usage for one API consumer.
   * Returns idempotent request, error, and billable-unit counters for the
   * selected stable consumer. The ledger is independent from sampled
   * request telemetry. Anonymous traffic is retained separately and is
   * not charged to this consumer identity.
   *
   * @returns APIConsumerUsageResponse Durable API consumer usage over the requested window.
   * @throws ApiError
   */
  public static getApiConsumerUsage({
    slug,
    consumerId,
    since,
    until,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity whose durable minute usage is returned.
     */
    consumerId: string,
    /**
     * RFC3339 lower bound. Defaults to the trailing 30 days.
     */
    since?: string,
    /**
     * RFC3339 exclusive upper bound. Defaults to UTC midnight today.
     */
    until?: string,
  }): CancelablePromise<APIConsumerUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      query: {
        'since': since,
        'until': until,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Quote one API consumer's priced usage.
   * Applies the app's immutable, versioned request rate cards to durable
   * consumer usage. The result is a deterministic estimate, not an invoice
   * or a payment authorization. Usage before the first effective rate card
   * is returned as unpriced_units rather than silently treated as free.
   *
   * @returns APIConsumerUsageQuoteResponse Deterministic usage quote over the requested window.
   * @throws ApiError
   */
  public static getApiConsumerUsageQuote({
    slug,
    consumerId,
    since,
    until,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Consumer identity whose priced usage is returned.
     */
    consumerId: string,
    /**
     * Optional RFC3339 lower bound for the quote period; defaults to the trailing 30 days.
     */
    since?: string,
    /**
     * Optional RFC3339 exclusive upper bound for the quote period; defaults to UTC midnight today.
     */
    until?: string,
  }): CancelablePromise<APIConsumerUsageQuoteResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/usage/quote',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      query: {
        'since': since,
        'until': until,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * List an app's immutable API consumer rate cards.
   * @returns APIConsumerRateCardListResponse Rate-card history in effective-time order.
   * @throws ApiError
   */
  public static listApiConsumerRateCards({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<APIConsumerRateCardListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/rate-cards',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Publish a new immutable API consumer rate card.
   * Publishes an app-level request price. Rate cards are append-only and
   * must use one currency per app. If effective_from is omitted, the card
   * starts at the next UTC minute.
   *
   * @returns APIConsumerRateCardResponse The newly published rate card.
   * @throws ApiError
   */
  public static createApiConsumerRateCard({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateAPIConsumerRateCardRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<APIConsumerRateCardResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/rate-cards',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Fetch one API consumer rate card.
   * @returns APIConsumerRateCardResponse The requested immutable rate card.
   * @throws ApiError
   */
  public static getApiConsumerRateCard({
    slug,
    rateCardId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Immutable API consumer rate-card UUID.
     */
    rateCardId: string,
  }): CancelablePromise<APIConsumerRateCardResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/rate-cards/{rate_card_id}',
      path: {
        'slug': slug,
        'rate_card_id': rateCardId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * List credentials for an API consumer (secret omitted).
   * @returns ConsumerKeyListResponse Consumer keys without plaintext secrets.
   * @throws ApiError
   */
  public static listConsumerKeys({
    slug,
    consumerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * API consumer identity UUID owning these credentials.
     */
    consumerId: string,
  }): CancelablePromise<ConsumerKeyListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/keys',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Issue a new consumer credential. Plaintext is returned once.
   * @returns ConsumerKeyResponse The created credential; key is returned exactly once.
   * @throws ApiError
   */
  public static createConsumerKey({
    slug,
    consumerId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * API consumer identity UUID owning these credentials.
     */
    consumerId: string,
    requestBody: CreateConsumerKeyRequest,
  }): CancelablePromise<ConsumerKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/keys',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
      },
    });
  }
  /**
   * Revoke a consumer credential.
   * @returns ConsumerKeyResponse The revoked credential (secret omitted).
   * @throws ApiError
   */
  public static revokeConsumerKey({
    slug,
    consumerId,
    keyId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * API consumer identity UUID for the key being revoked.
     */
    consumerId: string,
    /**
     * Consumer credential UUID.
     */
    keyId: string,
  }): CancelablePromise<ConsumerKeyResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/consumers/{consumer_id}/keys/{key_id}',
      path: {
        'slug': slug,
        'consumer_id': consumerId,
        'key_id': keyId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
}
