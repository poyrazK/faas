/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerResponse } from '../models/APIConsumerResponse.js';
import type { ApplyPlatformTenantCredentialsRequest } from '../models/ApplyPlatformTenantCredentialsRequest.js';
import type { ApplyPlatformTenantCredentialsResponse } from '../models/ApplyPlatformTenantCredentialsResponse.js';
import type { ApplyPlatformTenantRequest } from '../models/ApplyPlatformTenantRequest.js';
import type { ApplyPlatformTenantResponse } from '../models/ApplyPlatformTenantResponse.js';
import type { ClaimAPIConsumerUsageStatementRequest } from '../models/ClaimAPIConsumerUsageStatementRequest.js';
import type { CreateAPIConsumerUsageStatementRequest } from '../models/CreateAPIConsumerUsageStatementRequest.js';
import type { CreatePlatformTenantRequest } from '../models/CreatePlatformTenantRequest.js';
import type { LinkPlatformTenantConsumerRequest } from '../models/LinkPlatformTenantConsumerRequest.js';
import type { LinkPlatformTenantSurfaceRequest } from '../models/LinkPlatformTenantSurfaceRequest.js';
import type { PlatformTenantActivationResponse } from '../models/PlatformTenantActivationResponse.js';
import type { PlatformTenantCredentialsResponse } from '../models/PlatformTenantCredentialsResponse.js';
import type { PlatformTenantDetailResponse } from '../models/PlatformTenantDetailResponse.js';
import type { PlatformTenantListResponse } from '../models/PlatformTenantListResponse.js';
import type { PlatformTenantRequestBudgetResponse } from '../models/PlatformTenantRequestBudgetResponse.js';
import type { PlatformTenantResponse } from '../models/PlatformTenantResponse.js';
import type { PlatformTenantStatementHandoffResponse } from '../models/PlatformTenantStatementHandoffResponse.js';
import type { PlatformTenantStatementListResponse } from '../models/PlatformTenantStatementListResponse.js';
import type { PlatformTenantStatementResponse } from '../models/PlatformTenantStatementResponse.js';
import type { PlatformTenantSurfaceResponse } from '../models/PlatformTenantSurfaceResponse.js';
import type { PlatformTenantUsageResponse } from '../models/PlatformTenantUsageResponse.js';
import type { SetPlatformTenantRequestBudgetRequest } from '../models/SetPlatformTenantRequestBudgetRequest.js';
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
   * Atomically creates or reuses a platform tenant, app consumers, tenant surfaces, and hostname intent, or previews the same checks with dry_run. Existing surface IDs may also be linked. Omitted resources are not detached; keys are separate and DNS verification and certificate issuance remain asynchronous.
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
   * Read customer DNS, certificate, and routing readiness.
   * A read-only snapshot. Ready requires the tenant and every linked surface to be active, every hostname verified, a valid issued certificate, and tenant-surface routing enabled.
   * @returns PlatformTenantActivationResponse Observed activation state and DNS TXT challenges.
   * @throws ApiError
   */
  public static getPlatformTenantActivation({
    id,
  }: {
    /**
     * Customer whose domain activation is requested.
     */
    id: string,
  }): CancelablePromise<PlatformTenantActivationResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/activation',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read cross-app customer request admission ceilings and counters.
   * @returns PlatformTenantRequestBudgetResponse Current policy and UTC-window admitted-request counters.
   * @throws ApiError
   */
  public static getPlatformTenantRequestBudget({
    id,
  }: {
    /**
     * Customer whose shared admission budget is managed.
     */
    id: string,
  }): CancelablePromise<PlatformTenantRequestBudgetResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/request-budget',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Set cross-app customer request admission ceilings.
   * Zero disables a ceiling. The shared counter is authoritative across gateway replicas; configured admission fails closed if it is unavailable. These are admitted-request counts, not billed usage or a money cap.
   * @returns PlatformTenantRequestBudgetResponse Updated policy and current counters.
   * @throws ApiError
   */
  public static setPlatformTenantRequestBudget({
    id,
    requestBody,
  }: {
    /**
     * Customer whose shared admission budget is managed.
     */
    id: string,
    requestBody: SetPlatformTenantRequestBudgetRequest,
  }): CancelablePromise<PlatformTenantRequestBudgetResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/account/platform-tenants/{id}/request-budget',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
  /**
   * List revisions of a customer's cross-app usage statement for one period.
   * @returns PlatformTenantStatementListResponse Immutable statement revisions, oldest first.
   * @throws ApiError
   */
  public static listPlatformTenantStatements({
    id,
    periodStart,
    periodEnd,
  }: {
    /**
     * Platform tenant whose cross-app statements are requested.
     */
    id: string,
    /**
     * Inclusive UTC-minute start of the statement period.
     */
    periodStart: string,
    /**
     * Exclusive UTC-minute end of the statement period.
     */
    periodEnd: string,
  }): CancelablePromise<PlatformTenantStatementListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/usage-statements',
      path: {
        'id': id,
      },
      query: {
        'period_start': periodStart,
        'period_end': periodEnd,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Snapshot tenant-attributed usage across apps or create a late-usage adjustment.
   * A draft replays unchanged. After finalization, new units create the next revision; no new units replay the latest revision. Mixed currencies are rejected.
   * @returns PlatformTenantStatementResponse Existing draft or latest unchanged revision.
   * @throws ApiError
   */
  public static createPlatformTenantStatement({
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Platform tenant whose cross-app statements are requested.
     */
    id: string,
    requestBody: CreateAPIConsumerUsageStatementRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<PlatformTenantStatementResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/usage-statements',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
   * Read an immutable cross-app statement revision.
   * @returns PlatformTenantStatementResponse Statement snapshot.
   * @throws ApiError
   */
  public static getPlatformTenantStatement({
    id,
    statementId,
  }: {
    /**
     * Platform tenant owning the statement.
     */
    id: string,
    /**
     * Immutable cross-app statement revision UUID.
     */
    statementId: string,
  }): CancelablePromise<PlatformTenantStatementResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/usage-statements/{statement_id}',
      path: {
        'id': id,
        'statement_id': statementId,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Finalize a fully priced, single-currency statement revision.
   * @returns PlatformTenantStatementResponse Finalized statement.
   * @throws ApiError
   */
  public static finalizePlatformTenantStatement({
    id,
    statementId,
    idempotencyKey,
  }: {
    /**
     * Platform tenant whose statement revision is finalized.
     */
    id: string,
    /**
     * Statement revision to finalize.
     */
    statementId: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<PlatformTenantStatementResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/usage-statements/{statement_id}/finalize',
      path: {
        'id': id,
        'statement_id': statementId,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Read the immutable external billing receipt.
   * @returns PlatformTenantStatementHandoffResponse Handoff receipt.
   * @throws ApiError
   */
  public static getPlatformTenantStatementHandoff({
    id,
    statementId,
  }: {
    /**
     * Platform tenant whose external billing receipt is requested.
     */
    id: string,
    /**
     * Finalized statement revision to hand off.
     */
    statementId: string,
  }): CancelablePromise<PlatformTenantStatementHandoffResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/usage-statements/{statement_id}/handoff',
      path: {
        'id': id,
        'statement_id': statementId,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Record one provider-neutral external billing handoff.
   * Rejects consumer windows already claimed by overlapping app-local statements. Adjustment revisions for this exact tenant and period may each be handed off once.
   * @returns PlatformTenantStatementHandoffResponse Existing identical handoff receipt.
   * @throws ApiError
   */
  public static claimPlatformTenantStatement({
    id,
    statementId,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Platform tenant whose external billing receipt is requested.
     */
    id: string,
    /**
     * Finalized statement revision to hand off.
     */
    statementId: string,
    requestBody: ClaimAPIConsumerUsageStatementRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<PlatformTenantStatementHandoffResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/usage-statements/{statement_id}/handoff',
      path: {
        'id': id,
        'statement_id': statementId,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
   * List metadata for a customer's linked consumer keys across apps.
   * @returns PlatformTenantCredentialsResponse Metadata only; plaintext credentials are never returned.
   * @throws ApiError
   */
  public static listPlatformTenantCredentials({
    id,
    limit = 100,
    offset,
  }: {
    /**
     * Platform tenant whose credential metadata is requested.
     */
    id: string,
    /**
     * Maximum number of keys to return.
     */
    limit?: number,
    /**
     * Number of keys to skip.
     */
    offset?: number,
  }): CancelablePromise<PlatformTenantCredentialsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/credentials',
      path: {
        'id': id,
      },
      query: {
        'limit': limit,
        'offset': offset,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Atomically issue or rotate client-generated customer credentials across apps.
   * Send only a random key's prefix and SHA-256 hash, never its plaintext. Save plaintext securely before submission. Replaying an identical bundle is safe; revocations and additions commit together. This endpoint never returns plaintext, even on creation.
   * @returns ApplyPlatformTenantCredentialsResponse Planned or applied credential changes.
   * @throws ApiError
   */
  public static applyPlatformTenantCredentials({
    id,
    requestBody,
  }: {
    /**
     * Platform tenant whose linked consumers receive credentials.
     */
    id: string,
    requestBody: ApplyPlatformTenantCredentialsRequest,
  }): CancelablePromise<ApplyPlatformTenantCredentialsResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/credentials/apply',
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
}
