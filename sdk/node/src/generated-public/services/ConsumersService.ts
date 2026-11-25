/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConsumerIdentityListResponse } from '../models/ConsumerIdentityListResponse.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class ConsumersService {
  protected static readonly clientConfig?: typeof OpenAPI;
  /**
   * List the OAuth sign-ins of the app's MCP server and the consumers they bill to, newest first.
   * @returns ConsumerIdentityListResponse The app's consumer identities.
   * @throws ApiError
   */
  public static listConsumerIdentities({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ConsumerIdentityListResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/consumer-identities',
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
   * Unlink an OAuth sign-in from its consumer; the next sign-in enrolls again or links by key.
   * @returns void
   * @throws ApiError
   */
  public static deleteConsumerIdentity({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * The identity to unlink, as listed by the consumer identities endpoint.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/consumer-identities/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
}
