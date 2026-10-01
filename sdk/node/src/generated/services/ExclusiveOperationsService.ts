/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExclusiveAppTaskOperationRequest } from '../models/ExclusiveAppTaskOperationRequest.js';
import type { ExclusiveOperationAccepted } from '../models/ExclusiveOperationAccepted.js';
import type { ExclusiveOperationPolicy } from '../models/ExclusiveOperationPolicy.js';
import type { ExclusiveOperationRecord } from '../models/ExclusiveOperationRecord.js';
import type { ExclusiveOperationRequest } from '../models/ExclusiveOperationRequest.js';
import type { ExclusiveTriggerBindingRecord } from '../models/ExclusiveTriggerBindingRecord.js';
import type { ExclusiveTriggerBindingRequest } from '../models/ExclusiveTriggerBindingRequest.js';
import type { ExclusiveWorkPolicyList } from '../models/ExclusiveWorkPolicyList.js';
import type { ExclusiveWorkPolicyRecord } from '../models/ExclusiveWorkPolicyRecord.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class ExclusiveOperationsService {
  /**
   * List account-managed exclusive operation policies.
   * @returns ExclusiveWorkPolicyList Account policies and current revisions.
   * @throws ApiError
   */
  public static listExclusiveOperationPolicies(): CancelablePromise<ExclusiveWorkPolicyList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/operation-policies',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Create or revise a named exclusive operation policy.
   * The policy explicitly chooses account or platform-tenant scope and
   * queue, reject, or join_existing contention. Member app IDs and the
   * optional project environment are validated against this account.
   * Account and customer identity are derived from authentication; the
   * business key never selects a security scope.
   *
   * @returns ExclusiveWorkPolicyRecord Saved policy revision.
   * @throws ApiError
   */
  public static upsertExclusiveOperationPolicy({
    name,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Account-owned policy name to create or replace.
     */
    name: string,
    requestBody: ExclusiveOperationPolicy,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ExclusiveWorkPolicyRecord> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/account/operation-policies/{name}',
      path: {
        'name': name,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Inspect the operation policy attached to an account-owned trigger.
   * @returns ExclusiveTriggerBindingRecord Trigger binding and configured business key.
   * @throws ApiError
   */
  public static getExclusiveOperationTriggerBinding({
    source,
    id,
  }: {
    /**
     * Account-owned cron, inbound webhook, broker/queue trigger, or recurring Job schedule whose exclusive-operation binding is being inspected or changed.
     */
    source: 'cron' | 'inbound_webhook' | 'broker' | 'job_schedule',
    /**
     * Account-owned trigger ID; for broker bindings this is a non-cron trigger resource.
     */
    id: string,
  }): CancelablePromise<ExclusiveTriggerBindingRecord> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/operation-trigger-bindings/{source}/{id}',
      path: {
        'source': source,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Route an account-owned trigger through a managed operation policy.
   * App triggers and recurring Job schedules are resolved from account-owned state. A tenant-scoped policy requires an active tenant-to-app link; tenant identity is not read from the business key. Job schedules require an account-scoped policy that explicitly includes the Job.
   * @returns ExclusiveTriggerBindingRecord Saved trigger binding.
   * @throws ApiError
   */
  public static upsertExclusiveOperationTriggerBinding({
    source,
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Account-owned cron, inbound webhook, broker/queue trigger, or recurring Job schedule whose exclusive-operation binding is being inspected or changed.
     */
    source: 'cron' | 'inbound_webhook' | 'broker' | 'job_schedule',
    /**
     * Account-owned trigger ID; for broker bindings this is a non-cron trigger resource.
     */
    id: string,
    requestBody: ExclusiveTriggerBindingRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ExclusiveTriggerBindingRecord> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/account/operation-trigger-bindings/{source}/{id}',
      path: {
        'source': source,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Remove exclusive-operation routing from a trigger.
   * @returns void
   * @throws ApiError
   */
  public static deleteExclusiveOperationTriggerBinding({
    source,
    id,
  }: {
    /**
     * Account-owned cron, inbound webhook, broker/queue trigger, or recurring Job schedule whose exclusive-operation binding is being inspected or changed.
     */
    source: 'cron' | 'inbound_webhook' | 'broker' | 'job_schedule',
    /**
     * Account-owned trigger ID; for broker bindings this is a non-cron trigger resource.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/account/operation-trigger-bindings/{source}/{id}',
      path: {
        'source': source,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Submit app work through a named exclusive-operation policy.
   * The account scope is authenticated. For platform-tenant policies use the account tenant route or the tenant-self route so the customer scope comes from trusted platform context.
   * @returns ExclusiveOperationAccepted The durable operation was accepted or joined to an equivalent active operation.
   * @throws ApiError
   */
  public static submitExclusiveOperation({
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
    requestBody: ExclusiveOperationRequest,
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
  }): CancelablePromise<ExclusiveOperationAccepted> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/operations',
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
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Submit a deployment-attached command through managed exclusive ownership.
   * The authenticated account and app identify the target; deployment selection and ownership generation are platform-controlled.
   * @returns ExclusiveOperationAccepted The task operation was accepted or joined to equivalent active work.
   * @throws ApiError
   */
  public static submitExclusiveAppTaskOperation({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: ExclusiveAppTaskOperationRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ExclusiveOperationAccepted> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/operations/tasks',
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
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Submit work for an account-authorized platform customer.
   * The account credential must own the selected tenant. The tenant ID is checked against account state and never inferred from the concurrency key.
   * @returns ExclusiveOperationAccepted The operation was accepted or joined for the customer tenant selected by the account owner.
   * @throws ApiError
   */
  public static submitTenantExclusiveOperation({
    tenantId,
    slug,
    requestBody,
    idempotencyKey,
    xGregaleRevision,
    xGregaleRelease,
  }: {
    /**
     * Platform tenant owned by the authenticated account.
     */
    tenantId: string,
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: ExclusiveOperationRequest,
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
  }): CancelablePromise<ExclusiveOperationAccepted> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{tenant_id}/apps/{slug}/operations',
      path: {
        'tenant_id': tenantId,
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
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Submit work for the customer bound to this tenant credential.
   * Requires platform_tenant:invocations:manage. The tenant is derived from the bearer token; callers cannot choose another tenant.
   * @returns ExclusiveOperationAccepted The operation was accepted or joined for the customer identified by this tenant credential.
   * @throws ApiError
   */
  public static submitPlatformTenantSelfExclusiveOperation({
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
    requestBody: ExclusiveOperationRequest,
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
  }): CancelablePromise<ExclusiveOperationAccepted> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/apps/{slug}/operations',
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
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Read an account-owned operation and its committed result.
   * @returns ExclusiveOperationRecord Operation receipt; claim tokens and accepted request contents are never returned.
   * @throws ApiError
   */
  public static getExclusiveOperation({
    id,
  }: {
    /**
     * Operation receipt ID.
     */
    id: string,
  }): CancelablePromise<ExclusiveOperationRecord> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/operations/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Cancel pending or active account-owned work.
   * @returns void
   * @throws ApiError
   */
  public static cancelExclusiveOperation({
    id,
  }: {
    /**
     * Operation receipt ID to cancel.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/operations/{id}/cancel',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Read an operation belonging to the authenticated platform customer.
   * Requires platform_tenant:invocations:read. The tenant is derived from the bearer token.
   * @returns ExclusiveOperationRecord Customer-owned operation receipt; claim tokens and request contents are never returned.
   * @throws ApiError
   */
  public static getPlatformTenantSelfExclusiveOperation({
    id,
  }: {
    /**
     * Operation receipt ID owned by the authenticated tenant.
     */
    id: string,
  }): CancelablePromise<ExclusiveOperationRecord> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/operations/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Cancel work belonging to the authenticated platform customer.
   * Requires platform_tenant:invocations:manage. The tenant is derived from the bearer token.
   * @returns void
   * @throws ApiError
   */
  public static cancelPlatformTenantSelfExclusiveOperation({
    id,
  }: {
    /**
     * Tenant-owned operation receipt ID to cancel.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/operations/{id}/cancel',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
}
