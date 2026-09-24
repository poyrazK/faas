/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerResponse } from '../models/APIConsumerResponse.js';
import type { ApplyPlatformTenantRequest } from '../models/ApplyPlatformTenantRequest.js';
import type { ApplyPlatformTenantResponse } from '../models/ApplyPlatformTenantResponse.js';
import type { CreatePlatformTenantRequest } from '../models/CreatePlatformTenantRequest.js';
import type { LinkPlatformTenantConsumerRequest } from '../models/LinkPlatformTenantConsumerRequest.js';
import type { LinkPlatformTenantSurfaceRequest } from '../models/LinkPlatformTenantSurfaceRequest.js';
import type { PlatformTenantDetailResponse } from '../models/PlatformTenantDetailResponse.js';
import type { PlatformTenantListResponse } from '../models/PlatformTenantListResponse.js';
import type { PlatformTenantResponse } from '../models/PlatformTenantResponse.js';
import type { PlatformTenantSurfaceResponse } from '../models/PlatformTenantSurfaceResponse.js';
import type { PlatformTenantUsageResponse } from '../models/PlatformTenantUsageResponse.js';
import type { SetPlatformTenantStatusRequest } from '../models/SetPlatformTenantStatusRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class PlatformTenantsService {
  /**
   * List account-level platform customers.
   * @returns PlatformTenantListResponse One page of platform customers.
   * @throws ApiError
   */
  public static listPlatformTenants({
    limit = 100,
    offset,
  }: {
    /**
     * Maximum number of platform tenants in this page.
     */
    limit?: number,
    /**
     * Zero-based offset for the account's tenant list.
     */
    offset?: number,
  }): CancelablePromise<PlatformTenantListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants',
      query: {
        'limit': limit,
        'offset': offset,
      },
      errors: {
        401: `code: unauthorized`,
      },
    });
  }
  /**
   * Register an account-level end customer idempotently by external_ref.
   * @returns PlatformTenantResponse Existing customer with the same external_ref and name.
   * @throws ApiError
   */
  public static createPlatformTenant({
    requestBody,
  }: {
    requestBody: CreatePlatformTenantRequest,
  }): CancelablePromise<PlatformTenantResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        409: `code: conflict`,
      },
    });
  }
  /**
   * Atomically reconcile an additive customer onboarding bundle.
   * Creates a missing platform tenant and app consumers, links requested existing surfaces, or previews the same checks with dry_run. Omitted resources are not detached; keys, hostnames and certificates are managed separately.
   * @returns ApplyPlatformTenantResponse Applied or previewed onboarding plan and current surface states.
   * @throws ApiError
   */
  public static applyPlatformTenant({
    requestBody,
  }: {
    requestBody: ApplyPlatformTenantRequest,
  }): CancelablePromise<ApplyPlatformTenantResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/apply',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Read one customer and its linked app consumers and tenant surfaces.
   * @returns PlatformTenantDetailResponse Customer with current resource links.
   * @throws ApiError
   */
  public static getPlatformTenant({
    id,
  }: {
    /**
     * Platform tenant UUID in the authenticated account.
     */
    id: string,
  }): CancelablePromise<PlatformTenantDetailResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Suspend or resume linked consumer credentials and hostnames.
   * Anonymous traffic, independent JWT auth, and unrelated app domains keep their own access policy.
   * @returns PlatformTenantResponse Updated platform customer.
   * @throws ApiError
   */
  public static setPlatformTenantStatus({
    id,
    requestBody,
  }: {
    /**
     * Platform tenant UUID in the authenticated account.
     */
    id: string,
    requestBody: SetPlatformTenantStatusRequest,
  }): CancelablePromise<PlatformTenantResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/account/platform-tenants/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Link an existing same-account app consumer to this customer.
   * @returns APIConsumerResponse Linked consumer.
   * @throws ApiError
   */
  public static linkPlatformTenantConsumer({
    id,
    requestBody,
  }: {
    /**
     * Customer receiving the app-consumer link.
     */
    id: string,
    requestBody: LinkPlatformTenantConsumerRequest,
  }): CancelablePromise<APIConsumerResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/consumers',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Link an existing same-account tenant surface to this customer.
   * @returns PlatformTenantSurfaceResponse Linked surface.
   * @throws ApiError
   */
  public static linkPlatformTenantSurface({
    id,
    requestBody,
  }: {
    /**
     * Customer receiving the hostname-surface link.
     */
    id: string,
    requestBody: LinkPlatformTenantSurfaceRequest,
  }): CancelablePromise<PlatformTenantSurfaceResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/surfaces',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Read durable cross-app usage for linked consumers.
   * Raw usage only; app-specific rate cards and invoices are not aggregated.
   * @returns PlatformTenantUsageResponse Usage for the requested bounded UTC window.
   * @throws ApiError
   */
  public static getPlatformTenantUsage({
    id,
    since,
    until,
  }: {
    /**
     * Customer whose linked app usage is requested.
     */
    id: string,
    /**
     * Inclusive UTC start of the usage window, snapped to midnight.
     */
    since?: string,
    /**
     * Exclusive UTC end of the usage window, snapped to midnight.
     */
    until?: string,
  }): CancelablePromise<PlatformTenantUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/usage',
      path: {
        'id': id,
      },
      query: {
        'since': since,
        'until': until,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
}
