/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationAcceptedResponse } from '../models/OperationAcceptedResponse.js';
import type { OperationArtifactRequest } from '../models/OperationArtifactRequest.js';
import type { OperationCancellationRequest } from '../models/OperationCancellationRequest.js';
import type { OperationDefinitionResponse } from '../models/OperationDefinitionResponse.js';
import type { OperationDefinitionSpec } from '../models/OperationDefinitionSpec.js';
import type { OperationDefinitionsResponse } from '../models/OperationDefinitionsResponse.js';
import type { OperationDeliveryAttemptsResponse } from '../models/OperationDeliveryAttemptsResponse.js';
import type { OperationDeliveryInspection } from '../models/OperationDeliveryInspection.js';
import type { OperationDeliveryRetryRequest } from '../models/OperationDeliveryRetryRequest.js';
import type { OperationDeliveryRetryResponse } from '../models/OperationDeliveryRetryResponse.js';
import type { OperationDoctorResponse } from '../models/OperationDoctorResponse.js';
import type { OperationEventsResponse } from '../models/OperationEventsResponse.js';
import type { OperationExecutionsResponse } from '../models/OperationExecutionsResponse.js';
import type { OperationListResponse } from '../models/OperationListResponse.js';
import type { OperationMilestone } from '../models/OperationMilestone.js';
import type { OperationMilestoneRequest } from '../models/OperationMilestoneRequest.js';
import type { OperationMilestonesResponse } from '../models/OperationMilestonesResponse.js';
import type { OperationMilestoneValidationRequest } from '../models/OperationMilestoneValidationRequest.js';
import type { OperationMilestoneValidationResponse } from '../models/OperationMilestoneValidationResponse.js';
import type { OperationRecoveryRequest } from '../models/OperationRecoveryRequest.js';
import type { OperationReportRequest } from '../models/OperationReportRequest.js';
import type { OperationResponse } from '../models/OperationResponse.js';
import type { OperationStartRequest } from '../models/OperationStartRequest.js';
import type { OperationTenantIdentity } from '../models/OperationTenantIdentity.js';
import type { OperationWorkflowStateReport } from '../models/OperationWorkflowStateReport.js';
import type { OperationWorkflowStateReportResponse } from '../models/OperationWorkflowStateReportResponse.js';
import type { OperationWorkflowStateValidationRequest } from '../models/OperationWorkflowStateValidationRequest.js';
import type { OperationWorkflowStateValidationResponse } from '../models/OperationWorkflowStateValidationResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class OperationsService {
  /**
   * Read durable operation events as bounded JSON.
   * Requires account read scope and MFA. Resync_required instructs clients to refresh status when the watermark or event retention is stale. This operator endpoint returns JSON; customer SSE authorization remains separate.
   * @returns OperationEventsResponse Retained durable event page and the current sequence watermark.
   * @throws ApiError
   */
  public static getAccountOperationEvents({
    slug,
    id,
    after,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Last observed durable event sequence.
     */
    after?: number,
  }): CancelablePromise<OperationEventsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operations/{id}/events',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'after': after,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Inspect retained operation execution generations.
   * Requires account read scope and MFA. Rows are ordered by generation with attempt counts from the execution ledger. Payloads, headers and capabilities are omitted. Next_generation is the after watermark for the next page.
   * @returns OperationExecutionsResponse Ordered retained execution generations with ledger attempt counts.
   * @throws ApiError
   */
  public static getOperationExecutions({
    slug,
    id,
    after,
    limit = 20,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Last observed execution generation.
     */
    after?: number,
    /**
     * Maximum execution generations in this page.
     */
    limit?: number,
  }): CancelablePromise<OperationExecutionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operations/{id}/executions',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Retry a dead completion delivery without repeating work.
   * Requires deploy write scope and MFA. Atomically resets only a dead completion webhook delivery to pending. Pending, failed and in-flight deliveries retain their automatic retry policy and cannot be manually reset. Business state and execution generation stay unchanged; read status after an uncertain response.
   * @returns OperationResponse Business result with the independently reset notification projection.
   * @throws ApiError
   */
  public static retryOperationDelivery({
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
      method: 'POST',
      url: '/v1/apps/{slug}/operations/{id}/retry-delivery',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Reconcile an uncertain outcome or authorize safe retry.
   * Requires deploy:write and evidence. Expected generation prevents stale recovery; recovery_id makes the same recovery decision idempotent. succeeded validates result against the pinned output schema. safe_to_retry creates a new execution while retaining the logical operation identity and pinned contract. Ordinary invocation/DLQ replay cannot bypass this policy.
   * @returns OperationResponse Reconciled outcome or receipt of a newly authorized execution generation.
   * @throws ApiError
   */
  public static recoverOperation({
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
    requestBody: OperationRecoveryRequest,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/operations/{id}/recover',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Customer download of verified operation result bytes.
   * Requires platform_tenant:operations:read. Foreign artifacts and operations return the same 404. Only succeeded operations allow downloads. The API checks the retained object size and SHA-256 before serving verified bytes. Changed or missing retained private bytes make the artifact unavailable without rewriting the business outcome. The reference expires with the operation projection. No signed URL is stored.
   * @returns binary Verified file content authorized for this customer.
   * @throws ApiError
   */
  public static downloadPlatformTenantSelfOperationArtifact({
    id,
    artifact,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Attached result artifact identity.
     */
    artifact: string,
  }): CancelablePromise<Blob> {
    return __request(OpenAPI, {
      responseType: 'blob',
      method: 'GET',
      url: '/v1/platform-tenant-self/customer-operations/{id}/artifacts/{artifact}',
      path: {
        'id': id,
        'artifact': artifact,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Account download of verified operation result bytes.
   * Requires account read scope and app ownership. Only succeeded operations allow downloads. The API checks the retained object size and SHA-256 before serving verified bytes. Changed or missing retained private bytes make the artifact unavailable without rewriting the business outcome. The reference expires with the operation projection. No signed URL is stored.
   * @returns binary Verified file content authorized for this account and app.
   * @throws ApiError
   */
  public static downloadOperationArtifact({
    slug,
    id,
    artifact,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Attached result artifact identity.
     */
    artifact: string,
  }): CancelablePromise<Blob> {
    return __request(OpenAPI, {
      responseType: 'blob',
      method: 'GET',
      url: '/v1/apps/{slug}/operations/{id}/artifacts/{artifact}',
      path: {
        'slug': slug,
        'id': id,
        'artifact': artifact,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read retained business milestones for an account-owned Operation.
   * Requires account read scope and MFA. The app and Operation bind ownership; customer credentials cannot access this operator feed.
   * @returns OperationMilestonesResponse Account app Operation facts with retained page continuation.
   * @throws ApiError
   */
  public static getAccountOperationMilestones({
    slug,
    id,
    limit = 20,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationMilestonesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operations/{id}/milestones',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read this customer's retained Operation milestones.
   * Requires platform_tenant:operations:read. The authenticated customer must own the Operation. Retention follows business results, independently of generic event history.
   * @returns OperationMilestonesResponse Authenticated customer Operation facts and page continuation.
   * @throws ApiError
   */
  public static getPlatformTenantSelfOperationMilestones({
    id,
    limit = 20,
    cursor,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationMilestonesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/customer-operations/{id}/milestones',
      path: {
        'id': id,
      },
      query: {
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read a business entity timeline across related Operations.
   * Requires account read scope and MFA. Explicit app/environment/reference selectors and optional customer selection remain within account ownership. Paired workflow and workflow-instance selectors narrow the feed to one run. Facts are ordered by first platform publication time, not inferred business causality.
   * @returns OperationMilestonesResponse Account environment business-reference milestone feed.
   * @throws ApiError
   */
  public static listAccountBusinessMilestones({
    slug,
    scope,
    subjectType,
    subjectId,
    workflow,
    workflowInstanceId,
    tenantId,
    limit = 20,
    cursor,
    workflowStateCursor,
    staleOnly = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Explicit environment containing the related business Operations.
     */
    scope: string,
    /**
     * Business reference type for a timeline spanning related Operations.
     */
    subjectType: string,
    /**
     * Exact public business identifier for this timeline; maximum 256 UTF-8 bytes.
     */
    subjectId: string,
    /**
     * Optional workflow name. Supply with workflow_instance_id to read one workflow instance.
     */
    workflow?: string,
    /**
     * Optional app-provided workflow instance ID. Supply with workflow; the pair is bound into pagination.
     */
    workflowInstanceId?: string,
    /**
     * Optional account-owned customer selector on an operator milestone timeline.
     */
    tenantId?: string,
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
    /**
     * Independent continuation for retained state changes. Requires the paired workflow and workflow_instance_id selectors.
     */
    workflowStateCursor?: string,
    /**
     * When true, return only current workflow states that have passed an app-declared state_stale_after threshold. Applies only to workflow_states; milestone facts and state history are unchanged.
     */
    staleOnly?: boolean,
  }): CancelablePromise<OperationMilestonesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operation-milestones',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'workflow_instance_id': workflowInstanceId,
        'tenant_id': tenantId,
        'limit': limit,
        'cursor': cursor,
        'workflow_state_cursor': workflowStateCursor,
        'stale_only': staleOnly,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read a business entity timeline for the authenticated customer.
   * Requires platform_tenant:operations:read. Identity comes only from credentials. The app, environment, and paired public reference select related retained work. Paired workflow and workflow-instance selectors narrow the feed to one run. Opaque pagination binds all selectors; other customers with the same entity ID remain isolated.
   * @returns OperationMilestonesResponse Authenticated customer business-reference milestone feed.
   * @throws ApiError
   */
  public static listPlatformTenantSelfBusinessMilestones({
    appId,
    scope,
    subjectType,
    subjectId,
    workflow,
    workflowInstanceId,
    limit = 20,
    cursor,
    workflowStateCursor,
    staleOnly = false,
  }: {
    /**
     * App selected within the authenticated customer's milestone feed.
     */
    appId: string,
    /**
     * Explicit environment containing the related business Operations.
     */
    scope: string,
    /**
     * Business reference type for a timeline spanning related Operations.
     */
    subjectType: string,
    /**
     * Exact public business identifier for this timeline; maximum 256 UTF-8 bytes.
     */
    subjectId: string,
    /**
     * Optional workflow name. Supply with workflow_instance_id to read one workflow instance.
     */
    workflow?: string,
    /**
     * Optional app-provided workflow instance ID. Supply with workflow; the pair is bound into pagination.
     */
    workflowInstanceId?: string,
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
    /**
     * Independent continuation for retained state changes. Requires the paired workflow and workflow_instance_id selectors.
     */
    workflowStateCursor?: string,
    /**
     * When true, return only current workflow states that have passed an app-declared state_stale_after threshold. Applies only to workflow_states; milestone facts and state history are unchanged.
     */
    staleOnly?: boolean,
  }): CancelablePromise<OperationMilestonesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/customer-operation-milestones',
      query: {
        'app_id': appId,
        'scope': scope,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'workflow_instance_id': workflowInstanceId,
        'limit': limit,
        'cursor': cursor,
        'workflow_state_cursor': workflowStateCursor,
        'stale_only': staleOnly,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Publish a committed public business milestone.
   * Requires a current workload assertion and invocation attempt/capability. Validates the pinned schema, then deduplicates by logical Operation and milestone ID across recovery. Identical retries return the original fact; changed identity content conflicts. Publication never changes business completion or delivery state.
   * @returns OperationMilestone Committed business fact published or its original acknowledgement replayed.
   * @throws ApiError
   */
  public static reportOperationMilestone({
    id,
    xFaasInvocationId,
    xGregaleOperationAttempt,
    xGregaleOperationCapability,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current invocation UUID supplied by trusted dispatch.
     */
    xFaasInvocationId: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Ephemeral 256-bit claim capability supplied only to the active handler.
     */
    xGregaleOperationCapability: string,
    requestBody: OperationMilestoneRequest,
  }): CancelablePromise<OperationMilestone> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/operations/{id}/milestones',
      path: {
        'id': id,
      },
      headers: {
        'X-Faas-Invocation-Id': xFaasInvocationId,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Capability': xGregaleOperationCapability,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Validate public milestones before the application transaction commits.
   * Requires the active workload and invocation claim. This bounded, read-only check validates pinned declarations, payload schemas, duplicate identity and remaining milestone capacity. Failure allows the SDK to roll back business writes; success does not publish or reserve capacity.
   * @returns OperationMilestoneValidationResponse Submitted milestone contracts validated under the current execution claim.
   * @throws ApiError
   */
  public static validateOperationMilestones({
    id,
    xFaasInvocationId,
    xGregaleOperationAttempt,
    xGregaleOperationCapability,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current invocation UUID supplied by trusted dispatch.
     */
    xFaasInvocationId: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Ephemeral 256-bit claim capability supplied only to the active handler.
     */
    xGregaleOperationCapability: string,
    requestBody: OperationMilestoneValidationRequest,
  }): CancelablePromise<OperationMilestoneValidationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/operations/{id}/milestones/validate',
      path: {
        'id': id,
      },
      headers: {
        'X-Faas-Invocation-Id': xFaasInvocationId,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Capability': xGregaleOperationCapability,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Publish an app-reported business workflow state.
   * Requires the current workload and invocation claim. The state name must be declared by the pinned workflow definition. Revisions assigned inside the application transaction prevent late older publications from replacing a newer state.
   * @returns OperationWorkflowStateReportResponse State update accepted idempotently, even when a newer revision is already current.
   * @throws ApiError
   */
  public static reportOperationWorkflowState({
    id,
    xFaasInvocationId,
    xGregaleOperationAttempt,
    xGregaleOperationCapability,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current invocation UUID supplied by trusted dispatch.
     */
    xFaasInvocationId: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Ephemeral 256-bit claim capability supplied only to the active handler.
     */
    xGregaleOperationCapability: string,
    requestBody: OperationWorkflowStateReport,
  }): CancelablePromise<OperationWorkflowStateReportResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/operations/{id}/workflow-states',
      path: {
        'id': id,
      },
      headers: {
        'X-Faas-Invocation-Id': xFaasInvocationId,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Capability': xGregaleOperationCapability,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Validate app-reported workflow states before transaction commit.
   * Requires the active workload and invocation claim. Validates the declared workflow/state vocabulary, business reference, instance ID and positive transaction-assigned revisions without publishing or reserving capacity.
   * @returns OperationWorkflowStateValidationResponse All candidate state updates passed validation under the current execution claim.
   * @throws ApiError
   */
  public static validateOperationWorkflowStates({
    id,
    xFaasInvocationId,
    xGregaleOperationAttempt,
    xGregaleOperationCapability,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current invocation UUID supplied by trusted dispatch.
     */
    xFaasInvocationId: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Ephemeral 256-bit claim capability supplied only to the active handler.
     */
    xGregaleOperationCapability: string,
    requestBody: OperationWorkflowStateValidationRequest,
  }): CancelablePromise<OperationWorkflowStateValidationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/operations/{id}/workflow-states/validate',
      path: {
        'id': id,
      },
      headers: {
        'X-Faas-Invocation-Id': xFaasInvocationId,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Capability': xGregaleOperationCapability,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Report progress from the current workload attempt.
   * Requires a fresh platform-issued workload JWT with audience gregale:operations and matching account, app and instance, plus current dispatch proof. Stale attempts, expired leases and customer credentials are denied. report_id retries are idempotent; changed report contents conflict. Stages must match the pinned definition.
   * @returns OperationResponse Updated projection after accepting or replaying the fenced report.
   * @throws ApiError
   */
  public static reportOperationProgress({
    id,
    xFaasInvocationId,
    xGregaleOperationAttempt,
    xGregaleOperationCapability,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current invocation UUID supplied by trusted dispatch.
     */
    xFaasInvocationId: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Ephemeral 256-bit claim capability supplied only to the active handler.
     */
    xGregaleOperationCapability: string,
    requestBody: OperationReportRequest,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/operations/{id}/progress',
      path: {
        'id': id,
      },
      headers: {
        'X-Faas-Invocation-Id': xFaasInvocationId,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Capability': xGregaleOperationCapability,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Attach verified managed-object result bytes.
   * Requires workload JWT and current dispatch proof as for progress. The object must be in a ready, private, account/app/environment-owned managed bucket. Exact size and SHA-256 are verified before the attachment and durable event commit. Repeated report_id with the same declaration is idempotent. A verified copy is retained under a private platform key; subsequent source mutation or deletion does not change the retained result.
   * @returns OperationResponse Updated projection containing the verified managed-object reference.
   * @throws ApiError
   */
  public static attachOperationArtifact({
    id,
    xFaasInvocationId,
    xGregaleOperationAttempt,
    xGregaleOperationCapability,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current invocation UUID supplied by trusted dispatch.
     */
    xFaasInvocationId: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Ephemeral 256-bit claim capability supplied only to the active handler.
     */
    xGregaleOperationCapability: string,
    requestBody: OperationArtifactRequest,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/operations/{id}/artifacts',
      path: {
        'id': id,
      },
      headers: {
        'X-Faas-Invocation-Id': xFaasInvocationId,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Capability': xGregaleOperationCapability,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List retained customer operations for an account-owned app.
   * Requires account read scope and MFA. Explicit scope and optional tenant filter select data within account ownership. Descending keyset pages exclude private inputs and results. Customer tokens cannot access this operator route.
   * @returns OperationListResponse Account app history page with optional tenant selection.
   * @throws ApiError
   */
  public static listAccountOperations({
    slug,
    scope,
    tenantId,
    name,
    state,
    subjectType,
    subjectId,
    limit = 20,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment environment whose customer operations are listed.
     */
    scope: string,
    /**
     * Optional account-owned platform tenant filter.
     */
    tenantId?: string,
    /**
     * Operation definition name.
     */
    name?: string,
    /**
     * Business state filter.
     */
    state?: 'accepted' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation',
    /**
     * Business reference type for this account-owned app. Requires subject_id; supply both selectors together.
     */
    subjectType?: string,
    /**
     * Exact public entity identifier within the account app and environment. Requires subject_type. Maximum 256 UTF-8 bytes; ASCII controls are rejected.
     */
    subjectId?: string,
    /**
     * Number of account-owned operation summaries to include.
     */
    limit?: number,
    /**
     * Opaque cursor bound to the account and exact filters.
     */
    cursor?: string,
  }): CancelablePromise<OperationListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operations',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'name': name,
        'state': state,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
        410: `The retained result expired; its identity remains reserved for the deduplication window.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Inspect completion delivery independently of the business result.
   * Requires account read scope and MFA. Business work is never restarted. Reads remain available with admission closed. Retention expiry returns 410. Receiver observations are local ledger snapshots, not connectivity probes. Raw receiver errors, target URLs and payloads are excluded.
   * @returns OperationDeliveryInspection Current completion delivery and independently retained business outcome.
   * @throws ApiError
   */
  public static getOperationDelivery({
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
  }): CancelablePromise<OperationDeliveryInspection> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operations/{id}/delivery',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        410: `The operation retention window for delivery inspection has expired.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read retained notification attempts for one owned operation.
   * Requires account read scope and MFA. Business work is never restarted. Attempt history stays readable while admission is closed. Expired operations return 410. This endpoint pages the existing notification ledger and excludes raw errors, destination URLs and payloads.
   * @returns OperationDeliveryAttemptsResponse Retained completion attempt evidence and an optional continuation cursor.
   * @throws ApiError
   */
  public static getOperationDeliveryAttempts({
    slug,
    id,
    limit = 20,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Maximum retained notification attempts in this page, newest replay and attempt first.
     */
    limit?: number,
    /**
     * Opaque continuation bound to this operation and its completion delivery.
     */
    cursor?: string,
  }): CancelablePromise<OperationDeliveryAttemptsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operations/{id}/delivery-attempts',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        410: `The operation retention window for attempt history has expired.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Commit an idempotent decision to retry a dead completion delivery.
   * Requires account deploy write scope and MFA. Business work is never restarted. Retry IDs are scoped to the retained operation, with at most 32 decisions. An exact replay returns the immutable original receipt even after later delivery failures or delivery cleanup. Changed payloads and stale replay generations conflict. Receipt insertion and notification reset are atomic; receiver cooldowns and destination policy remain in force. Queued denotes the recorded decision, not live delivery status.
   * @returns OperationDeliveryRetryResponse Immutable retry decision; use the delivery read for current transport state.
   * @throws ApiError
   */
  public static retryOperationDeliveryWithReceipt({
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
    requestBody: OperationDeliveryRetryRequest,
  }): CancelablePromise<OperationDeliveryRetryResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/operations/{id}/delivery-retries',
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
        409: `code: conflict`,
        410: `The operation retention window for completion retry has expired.`,
        413: `Retry request exceeds 4096 bytes.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Observe Operations submission prerequisites on the responding API node.
   * Account read scope and MFA are required. Select an owned deployment and tenant, optionally one operation name. This read reserves no quota, grants no admission, probes no external services and does not qualify native lifecycle or fleet availability. Completion warnings are independent of submission blockers. Eligibility is an advisory observation, not an execution guarantee.
   * @returns OperationDoctorResponse Bounded read-only report, including blocked or unverified prerequisites.
   * @throws ApiError
   */
  public static getOperationDoctor({
    slug,
    deploymentId,
    tenantId,
    name,
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
     * Owned platform tenant whose submission prerequisites will be observed.
     */
    tenantId: string,
    /**
     * Optional immutable operation name; omit to inspect the deployment's definitions.
     */
    name?: string,
  }): CancelablePromise<OperationDoctorResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployments/{deployment_id}/operation-doctor',
      path: {
        'slug': slug,
        'deployment_id': deploymentId,
      },
      query: {
        'tenant_id': tenantId,
        'name': name,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List immutable Operations contracts for one owned deployment.
   * Requires account read scope and MFA. Deployment identity selects the environment and pins; reads remain available while admission is closed. No mutable active-deployment selector is inferred.
   * @returns OperationDefinitionsResponse Name-ordered contracts, bounded by the deployment definition allowance.
   * @throws ApiError
   */
  public static listOperationDefinitions({
    slug,
    deploymentId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment owning the contract.
     */
    deploymentId: string,
  }): CancelablePromise<OperationDefinitionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployments/{deployment_id}/operation-definitions',
      path: {
        'slug': slug,
        'deployment_id': deploymentId,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read one named contract pinned to an owned deployment.
   * Requires account read scope and MFA. Returns the immutable definition ID, revision, schemas, deployment, environment and completion destination, without changing admission.
   * @returns OperationDefinitionResponse Resolved immutable contract selected by deployment and name.
   * @throws ApiError
   */
  public static getOperationDefinition({
    slug,
    deploymentId,
    name,
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
  }): CancelablePromise<OperationDefinitionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployments/{deployment_id}/operation-definitions/{name}',
      path: {
        'slug': slug,
        'deployment_id': deploymentId,
        'name': name,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Discover and reopen this customer's retained work.
   * Requires platform_tenant:operations:read and an explicit app_id and environment scope. Identity comes only from the authenticated tenant token. Pages contain status summaries, never source input, result bytes, artifact locations or execution authority. Active work remains visible; expired settled work is omitted. Creation time and ID determine descending order. An opaque cursor is bound to the account, tenant, app, scope and filters. This is a live view, not a transaction snapshot: state filters and retention can change membership. Closed preview admission does not disable history reads.
   * @returns OperationListResponse Customer-scoped summaries and an optional continuation cursor.
   * @throws ApiError
   */
  public static listPlatformTenantSelfOperations({
    appId,
    scope,
    name,
    subjectType,
    subjectId,
    state,
    limit = 20,
    cursor,
  }: {
    /**
     * Owning app identifier, scoped to the authenticated customer.
     */
    appId: string,
    /**
     * Exact deployment environment; no all-environments selector.
     */
    scope: string,
    /**
     * Stable operation name across definition revisions.
     */
    name?: string,
    /**
     * Exact business reference type. Requires subject_id; both must be supplied together.
     */
    subjectType?: string,
    /**
     * Exact business reference ID, within the authenticated owner/app/environment. Requires subject_type. Maximum 256 UTF-8 bytes; ASCII controls are rejected.
     */
    subjectId?: string,
    /**
     * Current business state filter, independent of delivery.
     */
    state?: 'accepted' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation',
    /**
     * Maximum summaries in this page.
     */
    limit?: number,
    /**
     * Opaque continuation from the same identity and filters.
     */
    cursor?: string,
  }): CancelablePromise<OperationListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/customer-operations',
      query: {
        'app_id': appId,
        'scope': scope,
        'name': name,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'state': state,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Verify the authenticated customer identity for submission receipts.
   * Requires platform_tenant:operations:manage and a tenant credential. Account credentials cannot supply a tenant selector. Identity verification remains available while admission is closed.
   * @returns OperationTenantIdentity Verified account and tenant IDs without the bearer credential.
   * @throws ApiError
   */
  public static getPlatformTenantSelfOperationIdentity(): CancelablePromise<OperationTenantIdentity> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/customer-operations/identity',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
}
