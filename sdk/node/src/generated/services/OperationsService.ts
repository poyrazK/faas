/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationAcceptedResponse } from '../models/OperationAcceptedResponse.js';
import type { OperationCancellationRequest } from '../models/OperationCancellationRequest.js';
import type { OperationDefinitionResponse } from '../models/OperationDefinitionResponse.js';
import type { OperationDefinitionSpec } from '../models/OperationDefinitionSpec.js';
import type { OperationEventsResponse } from '../models/OperationEventsResponse.js';
import type { OperationResponse } from '../models/OperationResponse.js';
import type { OperationStartRequest } from '../models/OperationStartRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class OperationsService {
  /**
   * Install an immutable operation definition.
   * Requires deploy:write. Normal source deployment resolves schemas and installs definitions automatically. Repeating the same resolved contract returns the same definition. Changing a definition on the same deployment conflicts. This staged API has no production admission switch; definition registration and submission return 503 until the HTTP execution adapter is qualified.
   * @returns OperationDefinitionResponse Installed contract with immutable revision and deployment pins.
   * @throws ApiError
   */
  public static putOperationDefinition({
    slug,
    deploymentId,
    name,
    requestBody,
    xGregaleRelease,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment owning the contract.
     */
    deploymentId: string,
    /**
     * Operation name matching the request contract.
     */
    name: string,
    requestBody: OperationDefinitionSpec,
    /**
     * Optional release-set identity pinned by this definition.
     */
    xGregaleRelease?: string,
  }): CancelablePromise<OperationDefinitionResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/deployments/{deployment_id}/operation-definitions/{name}',
      path: {
        'slug': slug,
        'deployment_id': deploymentId,
        'name': name,
      },
      headers: {
        'X-Gregale-Release': xGregaleRelease,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read an account-owned operation.
   * Requires read scope. Business outcome and completion delivery are independent. The response omits original input and execution capabilities.
   * @returns OperationResponse Account-authorized projection of customer business work.
   * @throws ApiError
   */
  public static getOperation({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Stable logical operation identity.
     */
    id: string,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operations/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Request cancellation of account-owned work.
   * Requires deploy:write. Expected generation prevents cancelling a recovered execution. Pending work can be cancelled; dispatching work may finish successfully or require reconciliation. A cancellation request does not prove an external effect was undone.
   * @returns OperationResponse Account cancellation intent and the resulting business projection.
   * @throws ApiError
   */
  public static cancelOperation({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Stable logical operation identity.
     */
    id: string,
    requestBody: OperationCancellationRequest,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/operations/{id}/cancel',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Start work owned by the authenticated customer.
   * Requires platform_tenant:operations:manage and an active customer identity. Idempotency-Key is required and scoped to account, app, environment, authenticated platform tenant and operation name. Canonically equivalent JSON inputs reuse the same operation across deployment changes; different input conflicts. Retention follows the admitted plan: Hobby/Pro/Scale results 7/30/90 days and deduplication 30/90/180 days. An expired retained result returns 410 during the deduplication window. Fresh work is unavailable on Free. Production admission remains disabled in this staged API.
   * @returns OperationAcceptedResponse Durable acceptance for this logical customer submission.
   * @throws ApiError
   */
  public static startPlatformTenantSelfOperation({
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Stable submission key; retries must preserve it and the input.
     */
    idempotencyKey: string,
    requestBody: OperationStartRequest,
  }): CancelablePromise<OperationAcceptedResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/customer-operations',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read this customer's operation.
   * Requires platform_tenant:operations:read. Missing and foreign operation identities return the same 404. Completion delivery failure does not change business success.
   * @returns OperationResponse Status, typed result and delivery visible to this owner.
   * @throws ApiError
   */
  public static getPlatformTenantSelfOperation({
    id,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/customer-operations/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read or subscribe to durable operation progress.
   * Requires platform_tenant:operations:read. application/json returns a bounded ordered page. Accept: text/event-stream streams durable events plus snapshot/resync frames; reconnect at least every five minutes with a current credential. after overrides Last-Event-ID. Authorization is rechecked during streams; revocation and suspension close them. When history expired or the cursor is ahead, a resync frame instructs a fresh status read. Event retention is Hobby/Pro/Scale 1/7/30 days; PostgreSQL notifications are only wake hints.
   * @returns OperationEventsResponse Retained ordered history with an explicit resynchronization indication.
   * @throws ApiError
   */
  public static getPlatformTenantSelfOperationEvents({
    id,
    after,
    lastEventId,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Last durably consumed event sequence.
     */
    after?: number,
    /**
     * Reconnect cursor when after is absent.
     */
    lastEventId?: string,
  }): CancelablePromise<OperationEventsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/customer-operations/{id}/events',
      path: {
        'id': id,
      },
      headers: {
        'Last-Event-ID': lastEventId,
      },
      query: {
        'after': after,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Request cancellation of this customer's operation.
   * Requires platform_tenant:operations:manage. Only authenticated ownership authorizes cancellation. Expected generation is required. Running side effects can still succeed or need reconciliation.
   * @returns OperationResponse Customer cancellation intent accepted for the current generation.
   * @throws ApiError
   */
  public static cancelPlatformTenantSelfOperation({
    id,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    requestBody: OperationCancellationRequest,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/customer-operations/{id}/cancel',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
}
