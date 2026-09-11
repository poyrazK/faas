/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerListResponse } from '../models/APIConsumerListResponse.js';
import type { APIConsumerResponse } from '../models/APIConsumerResponse.js';
import type { ConsumerKeyListResponse } from '../models/ConsumerKeyListResponse.js';
import type { ConsumerKeyResponse } from '../models/ConsumerKeyResponse.js';
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
