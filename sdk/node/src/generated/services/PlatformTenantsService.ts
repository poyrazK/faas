/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerResponse } from '../models/APIConsumerResponse.js';
import type { ApplyPlatformTenantCredentialsRequest } from '../models/ApplyPlatformTenantCredentialsRequest.js';
import type { ApplyPlatformTenantCredentialsResponse } from '../models/ApplyPlatformTenantCredentialsResponse.js';
import type { ApplyPlatformTenantRequest } from '../models/ApplyPlatformTenantRequest.js';
import type { ApplyPlatformTenantResponse } from '../models/ApplyPlatformTenantResponse.js';
import type { AppWebhookDeliveryListResponse } from '../models/AppWebhookDeliveryListResponse.js';
import type { AppWebhookRetryDeliveryResponse } from '../models/AppWebhookRetryDeliveryResponse.js';
import type { ClaimAPIConsumerUsageStatementRequest } from '../models/ClaimAPIConsumerUsageStatementRequest.js';
import type { CreateAPIConsumerUsageStatementRequest } from '../models/CreateAPIConsumerUsageStatementRequest.js';
import type { CreatePlatformTenantAccessTokenRequest } from '../models/CreatePlatformTenantAccessTokenRequest.js';
import type { CreatePlatformTenantAccessTokenResponse } from '../models/CreatePlatformTenantAccessTokenResponse.js';
import type { CreatePlatformTenantRateCardRequest } from '../models/CreatePlatformTenantRateCardRequest.js';
import type { CreatePlatformTenantRequest } from '../models/CreatePlatformTenantRequest.js';
import type { CreatePlatformTenantWebhookRequest } from '../models/CreatePlatformTenantWebhookRequest.js';
import type { LinkPlatformTenantConsumerRequest } from '../models/LinkPlatformTenantConsumerRequest.js';
import type { LinkPlatformTenantSurfaceRequest } from '../models/LinkPlatformTenantSurfaceRequest.js';
import type { PlatformTenantAccessTokenListResponse } from '../models/PlatformTenantAccessTokenListResponse.js';
import type { PlatformTenantAccessTokenResponse } from '../models/PlatformTenantAccessTokenResponse.js';
import type { PlatformTenantActivationResponse } from '../models/PlatformTenantActivationResponse.js';
import type { PlatformTenantActivityResponse } from '../models/PlatformTenantActivityResponse.js';
import type { PlatformTenantCredentialsResponse } from '../models/PlatformTenantCredentialsResponse.js';
import type { PlatformTenantDetailResponse } from '../models/PlatformTenantDetailResponse.js';
import type { PlatformTenantListResponse } from '../models/PlatformTenantListResponse.js';
import type { PlatformTenantRateCardListResponse } from '../models/PlatformTenantRateCardListResponse.js';
import type { PlatformTenantRateCardResponse } from '../models/PlatformTenantRateCardResponse.js';
import type { PlatformTenantRequestBudgetResponse } from '../models/PlatformTenantRequestBudgetResponse.js';
import type { PlatformTenantResponse } from '../models/PlatformTenantResponse.js';
import type { PlatformTenantSelfStatementListResponse } from '../models/PlatformTenantSelfStatementListResponse.js';
import type { PlatformTenantStatementHandoffResponse } from '../models/PlatformTenantStatementHandoffResponse.js';
import type { PlatformTenantStatementListResponse } from '../models/PlatformTenantStatementListResponse.js';
import type { PlatformTenantStatementResponse } from '../models/PlatformTenantStatementResponse.js';
import type { PlatformTenantSurfaceResponse } from '../models/PlatformTenantSurfaceResponse.js';
import type { PlatformTenantUsageResponse } from '../models/PlatformTenantUsageResponse.js';
import type { PlatformTenantWebhookListResponse } from '../models/PlatformTenantWebhookListResponse.js';
import type { PlatformTenantWebhookResponse } from '../models/PlatformTenantWebhookResponse.js';
import type { RotateAppWebhookSecretRequest } from '../models/RotateAppWebhookSecretRequest.js';
import type { RotateAppWebhookSecretResponse } from '../models/RotateAppWebhookSecretResponse.js';
import type { SetPlatformTenantRequestBudgetRequest } from '../models/SetPlatformTenantRequestBudgetRequest.js';
import type { SetPlatformTenantStatusRequest } from '../models/SetPlatformTenantStatusRequest.js';
import type { UpdatePlatformTenantWebhookRequest } from '../models/UpdatePlatformTenantWebhookRequest.js';
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
   * List immutable customer prices applied across linked apps.
   * @returns PlatformTenantRateCardListResponse Tenant tariff history ordered by effective minute.
   * @throws ApiError
   */
  public static listPlatformTenantRateCards({
    id,
  }: {
    /**
     * Customer whose cross-app commercial tariff is managed.
     */
    id: string,
  }): CancelablePromise<PlatformTenantRateCardListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/rate-cards',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Set an immutable cross-app customer price.
   * Appends a version that overrides app-level prices for this tenant from its effective UTC minute. Before the first tenant version takes effect, statement pricing falls back to each app's rate card. A tenant can use only one currency; finalized statement revisions are never rewritten.
   * @returns PlatformTenantRateCardResponse New immutable tenant rate-card version.
   * @throws ApiError
   */
  public static createPlatformTenantRateCard({
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Customer whose cross-app commercial tariff is managed.
     */
    id: string,
    requestBody: CreatePlatformTenantRateCardRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<PlatformTenantRateCardResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/rate-cards',
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
   * Read retained request-debugger evidence across a platform tenant's apps.
   * This plan-gated support view returns only bounded debugger evidence carrying request-time tenant attribution. It is sampled/retained evidence, not a complete request ledger or billing source of truth. Bodies, headers, and credentials are never returned.
   * @returns PlatformTenantActivityResponse One bounded page of observed request telemetry; represented request counts are weighted by collapsed rows.
   * @throws ApiError
   */
  public static listPlatformTenantActivity({
    id,
    since,
    appId,
    status,
    limit = 100,
    cursor,
  }: {
    /**
     * Platform tenant whose cross-app request evidence is requested.
     */
    id: string,
    /**
     * Positive duration such as 30m, 24h, or 3d; defaults to 24h and is clamped to plan retention.
     */
    since?: string,
    /**
     * Restrict to one app UUID; results remain scoped to this tenant's request-time attribution.
     */
    appId?: string,
    /**
     * Restrict to one HTTP response status.
     */
    status?: number,
    /**
     * Maximum rows in this page.
     */
    limit?: number,
    /**
     * Opaque cursor from the previous page; it pins the time window and filters.
     */
    cursor?: string,
  }): CancelablePromise<PlatformTenantActivityResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/activity',
      path: {
        'id': id,
      },
      query: {
        'since': since,
        'app_id': appId,
        'status': status,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * For each usage minute, an effective tenant-wide rate card takes precedence over the app's rate card; before the tenant's first effective card, app pricing remains the fallback. A draft replays unchanged. After finalization, new units create the next revision; no new units replay the latest revision. Mixed effective currencies are rejected.
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
   * List a platform tenant's statement event receivers.
   * @returns PlatformTenantWebhookListResponse Tenant-scoped webhook subscriptions.
   * @throws ApiError
   */
  public static listPlatformTenantWebhooks({
    id,
  }: {
    /**
     * Platform tenant whose billing event receiver is managed.
     */
    id: string,
  }): CancelablePromise<PlatformTenantWebhookListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/webhooks',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Subscribe to finalized cross-app tenant statements.
   * Creates a signed, retryable receiver scoped to this tenant. Finalization is durably enqueued with the statement transition; one delivery is created per statement revision.
   * @returns PlatformTenantWebhookResponse Receiver created. The plaintext secret is not returned.
   * @throws ApiError
   */
  public static createPlatformTenantWebhook({
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Platform tenant whose billing event receiver is managed.
     */
    id: string,
    requestBody: CreatePlatformTenantWebhookRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<PlatformTenantWebhookResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/webhooks',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Read a tenant webhook subscription.
   * @returns PlatformTenantWebhookResponse Subscription metadata; the secret remains masked.
   * @throws ApiError
   */
  public static getPlatformTenantWebhook({
    id,
    webhookId,
  }: {
    /**
     * Tenant that owns the subscription being inspected or changed.
     */
    id: string,
    /**
     * Tenant-scoped webhook subscription to inspect or change.
     */
    webhookId: string,
  }): CancelablePromise<PlatformTenantWebhookResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/webhooks/{webhook_id}',
      path: {
        'id': id,
        'webhook_id': webhookId,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Update a tenant webhook destination or delivery policy.
   * @returns PlatformTenantWebhookResponse Updated subscription metadata.
   * @throws ApiError
   */
  public static updatePlatformTenantWebhook({
    id,
    webhookId,
    requestBody,
  }: {
    /**
     * Tenant that owns the subscription being inspected or changed.
     */
    id: string,
    /**
     * Tenant-scoped webhook subscription to inspect or change.
     */
    webhookId: string,
    requestBody: UpdatePlatformTenantWebhookRequest,
  }): CancelablePromise<PlatformTenantWebhookResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/account/platform-tenants/{id}/webhooks/{webhook_id}',
      path: {
        'id': id,
        'webhook_id': webhookId,
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
   * Delete a tenant webhook and its remaining delivery history.
   * @returns void
   * @throws ApiError
   */
  public static deletePlatformTenantWebhook({
    id,
    webhookId,
  }: {
    /**
     * Tenant that owns the subscription being inspected or changed.
     */
    id: string,
    /**
     * Tenant-scoped webhook subscription to inspect or change.
     */
    webhookId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/account/platform-tenants/{id}/webhooks/{webhook_id}',
      path: {
        'id': id,
        'webhook_id': webhookId,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Rotate the signing secret for a tenant webhook.
   * @returns RotateAppWebhookSecretResponse Secret rotated; plaintext is not returned.
   * @throws ApiError
   */
  public static rotatePlatformTenantWebhookSecret({
    id,
    webhookId,
    requestBody,
  }: {
    /**
     * Tenant context used to authorize secret rotation.
     */
    id: string,
    /**
     * Subscription whose signing secret is rotated.
     */
    webhookId: string,
    requestBody: RotateAppWebhookSecretRequest,
  }): CancelablePromise<RotateAppWebhookSecretResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/webhooks/{webhook_id}/rotate-secret',
      path: {
        'id': id,
        'webhook_id': webhookId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Inspect tenant statement webhook deliveries.
   * @returns AppWebhookDeliveryListResponse Durable delivery history, newest first.
   * @throws ApiError
   */
  public static listPlatformTenantWebhookDeliveries({
    id,
    webhookId,
    pageSize = 50,
    pageToken,
  }: {
    /**
     * Tenant context used to scope delivery history.
     */
    id: string,
    /**
     * Subscription whose delivery attempts are listed.
     */
    webhookId: string,
    /**
     * Maximum number of delivery rows to return, from 1 to 100.
     */
    pageSize?: number,
    /**
     * Opaque cursor returned by the previous page.
     */
    pageToken?: string,
  }): CancelablePromise<AppWebhookDeliveryListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/webhooks/{webhook_id}/deliveries',
      path: {
        'id': id,
        'webhook_id': webhookId,
      },
      query: {
        'page_size': pageSize,
        'page_token': pageToken,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Retry one dead tenant webhook delivery.
   * @returns AppWebhookRetryDeliveryResponse Delivery requeued.
   * @throws ApiError
   */
  public static retryPlatformTenantWebhookDelivery({
    id,
    webhookId,
    did,
  }: {
    /**
     * Tenant context used to authorize delivery replay.
     */
    id: string,
    /**
     * Subscription that owns the delivery to replay.
     */
    webhookId: string,
    /**
     * Dead delivery to retry.
     */
    did: string,
  }): CancelablePromise<AppWebhookRetryDeliveryResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/webhooks/{webhook_id}/deliveries/{did}/retry',
      path: {
        'id': id,
        'webhook_id': webhookId,
        'did': did,
      },
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
  /**
   * List metadata for downstream tenant access tokens.
   * @returns PlatformTenantAccessTokenListResponse Metadata only; token plaintext is never returned by listing.
   * @throws ApiError
   */
  public static listPlatformTenantAccessTokens({
    id,
  }: {
    /**
     * Platform tenant receiving the downstream self-service token.
     */
    id: string,
  }): CancelablePromise<PlatformTenantAccessTokenListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/platform-tenants/{id}/access-tokens',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Mint a tenant-bound read-only self-service credential.
   * The bearer is scoped to exactly one downstream tenant, supports usage and/or finalized-statement reads, expires within 365 days, and is returned once. Account-wide API-key creation cannot mint these special tenant scopes. This endpoint does not cache plaintext for Idempotency-Key retries; after a lost response, list token metadata and create a replacement under a new name.
   * @returns CreatePlatformTenantAccessTokenResponse Token metadata and one-time plaintext bearer. Store the token securely; it cannot be retrieved later.
   * @throws ApiError
   */
  public static createPlatformTenantAccessToken({
    id,
    requestBody,
  }: {
    /**
     * Platform tenant receiving the downstream self-service token.
     */
    id: string,
    requestBody: CreatePlatformTenantAccessTokenRequest,
  }): CancelablePromise<CreatePlatformTenantAccessTokenResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{id}/access-tokens',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
      },
    });
  }
  /**
   * Revoke a downstream tenant access token.
   * @returns PlatformTenantAccessTokenResponse Revoked token metadata; plaintext is never returned.
   * @throws ApiError
   */
  public static revokePlatformTenantAccessToken({
    id,
    tokenId,
  }: {
    /**
     * Platform tenant that owns the access token.
     */
    id: string,
    /**
     * Access token to revoke.
     */
    tokenId: string,
  }): CancelablePromise<PlatformTenantAccessTokenResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/account/platform-tenants/{id}/access-tokens/{token_id}',
      path: {
        'id': id,
        'token_id': tokenId,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read this tenant's own cross-app raw usage.
   * Requires a tenant-bound access token with platform_tenant:usage:read. The credential cannot select or impersonate a different tenant.
   * @returns PlatformTenantUsageResponse This tenant's usage over the requested bounded window.
   * @throws ApiError
   */
  public static getPlatformTenantSelfUsage({
    since,
    until,
  }: {
    /**
     * Inclusive UTC-day boundary for the requested tenant usage.
     */
    since?: string,
    /**
     * Exclusive UTC-day boundary; buckets before this instant are included.
     */
    until?: string,
  }): CancelablePromise<PlatformTenantUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/usage',
      query: {
        'since': since,
        'until': until,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
      },
    });
  }
  /**
   * List this tenant's finalized cross-app usage statements.
   * Requires a tenant-bound access token with platform_tenant:statements:read. Drafts and superseded revisions are never exposed. Statements overlapping the requested window are returned newest period and revision first.
   * @returns PlatformTenantSelfStatementListResponse One bounded page of finalized statements.
   * @throws ApiError
   */
  public static listPlatformTenantSelfStatements({
    periodStart,
    periodEnd,
    limit = 100,
    offset,
  }: {
    /**
     * Inclusive UTC-minute start of the lookup window; the range may be at most 90 days.
     */
    periodStart: string,
    /**
     * Exclusive UTC-minute end of the lookup window.
     */
    periodEnd: string,
    /**
     * Maximum statements in this page.
     */
    limit?: number,
    /**
     * Zero-based offset for the next page; use next_offset from the previous response.
     */
    offset?: number,
  }): CancelablePromise<PlatformTenantSelfStatementListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/usage-statements',
      query: {
        'period_start': periodStart,
        'period_end': periodEnd,
        'limit': limit,
        'offset': offset,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
      },
    });
  }
  /**
   * Read one of this tenant's finalized statement revisions.
   * Draft, superseded, and other tenants' statements all appear as not found.
   * @returns PlatformTenantStatementResponse Finalized statement snapshot.
   * @throws ApiError
   */
  public static getPlatformTenantSelfStatement({
    statementId,
  }: {
    /**
     * Finalized immutable statement revision belonging to this token's tenant.
     */
    statementId: string,
  }): CancelablePromise<PlatformTenantStatementResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/usage-statements/{statement_id}',
      path: {
        'statement_id': statementId,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
}
