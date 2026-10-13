/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AsyncInvokeResponse } from '../models/AsyncInvokeResponse.js';
import type { CancelPendingWorkRequest } from '../models/CancelPendingWorkRequest.js';
import type { CancelPendingWorkResponse } from '../models/CancelPendingWorkResponse.js';
import type { DurableEntityBackup } from '../models/DurableEntityBackup.js';
import type { DurableEntityBackupPage } from '../models/DurableEntityBackupPage.js';
import type { DurableEntityInspectResponse } from '../models/DurableEntityInspectResponse.js';
import type { DurableEntityInvokeRequest } from '../models/DurableEntityInvokeRequest.js';
import type { DurableEntityInvokeResponse } from '../models/DurableEntityInvokeResponse.js';
import type { DurableEntityRestorePreview } from '../models/DurableEntityRestorePreview.js';
import type { DurableEntityRestoreRequest } from '../models/DurableEntityRestoreRequest.js';
import type { DurableEntityRestoreResponse } from '../models/DurableEntityRestoreResponse.js';
import type { DurableEntityRestoreValidationResponse } from '../models/DurableEntityRestoreValidationResponse.js';
import type { DurableEntityRetryRequest } from '../models/DurableEntityRetryRequest.js';
import type { DurableEntityRetryResponse } from '../models/DurableEntityRetryResponse.js';
import type { DurableEntityStateExport } from '../models/DurableEntityStateExport.js';
import type { Invocation } from '../models/Invocation.js';
import type { InvokeRequest } from '../models/InvokeRequest.js';
import type { InvokeResponse } from '../models/InvokeResponse.js';
import type { ListInvocationsResponse } from '../models/ListInvocationsResponse.js';
import type { UpsertWorkPolicyRequest } from '../models/UpsertWorkPolicyRequest.js';
import type { WorkPolicyListResponse } from '../models/WorkPolicyListResponse.js';
import type { WorkPolicyResponse } from '../models/WorkPolicyResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class InvocationsService {
  /**
   * Sync-invoke an app; long-poll for the result.
   * Enqueues an invocation row and waits for the drain to drive it
   * to a terminal state. Server-side cap is 30s on paid plans, 5s
   * on Free. Returns 504 (long_poll_timeout) when the cap elapses;
   * the customer can immediately re-call /v1/invocations/{id}
   * to pick up the eventual result.
   *
   * @returns InvokeResponse The completed invocation.
   * @throws ApiError
   */
  public static invokeApp({
    slug,
    requestBody,
    xGregaleRevision,
    xGregaleRelease,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: InvokeRequest,
    /**
     * Exact deployment pin. Mutually exclusive with X-Gregale-Release; checked again at delivery.
     */
    xGregaleRevision?: string,
    /**
     * Immutable project release set. Defaults to the active set for project apps and is checked again at delivery.
     */
    xGregaleRelease?: string,
  }): CancelablePromise<InvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/invoke',
      path: {
        'slug': slug,
      },
      headers: {
        'X-Gregale-Revision': xGregaleRevision,
        'X-Gregale-Release': xGregaleRelease,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        504: `code: long_poll_timeout — server-side long-poll budget elapsed without a terminal row.`,
      },
    });
  }
  /**
   * Inspect Durable Entity Tenant Quotas.
   * Inspect durable entity tenant quotas: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Inspect durable entity tenant quotas — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static inspectDurableEntityTenantQuotas({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Inspect durable entity tenant quotas — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Inspect durable entity tenant quotas — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Inspect durable entity tenant quotas — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Inspect durable entity tenant quotas — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/quotas',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Inspect durable entity tenant quotas — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Inspect durable entity tenant quotas — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Get Durable Entity Readiness.
   * Get durable entity readiness: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Get durable entity readiness — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static getDurableEntityReadiness({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/readiness',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Get durable entity readiness — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Get durable entity readiness — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Plan Durable Entity Quota Migration.
   * Plan durable entity quota migration: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Plan durable entity quota migration — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static planDurableEntityQuotaMigration({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Plan durable entity quota migration — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Plan durable entity quota migration — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Plan durable entity quota migration — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Plan durable entity quota migration — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
    /**
     * Plan durable entity quota migration — parameters / cursor: Opaque continuation from the preceding page; restart listing after revision or cursor conflicts.
     */
    cursor?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/quota-migration/plan',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Plan durable entity quota migration — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Plan durable entity quota migration — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect Durable Entity Quota Migration.
   * Inspect durable entity quota migration: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Inspect durable entity quota migration — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static inspectDurableEntityQuotaMigration({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Inspect durable entity quota migration — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Inspect durable entity quota migration — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Inspect durable entity quota migration — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Inspect durable entity quota migration — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/quota-migration',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Inspect durable entity quota migration — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Inspect durable entity quota migration — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Start Durable Entity Quota Migration.
   * Start durable entity quota migration: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Start durable entity quota migration — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static startDurableEntityQuotaMigration({
    slug,
    namespace,
    key,
    requestBody,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Start durable entity quota migration — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Start durable entity quota migration — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Start durable entity quota migration — requestBody: Exact operation input. Retain operation identity and payload when replaying an uncertain mutation.
     */
    requestBody: {
      /**
       * Start durable entity quota migration — requestBody / content / application/json / schema / properties / migration id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      migration_id: string;
      /**
       * Start durable entity quota migration — requestBody / content / application/json / schema / properties / expected migration id: Prior migration identity, or empty for an initial migration.
       */
      expected_migration_id?: string;
      /**
       * Start durable entity quota migration — requestBody / content / application/json / schema / properties / expected revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes.
       */
      expected_revision: number;
    },
    /**
     * Start durable entity quota migration — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Start durable entity quota migration — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/quota-migration/start',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Start durable entity quota migration — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Start durable entity quota migration — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Step Durable Entity Quota Migration.
   * Step durable entity quota migration: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Step durable entity quota migration — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static stepDurableEntityQuotaMigration({
    slug,
    namespace,
    key,
    requestBody,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Step durable entity quota migration — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Step durable entity quota migration — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Step durable entity quota migration — requestBody: Exact operation input. Retain operation identity and payload when replaying an uncertain mutation.
     */
    requestBody: {
      /**
       * Step durable entity quota migration — requestBody / content / application/json / schema / properties / migration id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      migration_id: string;
      /**
       * Step durable entity quota migration — requestBody / content / application/json / schema / properties / expected revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes.
       */
      expected_revision: number;
    },
    /**
     * Step durable entity quota migration — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Step durable entity quota migration — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/quota-migration/step',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Step durable entity quota migration — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Step durable entity quota migration — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect Durable Entity Quota Policy.
   * Inspect durable entity quota policy: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Inspect durable entity quota policy — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static inspectDurableEntityQuotaPolicy({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Inspect durable entity quota policy — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Inspect durable entity quota policy — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Inspect durable entity quota policy — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Inspect durable entity quota policy — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/quota-policy',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Inspect durable entity quota policy — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Inspect durable entity quota policy — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Preview Durable Entity Quota Policy.
   * Preview durable entity quota policy: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Preview durable entity quota policy — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static previewDurableEntityQuotaPolicy({
    slug,
    namespace,
    key,
    requestBody,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Preview durable entity quota policy — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Preview durable entity quota policy — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Preview durable entity quota policy — requestBody: Exact operation input. Retain operation identity and payload when replaying an uncertain mutation.
     */
    requestBody: {
      /**
       * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / operation id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      operation_id: string;
      /**
       * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / expected revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes.
       */
      expected_revision: number;
      /**
       * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / expected migration id: Observed completed migration identity.
       */
      expected_migration_id?: string;
      /**
       * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / expected migration revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes.
       */
      expected_migration_revision: number;
      /**
       * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / migration id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      migration_id: string;
      /**
       * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / limits: Proposed tenant admission caps; all fields are required.
       */
      limits: {
        /**
         * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / limits / properties / connections: Approved connections cap; server validates bounds and monotonic increases.
         */
        connections: number;
        /**
         * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / limits / properties / grants per minute: Approved grants per minute cap; server validates bounds and monotonic increases.
         */
        grants_per_minute: number;
        /**
         * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / limits / properties / invocations per minute: Approved invocations per minute cap; server validates bounds and monotonic increases.
         */
        invocations_per_minute: number;
        /**
         * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / limits / properties / retained bytes: Approved retained bytes cap; server validates bounds and monotonic increases.
         */
        retained_bytes: number;
        /**
         * Preview durable entity quota policy — requestBody / content / application/json / schema / properties / limits / properties / entity bytes: Approved entity bytes cap; server validates bounds and monotonic increases.
         */
        entity_bytes: number;
      };
    },
    /**
     * Preview durable entity quota policy — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Preview durable entity quota policy — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/quota-policy/preview',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Preview durable entity quota policy — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Preview durable entity quota policy — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Apply Durable Entity Quota Policy.
   * Apply durable entity quota policy: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Apply durable entity quota policy — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static applyDurableEntityQuotaPolicy({
    slug,
    namespace,
    key,
    requestBody,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Apply durable entity quota policy — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Apply durable entity quota policy — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Apply durable entity quota policy — requestBody: Exact operation input. Retain operation identity and payload when replaying an uncertain mutation.
     */
    requestBody: {
      /**
       * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / operation id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      operation_id: string;
      /**
       * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / expected revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes.
       */
      expected_revision: number;
      /**
       * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / expected migration id: Observed completed migration identity.
       */
      expected_migration_id?: string;
      /**
       * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / expected migration revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes.
       */
      expected_migration_revision: number;
      /**
       * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / migration id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      migration_id: string;
      /**
       * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / limits: Proposed tenant admission caps; all fields are required.
       */
      limits: {
        /**
         * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / limits / properties / connections: Approved connections cap; server validates bounds and monotonic increases.
         */
        connections: number;
        /**
         * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / limits / properties / grants per minute: Approved grants per minute cap; server validates bounds and monotonic increases.
         */
        grants_per_minute: number;
        /**
         * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / limits / properties / invocations per minute: Approved invocations per minute cap; server validates bounds and monotonic increases.
         */
        invocations_per_minute: number;
        /**
         * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / limits / properties / retained bytes: Approved retained bytes cap; server validates bounds and monotonic increases.
         */
        retained_bytes: number;
        /**
         * Apply durable entity quota policy — requestBody / content / application/json / schema / properties / limits / properties / entity bytes: Approved entity bytes cap; server validates bounds and monotonic increases.
         */
        entity_bytes: number;
      };
    },
    /**
     * Apply durable entity quota policy — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Apply durable entity quota policy — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/quota-policy/apply',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Apply durable entity quota policy — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Apply durable entity quota policy — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * List Durable Entity Registry.
   * List durable entity registry: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any List durable entity registry — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static listDurableEntityRegistry({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * List durable entity registry — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * List durable entity registry — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * List durable entity registry — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * List durable entity registry — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
    /**
     * List durable entity registry — parameters / cursor: Opaque continuation from the preceding page; restart listing after revision or cursor conflicts.
     */
    cursor?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/registry',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `List durable entity registry — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `List durable entity registry — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Enable Durable Entity Registry.
   * Enable durable entity registry: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Enable durable entity registry — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static enableDurableEntityRegistry({
    slug,
    namespace,
    key,
    requestBody,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Enable durable entity registry — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Enable durable entity registry — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Enable durable entity registry — requestBody: Exact operation input. Retain operation identity and payload when replaying an uncertain mutation.
     */
    requestBody: {
      /**
       * Enable durable entity registry — requestBody / content / application/json / schema / properties / operation id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      operation_id: string;
      /**
       * Enable durable entity registry — requestBody / content / application/json / schema / properties / expected revision: Exact opaque revision returned by inspection; never convert it to a number.
       */
      expected_revision: string;
    },
    /**
     * Enable durable entity registry — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Enable durable entity registry — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/registry/enable',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Enable durable entity registry — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Enable durable entity registry — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect Durable Entity Registry Cleanup.
   * Inspect durable entity registry cleanup: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Inspect durable entity registry cleanup — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static inspectDurableEntityRegistryCleanup({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Inspect durable entity registry cleanup — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Inspect durable entity registry cleanup — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Inspect durable entity registry cleanup — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Inspect durable entity registry cleanup — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/registry/cleanup',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Inspect durable entity registry cleanup — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Inspect durable entity registry cleanup — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Start Durable Entity Registry Cleanup.
   * Start durable entity registry cleanup: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Start durable entity registry cleanup — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static startDurableEntityRegistryCleanup({
    slug,
    namespace,
    key,
    requestBody,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Start durable entity registry cleanup — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Start durable entity registry cleanup — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Start durable entity registry cleanup — requestBody: Exact operation input. Retain operation identity and payload when replaying an uncertain mutation.
     */
    requestBody: {
      /**
       * Start durable entity registry cleanup — requestBody / content / application/json / schema / properties / operation id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      operation_id: string;
      /**
       * Start durable entity registry cleanup — requestBody / content / application/json / schema / properties / expected revision: Exact opaque revision returned by inspection; never convert it to a number.
       */
      expected_revision: string;
    },
    /**
     * Start durable entity registry cleanup — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Start durable entity registry cleanup — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/registry/cleanup/start',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Start durable entity registry cleanup — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Start durable entity registry cleanup — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Step Durable Entity Registry Cleanup.
   * Step durable entity registry cleanup: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Step durable entity registry cleanup — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static stepDurableEntityRegistryCleanup({
    slug,
    namespace,
    key,
    requestBody,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Step durable entity registry cleanup — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Step durable entity registry cleanup — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Step durable entity registry cleanup — requestBody: Exact operation input. Retain operation identity and payload when replaying an uncertain mutation.
     */
    requestBody: {
      /**
       * Step durable entity registry cleanup — requestBody / content / application/json / schema / properties / operation id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      operation_id: string;
      /**
       * Step durable entity registry cleanup — requestBody / content / application/json / schema / properties / expected revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes.
       */
      expected_revision: number;
      /**
       * Step durable entity registry cleanup — requestBody / content / application/json / schema / properties / restart cleanup: Restart bounded listing passes while preserving the existing epoch fence.
       */
      restart_cleanup?: boolean;
    },
    /**
     * Step durable entity registry cleanup — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Step durable entity registry cleanup — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/registry/cleanup/step',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Step durable entity registry cleanup — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Step durable entity registry cleanup — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Preview Durable Entity Retirement.
   * Preview durable entity retirement: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Preview durable entity retirement — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static previewDurableEntityRetirement({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Preview durable entity retirement — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Preview durable entity retirement — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Preview durable entity retirement — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Preview durable entity retirement — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/retirement',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Preview durable entity retirement — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Preview durable entity retirement — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Start Durable Entity Retirement.
   * Start durable entity retirement: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Start durable entity retirement — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static startDurableEntityRetirement({
    slug,
    namespace,
    key,
    requestBody,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Start durable entity retirement — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Start durable entity retirement — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Start durable entity retirement — requestBody: Exact operation input. Retain operation identity and payload when replaying an uncertain mutation.
     */
    requestBody: {
      /**
       * Start durable entity retirement — requestBody / content / application/json / schema / properties / operation id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      operation_id: string;
      /**
       * Start durable entity retirement — requestBody / content / application/json / schema / properties / expected version: Exact entity version observed in the retirement preview.
       */
      expected_version: number;
      /**
       * Start durable entity retirement — requestBody / content / application/json / schema / properties / expected recovery revision: Exact opaque revision returned by inspection; never convert it to a number.
       */
      expected_recovery_revision: string;
    },
    /**
     * Start durable entity retirement — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Start durable entity retirement — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/retirement/start',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Start durable entity retirement — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Start durable entity retirement — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Step Durable Entity Retirement.
   * Step durable entity retirement: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Step durable entity retirement — responses / 200: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static stepDurableEntityRetirement({
    slug,
    namespace,
    key,
    requestBody,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Step durable entity retirement — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Step durable entity retirement — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Step durable entity retirement — requestBody: Exact operation input. Retain operation identity and payload when replaying an uncertain mutation.
     */
    requestBody: {
      /**
       * Step durable entity retirement — requestBody / content / application/json / schema / properties / operation id: Fresh operation identity; replay must retain this identity and the exact request.
       */
      operation_id: string;
      /**
       * Step durable entity retirement — requestBody / content / application/json / schema / properties / expected revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes.
       */
      expected_revision: number;
      /**
       * Step durable entity retirement — requestBody / content / application/json / schema / properties / restart cleanup: Restart verified retirement cleanup after an uncertain outcome.
       */
      restart_cleanup?: boolean;
    },
    /**
     * Step durable entity retirement — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Step durable entity retirement — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/retirement/step',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Step durable entity retirement — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Step durable entity retirement — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Mint Durable Entity Session Grant.
   * Mint durable entity session grant: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns any Mint durable entity session grant — responses / 201: Bounded preview result. Metadata revisions remain opaque or exact integers as documented in the operator guide.
   * @throws ApiError
   */
  public static mintDurableEntitySessionGrant({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Mint durable entity session grant — requestBody: Exact operation input. Retain operation identity and payload when replaying an uncertain mutation.
     */
    requestBody: {
      /**
       * Mint durable entity session grant — requestBody / content / application/json / schema / properties / namespace: Exact entity namespace.
       */
      namespace: string;
      /**
       * Mint durable entity session grant — requestBody / content / application/json / schema / properties / key: Exact logical entity key.
       */
      key: string;
      /**
       * Mint durable entity session grant — requestBody / content / application/json / schema / properties / env: Registered environment; defaults to production.
       */
      env?: string;
      /**
       * Mint durable entity session grant — requestBody / content / application/json / schema / properties / platform tenant id: Optional same-account platform tenant.
       */
      platform_tenant_id?: string;
      /**
       * Mint durable entity session grant — requestBody / content / application/json / schema / properties / session id: Existing logical session identity.
       */
      session_id: string;
      /**
       * Mint durable entity session grant — requestBody / content / application/json / schema / properties / generation: Current session generation.
       */
      generation: number;
      /**
       * Mint durable entity session grant — requestBody / content / application/json / schema / properties / origin: Exact browser origin without a path, query or fragment.
       */
      origin: string;
      /**
       * Mint durable entity session grant — requestBody / content / application/json / schema / properties / commands: Allowed command names; delegation does not grant other commands.
       */
      commands: Array<string>;
      /**
       * Mint durable entity session grant — requestBody / content / application/json / schema / properties / ttl seconds: Credential lifetime in seconds.
       */
      ttl_seconds: number;
    },
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/session-grants',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Mint durable entity session grant — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Mint durable entity session grant — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Revoke Durable Entity Session Grant.
   * Revoke durable entity session grant: Opt-in durable-object preview. Requires the configured application allowlist and applicable feature gates; production enablement awaits native GCS/KVM acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy write access for mutation. Reader grace, claims and unknown acknowledgements must be respected. Responses are not cacheable. See the durable entity operator guides for the bounded recovery contract.
   * @returns void
   * @throws ApiError
   */
  public static revokeDurableEntitySessionGrant({
    slug,
    grant,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Revoke durable entity session grant — parameters / grant: Grant identity returned when minting the credential.
     */
    grant: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/entities/session-grants/{grant}',
      path: {
        'slug': slug,
        'grant': grant,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Revoke durable entity session grant — responses / 409: Observed revision, ownership, migration or request identity conflicts. Inspect before replaying.`,
        410: `Revoke durable entity session grant — responses / 410: Entity identity is permanently retired. Use a new identity for new work.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Upgrade a scoped durable entity session to WebSocket.
   * Upgrade a scoped durable entity session to websocket: Service transport requires Bearer authentication, MFA, deploy write access and exact entity selectors. Origin headers are rejected. Every frame re-enters authentication and immutable-scope admission. Feature gates and the application allowlist must be enabled; native acceptance remains pending. The Node durable-session helper handles framing, replay and generation fences.
   * @returns void
   * @throws ApiError
   */
  public static connectDurableEntity({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Upgrade a scoped durable entity session to websocket — parameters / namespace: Exact entity namespace selecting the scope.
     */
    namespace: string,
    /**
     * Upgrade a scoped durable entity session to websocket — parameters / key: Exact entity key selecting the scope; scope management affects the entire account/app/environment/tenant registry.
     */
    key: string,
    /**
     * Upgrade a scoped durable entity session to websocket — parameters / environment: Registered environment, defaulting to production.
     */
    environment?: string,
    /**
     * Upgrade a scoped durable entity session to websocket — parameters / platform tenant id: Optional same-account platform tenant; customer self-service tokens cannot manage scope state.
     */
    platformTenantId?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/connect',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Upgrade a scoped browser session to WebSocket.
   * Upgrade a scoped browser session to websocket: Browser transport authenticates the short-lived grant through Sec-WebSocket-Protocol and requires the exact Origin bound at issuance. Authorization headers and query parameters are rejected. Issuer revocation, scope and generation are rechecked for commands. Feature gates and the application allowlist must be enabled; native acceptance remains pending. The Node durable-session helper handles framing, replay and generation fences.
   * @returns void
   * @throws ApiError
   */
  public static connectDurableEntityBrowserSession({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/session-connect',
      path: {
        'slug': slug,
      },
      errors: {
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect one durable entity's state and recovery metadata.
   * Account-owner read preview, requiring apps:read or admin, MFA where
   * applicable, and the explicit durable entity app allowlist. Resolves the
   * current immutable environment and same-account optional customer. Owner
   * inspection permits suspended customers and plan downgrades for diagnosis;
   * it does not authorize execution. Customer self-service tokens are excluded.
   * Returns state version, alarm and pending-outbox recovery metadata only.
   * No business data, receipts, payloads, credentials, claim tokens or bucket
   * paths are exposed. Inspection acquires no ownership, writes no objects,
   * repairs no indexes and runs no guest code. A missing entity returns 404
   * without creating it; corrupt committed state fails closed with 503.
   * Head delivery status is a separate, later observation of retained transport
   * history. Unknown includes absent/pruned history or read failures and never
   * proves a message was not accepted. An empty pending queue does not prove
   * receiver completion. These observations are not atomic across stores.
   * Inspection does not retry work. Use its recovery_revision for the separate
   * owner-only retry endpoint. Responses are not cacheable.
   *
   * @returns DurableEntityInspectResponse Metadata-only entity and head delivery observations.
   * @throws ApiError
   */
  public static inspectDurableEntity({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Entity namespace, bounded to 256 UTF-8 bytes.
     */
    namespace: string,
    /**
     * Logical entity key, bounded to 256 UTF-8 bytes.
     */
    key: string,
    /**
     * Current registered project environment; defaults to production.
     */
    environment?: string,
    /**
     * Optional customer owned by the authenticated account, including suspended customers.
     */
    platformTenantId?: string,
  }): CancelablePromise<DurableEntityInspectResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/inspect',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Preview disabled, bucket unavailable, corrupt committed state or retryable reclamation race.`,
        504: `Inspection deadline elapsed.`,
      },
    });
  }
  /**
   * List retained backup metadata.
   * This backup listing operation provides an owner diagnostic preview requiring apps:read or admin and MFA where
   * applicable. Requires durable entity app enablement. Private, no-store.
   * No ownership acquisition, guest execution or writes. Backup listing is
   * bounded and metadata-only; backup reads contain sensitive application data.
   * Restore preview reports observed versions, recognized schema envelopes and
   * preserved pending work. Compatibility is always unverified; schema equality
   * does not validate application data. Preview grants no restore authority.
   *
   * @returns DurableEntityBackupPage Backup listing observation. Observational result; subsequent restore still requires a fenced commit.
   * @throws ApiError
   */
  public static listDurableEntityBackups({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Entity namespace for backup listing.
     */
    namespace: string,
    /**
     * Logical entity key for backup listing.
     */
    key: string,
    /**
     * Registered environment for backup listing; defaults to production.
     */
    environment?: string,
    /**
     * Optional account-owned customer scope for backup listing.
     */
    platformTenantId?: string,
    /**
     * Opaque continuation cursor from the previous backup page.
     */
    cursor?: string,
  }): CancelablePromise<DurableEntityBackupPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/backups',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Backup listing observation. Observation raced with publication/deletion, preview is unavailable or storage is corrupt. Retry the read.`,
        504: `Backup listing observation. Observation deadline elapsed. Retry the read.`,
      },
    });
  }
  /**
   * Read one private application-state backup.
   * This backup retrieval operation provides an owner diagnostic preview requiring apps:read or admin and MFA where
   * applicable. Requires durable entity app enablement. Private, no-store.
   * No ownership acquisition, guest execution or writes. Backup listing is
   * bounded and metadata-only; backup reads contain sensitive application data.
   * Restore preview reports observed versions, recognized schema envelopes and
   * preserved pending work. Compatibility is always unverified; schema equality
   * does not validate application data. Preview grants no restore authority.
   *
   * @returns DurableEntityBackup Backup retrieval observation. Observational result; subsequent restore still requires a fenced commit.
   * @throws ApiError
   */
  public static getDurableEntityBackup({
    slug,
    namespace,
    key,
    backupId,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Entity namespace for backup retrieval.
     */
    namespace: string,
    /**
     * Logical entity key for backup retrieval.
     */
    key: string,
    /**
     * Identifier of the retained backup to retrieve.
     */
    backupId: string,
    /**
     * Registered environment for backup retrieval; defaults to production.
     */
    environment?: string,
    /**
     * Optional account-owned customer scope for backup retrieval.
     */
    platformTenantId?: string,
  }): CancelablePromise<DurableEntityBackup> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/backups/get',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
        'backup_id': backupId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Backup retrieval observation. Observation raced with publication/deletion, preview is unavailable or storage is corrupt. Retry the read.`,
        504: `Backup retrieval observation. Observation deadline elapsed. Retry the read.`,
      },
    });
  }
  /**
   * Preview a restore without writes or execution.
   * This restore preview operation provides an owner diagnostic preview requiring apps:read or admin and MFA where
   * applicable. Requires durable entity app enablement. Private, no-store.
   * No ownership acquisition, guest execution or writes. Backup listing is
   * bounded and metadata-only; backup reads contain sensitive application data.
   * Restore preview reports observed versions, recognized schema envelopes and
   * preserved pending work. Compatibility is always unverified; schema equality
   * does not validate application data. Preview grants no restore authority.
   *
   * @returns DurableEntityRestorePreview Restore preview observation. Observational result; subsequent restore still requires a fenced commit.
   * @throws ApiError
   */
  public static previewDurableEntityRestore({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: DurableEntityRestoreRequest,
  }): CancelablePromise<DurableEntityRestorePreview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/restore/preview',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        413: `Preview body exceeds the central invocation byte limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Restore preview observation. Observation raced with publication/deletion, preview is unavailable or storage is corrupt. Retry the read.`,
        504: `Restore preview observation. Observation deadline elapsed. Retry the read.`,
      },
    });
  }
  /**
   * Export committed application state from one durable entity.
   * Owner-only preview requiring apps:read or admin and MFA where applicable.
   * Resolves immutable account, app, environment and tenant identity. Requires
   * app enablement. Diagnostic reads permit held/suspended scopes and plan
   * downgrades. Performs no writes or guest invocation. Contains sensitive
   * application JSON; responses are private, no-store. Excludes alarms,
   * receipts, outbox and ownership. Checksum detects corruption, not authority.
   *
   * @returns DurableEntityStateExport One committed state export. No version-zero entity is exported.
   * @throws ApiError
   */
  public static exportDurableEntity({
    slug,
    namespace,
    key,
    environment,
    platformTenantId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Entity namespace for state export.
     */
    namespace: string,
    /**
     * Logical entity key for state export.
     */
    key: string,
    /**
     * Registered environment for state export; defaults to production.
     */
    environment?: string,
    /**
     * Optional account-owned customer scope for state export.
     */
    platformTenantId?: string,
  }): CancelablePromise<DurableEntityStateExport> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/entities/export',
      path: {
        'slug': slug,
      },
      query: {
        'namespace': namespace,
        'key': key,
        'environment': environment,
        'platform_tenant_id': platformTenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `State export preview is unavailable, ownership is busy or storage failed. Retry the export read.`,
        504: `State export deadline elapsed. Retry the export read.`,
      },
    });
  }
  /**
   * Ask the live application deployment to validate exported state.
   * Operator-gated preview requiring deploy:write or admin, MFA where applicable,
   * app enablement, execution plan and active-tenant/account rules. Enqueues
   * a pinned invocation to the distinct private validation path. The synchronous
   * application validator must be pure and returns only a versioned boolean
   * verdict. Gregale commits no state, alarm, outbox or request receipt here;
   * invocation rows and normal execution resource use still occur. External
   * application I/O is not independently disabled by the current runtime.
   * Validate needs an existing committed entity and matching expected_version.
   * Returned deployment_id identifies the checked deployment, not a permission
   * token. Restore revalidates under its claim and rejects deployment drift;
   * preview/validation does not reserve a version. Keep candidate data private.
   *
   * @returns DurableEntityRestoreValidationResponse Application verdict without entity publication; false rejects the candidate.
   * @throws ApiError
   */
  public static validateDurableEntityRestore({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: DurableEntityRestoreRequest,
  }): CancelablePromise<DurableEntityRestoreValidationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/restore/validate',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `validateDurableEntityRestore requires an eligible plan and an account without a billing or abuse hold.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Current state or selected deployment changed. Resolve the observation before starting a new operation.`,
        413: `Validation request exceeds the central invocation byte limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        502: `Application validation failed or returned a malformed verdict.`,
        503: `Application validation preview is disabled or storage/execution is unavailable.`,
        504: `Validation deadline elapsed. No entity commit occurs for validation.`,
      },
    });
  }
  /**
   * Restore exported application data with a fenced expected-version check.
   * Owner-only mutation preview requiring deploy:write or admin and MFA where
   * applicable. Applies app enablement, execution plan, account hold and active
   * tenant rules. Export identity must exactly match resolved target scope.
   * Requires an existing committed entity, positive expected_version and stable
   * request_id. Commits only application data, preserving current receipts,
   * alarms, outbox and delivery attempts. Advances business version once; invokes
   * no guest unless application restore validation is enabled. With
   * FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED=1, every new restore requires
   * validation_deployment_id and a fresh pure validator verdict under the claim.
   * The chosen deployment is resolved before and after validation; publication
   * still uses the entity ownership/version CAS. Deployment routing and bucket
   * publication are not one atomic transaction. Receipt replay skips validation.
   * Receipt replay precedes expected-version comparison. Retry uncertain
   * outcomes with the identical request body and ID. Check application schema
   * compatibility before restoring. Audit is best effort after acknowledged
   * success, not atomic with the object-store commit. Responses are private,
   * no-store; request/response state must not be logged.
   *
   * @returns DurableEntityRestoreResponse Restore committed or an existing receipt was replayed.
   * @throws ApiError
   */
  public static restoreDurableEntity({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: DurableEntityRestoreRequest,
  }): CancelablePromise<DurableEntityRestoreResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/restore',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `restoreDurableEntity requires an eligible plan and an account without a billing or abuse hold.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Expected version is stale, request identity conflicts or storage budget is exceeded.`,
        413: `Restore request exceeds the central invocation byte limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Preview unavailable, busy ownership, storage failure or uncertain outcome. Retry restore with identical request ID and body.`,
        504: `Deadline elapsed. Retry restore with identical request ID and body.`,
      },
    });
  }
  /**
   * Re-arm exactly one exhausted alarm or outgoing message.
   * Account-owner mutation preview requiring deploy:write or admin, MFA where
   * applicable, durable entity app enablement and the existing execution plan
   * and active-customer rules. Account holds block recovery. Requires a fresh
   * inspection's version and opaque recovery_revision plus alarm_at or head_id.
   * The revision changes on every manifest write, including ownership and
   * retry metadata changes. Only exhausted work on an unowned entity can be
   * re-armed. A stale observation or non-exhausted target returns 409; an
   * active owner returns 503. No missing entity is created.
   * Recovery changes retry metadata only, preserving state, receipts, deadlines,
   * message identities, payloads and durable transport acceptance. It does not
   * invoke the guest, send a webhook or retry a terminal receiver delivery.
   * Existing workers must be enabled separately. Responses are not cacheable.
   * A repeated request after success returns 409. After an uncertain response,
   * inspect again before deciding whether another recovery is needed.
   *
   * @returns DurableEntityRetryResponse Retry metadata was re-armed; execution or delivery is not confirmed.
   * @throws ApiError
   */
  public static retryDurableEntity({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: DurableEntityRetryRequest,
  }): CancelablePromise<DurableEntityRetryResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/retry',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `retryDurableEntity requires an eligible plan and an account without a billing or abuse hold.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Observation changed, target changed or work is not exhausted. Inspect again.`,
        413: `Recovery request exceeds the central durable entity metadata size limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Preview unavailable, active owner, corrupt state or uncertain recovery outcome. Inspect before retrying.`,
        504: `Recovery deadline elapsed. Inspect before retrying.`,
      },
    });
  }
  /**
   * Invoke an object-storage-backed entity in the operator preview.
   * Disabled unless the operator configures a private bucket and explicitly
   * enables this app. Account authentication, deploy-write scopes and MFA
   * apply. A selected customer must belong to the account and be active;
   * customer self-service tokens are not accepted on this surface.
   * Entity identity includes the app, immutable environment identity,
   * optional verified customer, namespace and key. The deployed handler
   * receives state at POST /__gregale/entities and returns data, result and
   * an optional alarm_at. Handlers must compute transitions without external
   * side effects. Alarm delivery requires a separate operator opt-in and
   * private delimiter listing. Due alarms use the same scheduler path with
   * event=alarm. Clearing or replacing the deadline invalidates stale work;
   * a failed handler leaves the alarm due. Attempts may repeat, while state,
   * the alarm receipt and its next deadline publish atomically. Alarm timing
   * depends on bounded entity sweeps and has no production latency guarantee.
   * State and replay receipts commit in object storage; the existing SQL
   * invocation ledger is used only to schedule and observe guest execution.
   * Keep request_id and exact payload bytes for retries, including after an
   * uncertain response. HTTP Idempotency-Key does not identify entity work.
   * Request IDs starting with __gregale_alarm/ are reserved for delivery.
   * Replays return the original result and version without executing code.
   * Calls have a 25 second budget. Owners expire after at most five minutes.
   * An immutable receipt index preserves original results without a receipt
   * count ceiling. Encoded snapshots and individual receipts remain bounded
   * to 1 MiB. Operator cleanup can reclaim superseded snapshots and index
   * nodes after a fenced generation barrier; it never expires replay receipts.
   * An explicit operator per-entity byte cap can limit the current snapshot
   * and its reachable immutable receipts/index/archive. It excludes metadata,
   * abandoned uploads and provider history; it is not a plan billing quota.
   * Over-cap new work returns 409 durable_entity_storage_limit with limit and
   * observed projected bytes. Existing receipts replay at capacity. Legacy
   * entities under a cap return 503 durable_entity_inventory_pending for new
   * work until bounded verified accounting completes. Handler computation
   * can run before quota rejection. Entity deletion is not available yet.
   *
   * @returns DurableEntityInvokeResponse Committed or replayed entity result.
   * @throws ApiError
   */
  public static invokeDurableEntity({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: DurableEntityInvokeRequest,
  }): CancelablePromise<DurableEntityInvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/entities/invoke',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `invokeDurableEntity requires an eligible plan and an account without a billing or abuse hold.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Request identity conflict, object budget or committed storage cap exceeded, or deployment unavailable. Storage limits include limit and observed projected bytes.`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        502: `Handler failed without publishing entity state.`,
        503: `Preview disabled, owner busy, legacy accounting pending, storage unavailable or commit outcome uncertain; retry the same request identity.`,
        504: `Request budget elapsed; retry the same request identity and payload.`,
      },
    });
  }
  /**
   * Async-invoke an app; returns id + status URL.
   * Enqueues an invocation row and returns immediately with the
   * id. The customer polls /v1/invocations/{id} (or uses the
   * dashboard SSE) for the eventual row state.
   *
   * @returns AsyncInvokeResponse The enqueued invocation.
   * @throws ApiError
   */
  public static invokeAppAsync({
    slug,
    requestBody,
    idempotencyKey,
    xGregaleRevision,
    xGregaleRelease,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: InvokeRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
    /**
     * Exact deployment pin. Mutually exclusive with X-Gregale-Release; checked again at delivery.
     */
    xGregaleRevision?: string,
    /**
     * Immutable project release set. Defaults to the active set for project apps and is checked again at delivery.
     */
    xGregaleRelease?: string,
  }): CancelablePromise<AsyncInvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/invoke/async',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
        'X-Gregale-Revision': xGregaleRevision,
        'X-Gregale-Release': xGregaleRelease,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List named work policies for an app.
   * Stage reads return the complete desired policy collection from immutable workload settings. An uninitialized stage collection returns 409 and never inherits production policies. Stage policy execution remains unavailable until work lanes and producers are isolated.
   * @returns WorkPolicyListResponse App work policies.
   * @throws ApiError
   */
  public static listAppWorkPolicies({
    slug,
    environment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Registered project environment. Omit for legacy production policies. An explicit production selection uses the legacy collection.
     */
    environment?: string,
  }): CancelablePromise<WorkPolicyListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/work-policies',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Create or update a named app work policy.
   * Legacy production changes affect new work only. Stage edits create immutable desired workload configuration and subsequent deployments pin it; existing deployments keep their policies. Stage policy execution and qualification remain unavailable until work lanes and producers are isolated.
   * @returns WorkPolicyResponse Saved policy.
   * @throws ApiError
   */
  public static upsertAppWorkPolicy({
    slug,
    name,
    requestBody,
    environment,
    ifWorkloadRevision,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Named app work policy.
     */
    name: string,
    requestBody: UpsertWorkPolicyRequest,
    /**
     * Edit the complete desired collection in a registered stage. Omit for legacy production policies. Protected environments reject direct edits.
     */
    environment?: string,
    /**
     * Expected complete desired workload revision for a stage edit. Zero means no revision exists; concurrent edits return 409.
     */
    ifWorkloadRevision?: number,
  }): CancelablePromise<WorkPolicyResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/work-policies/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      headers: {
        'If-Workload-Revision': ifWorkloadRevision,
      },
      query: {
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Delete a policy after removing event subscription bindings.
   * A stage deletion preserves an explicit empty collection and its collection revision clock after the last policy is removed. Production producer bindings retain their existing deletion checks.
   * @returns void
   * @throws ApiError
   */
  public static deleteAppWorkPolicy({
    slug,
    name,
    environment,
    ifWorkloadRevision,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Named app work policy.
     */
    name: string,
    /**
     * Edit the complete desired collection in a registered stage. Omit for legacy production policies. Protected environments reject direct edits.
     */
    environment?: string,
    /**
     * Expected complete desired workload revision for a stage edit.
     */
    ifWorkloadRevision?: number,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/work-policies/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      headers: {
        'If-Workload-Revision': ifWorkloadRevision,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Cancel pending work for one policy and application key.
   * Running work continues. A repeated Idempotency-Key returns the original receipt and does not cancel newer work. A stage selection cancels only its isolated environment lane, including work admitted under a policy that has since been deleted from desired settings. Cancellation receipts are independent per environment. Omitting environment preserves the production API.
   * @returns CancelPendingWorkResponse Durable cancellation receipt.
   * @throws ApiError
   */
  public static cancelPendingAppWork({
    slug,
    name,
    requestBody,
    idempotencyKey,
    environment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Policy whose pending lane is being cancelled.
     */
    name: string,
    requestBody: CancelPendingWorkRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
    /**
     * Registered project environment whose pending work should be cancelled. Stage lanes and receipts are isolated by the environment's immutable identity.
     */
    environment?: string,
  }): CancelablePromise<CancelPendingWorkResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/work-policies/{name}/cancel-pending',
      path: {
        'slug': slug,
        'name': name,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * List recent invocations on the account.
   * Paginated by `?before=<id>` (the LAST id of the returned slice).
   * Defaults to 20 per page; capped at 200.
   *
   * @returns ListInvocationsResponse The page.
   * @throws ApiError
   */
  public static listInvocations({
    before,
    limit = 20,
  }: {
    /**
     * Cursor — return rows whose id is strictly less than this. Omit for the most recent page.
     */
    before?: string,
    /**
     * Page size; 1-200, default 20.
     */
    limit?: number,
  }): CancelablePromise<ListInvocationsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/invocations',
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read a single invocation by id (account-scoped).
   * @returns Invocation The row.
   * @throws ApiError
   */
  public static getInvocation({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<Invocation> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/invocations/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Re-issue a failed or dead_letter invocation.
   * Accepts no request body. Requires deployment write scope and configured
   * MFA. Only failed or dead-lettered unbound unkeyed work is eligible.
   * The parent and its current app must belong to the caller. The child
   * preserves the original request, deployment scope, customer identity
   * and trusted replay lineage. Trace/version headers and execution/result
   * lifetimes are refreshed; retry policy uses the current app and plan.
   *
   * Each parent creates at most one durable recovery child. Concurrent and
   * repeated requests return that child regardless of Idempotency-Key,
   * including after completion. A subsequent recovery targets the failed
   * child. If its child has been pruned, the retained parent returns 409
   * `invocation_replay_unavailable` instead of creating another execution.
   * Existing acceptance is returned before checking expired deployment pins.
   * Application delivery and external side effects remain at least once.
   *
   * Other parent states return `invocation_not_replayable`. Keyed work
   * returns `keyed_replay_requires_policy`; use `/replay-keyed` for failed
   * keyed work. Queue-bound or named-queue work returns
   * `queue_replay_requires_binding` and requires its app queue dead-letter
   * replay endpoint.
   *
   * @returns AsyncInvokeResponse The recovery child was enqueued or already exists.
   * @throws ApiError
   */
  public static replayInvocation({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<AsyncInvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/invocations/{id}/replay',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `The parent is not replayable, requires its policy/binding recovery
        path, or its durable child is no longer retained
        (\`invocation_replay_unavailable\`).
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Recover failed keyed work in its captured policy lane
   * Accepts no request body. Requires deploy write scope and configured MFA.
   * Only failed keyed work without a queue binding is eligible. The child
   * retains the captured policy revision, key, fairness limits, deployment
   * scope, customer identity, payload, retry policy and trusted replay root.
   * It receives the next sequence in the same lane, after previously admitted
   * work. Replay does not supersede pending rows or restart debounce.
   *
   * The original pending expiry and start deadline remain effective. A new
   * replay after either elapsed deadline returns `keyed_replay_expired`.
   * Each parent creates at most one child. Repeating a request returns that
   * child, including after completion; a subsequent recovery must target
   * the failed child. If the child has been pruned while its parent remains,
   * the parent returns `keyed_replay_unavailable` instead of executing again.
   * The original failure remains visible in event receipts and retained
   * replay history. Delivery and application side effects remain at least once.
   *
   * @returns AsyncInvokeResponse The lane-preserving recovery child was admitted or is already retained.
   * @throws ApiError
   */
  public static replayKeyedInvocation({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<AsyncInvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/invocations/{id}/replay-keyed',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Recovery is ineligible, its pending deadline expired, or its child is no longer retained.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
}
