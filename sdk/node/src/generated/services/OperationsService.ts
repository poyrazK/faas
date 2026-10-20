/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationAcceptedResponse } from '../models/OperationAcceptedResponse.js';
import type { OperationApprovalRequest } from '../models/OperationApprovalRequest.js';
import type { OperationArtifactRequest } from '../models/OperationArtifactRequest.js';
import type { OperationArtifactUploadRequest } from '../models/OperationArtifactUploadRequest.js';
import type { OperationArtifactUploadResponse } from '../models/OperationArtifactUploadResponse.js';
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
import type { OperationExecutionControlResponse } from '../models/OperationExecutionControlResponse.js';
import type { OperationExecutionsResponse } from '../models/OperationExecutionsResponse.js';
import type { OperationJobArtifactResponse } from '../models/OperationJobArtifactResponse.js';
import type { OperationJobControlResponse } from '../models/OperationJobControlResponse.js';
import type { OperationJobReportRequest } from '../models/OperationJobReportRequest.js';
import type { OperationListResponse } from '../models/OperationListResponse.js';
import type { OperationMilestone } from '../models/OperationMilestone.js';
import type { OperationMilestoneRequest } from '../models/OperationMilestoneRequest.js';
import type { OperationMilestonesResponse } from '../models/OperationMilestonesResponse.js';
import type { OperationMilestoneValidationRequest } from '../models/OperationMilestoneValidationRequest.js';
import type { OperationMilestoneValidationResponse } from '../models/OperationMilestoneValidationResponse.js';
import type { OperationRecoveryDecision } from '../models/OperationRecoveryDecision.js';
import type { OperationRecoveryInspection } from '../models/OperationRecoveryInspection.js';
import type { OperationRecoveryPreview } from '../models/OperationRecoveryPreview.js';
import type { OperationRecoveryPreviewRequest } from '../models/OperationRecoveryPreviewRequest.js';
import type { OperationRecoveryRequest } from '../models/OperationRecoveryRequest.js';
import type { OperationReportRequest } from '../models/OperationReportRequest.js';
import type { OperationResponse } from '../models/OperationResponse.js';
import type { OperationSignalReceipt } from '../models/OperationSignalReceipt.js';
import type { OperationSignalRequest } from '../models/OperationSignalRequest.js';
import type { OperationStartRequest } from '../models/OperationStartRequest.js';
import type { OperationSubjectSignalRequest } from '../models/OperationSubjectSignalRequest.js';
import type { OperationSubjectSignalResponse } from '../models/OperationSubjectSignalResponse.js';
import type { OperationSubmissionLookupRequest } from '../models/OperationSubmissionLookupRequest.js';
import type { OperationSubmissionLookupResponse } from '../models/OperationSubmissionLookupResponse.js';
import type { OperationTenantIdentity } from '../models/OperationTenantIdentity.js';
import type { OperationTimelineResponse } from '../models/OperationTimelineResponse.js';
import type { OperationWorkflowActionPreviewRequest } from '../models/OperationWorkflowActionPreviewRequest.js';
import type { OperationWorkflowActionPreviewResponse } from '../models/OperationWorkflowActionPreviewResponse.js';
import type { OperationWorkflowArtifactResponse } from '../models/OperationWorkflowArtifactResponse.js';
import type { OperationWorkflowAttentionResponse } from '../models/OperationWorkflowAttentionResponse.js';
import type { OperationWorkflowAttentionSummary } from '../models/OperationWorkflowAttentionSummary.js';
import type { OperationWorkflowCase } from '../models/OperationWorkflowCase.js';
import type { OperationWorkflowControlResponse } from '../models/OperationWorkflowControlResponse.js';
import type { OperationWorkflowCoordinationTimeline } from '../models/OperationWorkflowCoordinationTimeline.js';
import type { OperationWorkflowOutcomesResponse } from '../models/OperationWorkflowOutcomesResponse.js';
import type { OperationWorkflowOutcomeSummary } from '../models/OperationWorkflowOutcomeSummary.js';
import type { OperationWorkflowPerformanceInstancesResponse } from '../models/OperationWorkflowPerformanceInstancesResponse.js';
import type { OperationWorkflowPerformanceSummary } from '../models/OperationWorkflowPerformanceSummary.js';
import type { OperationWorkflowPlaybookEvidenceWatch } from '../models/OperationWorkflowPlaybookEvidenceWatch.js';
import type { OperationWorkflowPlaybookEvidenceWatchRequest } from '../models/OperationWorkflowPlaybookEvidenceWatchRequest.js';
import type { OperationWorkflowPlaybookProgress } from '../models/OperationWorkflowPlaybookProgress.js';
import type { OperationWorkflowPlaybookProgressStartRequest } from '../models/OperationWorkflowPlaybookProgressStartRequest.js';
import type { OperationWorkflowPlaybookProgressUpdateRequest } from '../models/OperationWorkflowPlaybookProgressUpdateRequest.js';
import type { OperationWorkflowPlaybookReviewRequest } from '../models/OperationWorkflowPlaybookReviewRequest.js';
import type { OperationWorkflowReadinessComparisonRequest } from '../models/OperationWorkflowReadinessComparisonRequest.js';
import type { OperationWorkflowReadinessComparisonResponse } from '../models/OperationWorkflowReadinessComparisonResponse.js';
import type { OperationWorkflowReadinessCoordinationRequest } from '../models/OperationWorkflowReadinessCoordinationRequest.js';
import type { OperationWorkflowReadinessFollowUpsResponse } from '../models/OperationWorkflowReadinessFollowUpsResponse.js';
import type { OperationWorkflowReadinessInboxResponse } from '../models/OperationWorkflowReadinessInboxResponse.js';
import type { OperationWorkflowReadinessNotification } from '../models/OperationWorkflowReadinessNotification.js';
import type { OperationWorkflowReadinessNotificationCheck } from '../models/OperationWorkflowReadinessNotificationCheck.js';
import type { OperationWorkflowReadinessQueueResponse } from '../models/OperationWorkflowReadinessQueueResponse.js';
import type { OperationWorkflowReadinessRequest } from '../models/OperationWorkflowReadinessRequest.js';
import type { OperationWorkflowReadinessResponse } from '../models/OperationWorkflowReadinessResponse.js';
import type { OperationWorkflowReadinessSubscription } from '../models/OperationWorkflowReadinessSubscription.js';
import type { OperationWorkflowReadinessSubscriptionRequest } from '../models/OperationWorkflowReadinessSubscriptionRequest.js';
import type { OperationWorkflowReadinessSubscriptionsResponse } from '../models/OperationWorkflowReadinessSubscriptionsResponse.js';
import type { OperationWorkflowStateReport } from '../models/OperationWorkflowStateReport.js';
import type { OperationWorkflowStateReportResponse } from '../models/OperationWorkflowStateReportResponse.js';
import type { OperationWorkflowStateValidationRequest } from '../models/OperationWorkflowStateValidationRequest.js';
import type { OperationWorkflowStateValidationResponse } from '../models/OperationWorkflowStateValidationResponse.js';
import type { PlatformTenantSubjectSignalRequest } from '../models/PlatformTenantSubjectSignalRequest.js';
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
   * Read one chronological history of an operation.
   * Requires account read scope and MFA. Merges operation events, HTTP execution attempts, workflow step attempts and completion delivery attempts by time (ADR-1195). Each source pages after its own position, so pages never skip or repeat a record while the operation runs. Attempt entries are snapshots at read time. HTTP attempts carry an instance log window for the archived log route; workflow step attempts do not. Platform tenants read events instead.
   * @returns OperationTimelineResponse Chronological timeline page with a continuation cursor while records remain.
   * @throws ApiError
   */
  public static getOperationTimeline({
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
     * Maximum timeline entries in this page.
     */
    limit?: number,
    /**
     * Operation-bound continuation cursor from next_cursor.
     */
    cursor?: string,
  }): CancelablePromise<OperationTimelineResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operations/{id}/timeline',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
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
   * Inspect recovery evidence without changing execution.
   * Account read scope and MFA are required. Returns the pinned execution, confirmed steps, uncertain attempts and private file metadata from a consistent durable snapshot. No payloads, raw errors, object locations or capabilities are returned. Inspection reads never renew leases, consume quotas or publish files. Available while new admission is closed.
   * @returns OperationRecoveryInspection Recovery inspection with durable execution and retained file metadata.
   * @throws ApiError
   */
  public static inspectOperationRecovery({
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
  }): CancelablePromise<OperationRecoveryInspection> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operations/{id}/recovery-inspection',
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
   * Preview an operation recovery decision.
   * Account read scope and MFA are required. Validates a proposed resolution without starting work, recording a receipt, consuming quotas or publishing files. Workflow retries use the native resume planner. Eligible means current platform checks pass; external effects still require operator evidence. Apply revalidates under writer locks. Expected generation rejects stale previews; the inspection revision can optionally fence the later apply request. Available while new admission is closed.
   * @returns OperationRecoveryPreview Read-only recovery proposal with current eligibility and planned reuse.
   * @throws ApiError
   */
  public static previewOperationRecovery({
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
    requestBody: OperationRecoveryPreviewRequest,
  }): CancelablePromise<OperationRecoveryPreview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/operations/{id}/recovery-preview',
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
   * Reconcile an uncertain outcome or authorize safe retry.
   * Requires deploy:write and evidence. Expected generation prevents stale recovery; optional expected_inspection_revision also rejects changed execution evidence. Matching recovery receipts replay before revision validation. recovery_id makes the same recovery decision idempotent. succeeded validates result against the pinned output schema. safe_to_retry creates a new execution while retaining the logical operation identity and pinned contract. Ordinary invocation/DLQ replay cannot bypass this policy.
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
   * Record or replay an immutable recovery decision acknowledgement.
   * Account-only; requires deploy:write and MFA when enabled. Uses the same evidence, generation, optional inspection revision, output, quota and pinned execution fences as recover. The decision is committed atomically with recovery. Matching recovery_id and canonical request replay the original decision even after work advances; changed intent returns 409. The acknowledgement describes state at acceptance, not current status or completed work. expires_at is the retention deadline before this decision; later recovery cannot extend it. Old accepted recoveries without retained decision acknowledgements return 409 operation_recovery_receipt_unavailable and are never repeated. Expired receipts return 410.
   * @returns OperationRecoveryDecision Immutable original decision acknowledgement; read current operation status separately.
   * @throws ApiError
   */
  public static recoverOperationWithReceipt({
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
  }): CancelablePromise<OperationRecoveryDecision> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/operations/{id}/recover-receipt',
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
   * Observe cancellation and time bounds for the current HTTP attempt.
   * Requires a fresh platform-issued workload JWT with audience gregale:operations and matching account, app and instance, plus current dispatch proof. Returns one fenced observation of cancellation intent, the admitted deadline and current lease. The read does not renew the lease, settle work, consume progress reports or authorize external effects. Expired deadlines, stale attempts and customer credentials are denied. Remains available when new operation admission is closed. Handlers cooperate with cancellation; stopping after dispatch does not prove that an external effect did not happen.
   * @returns OperationExecutionControlResponse Read-only live claim observation. Cache-Control is no-store.
   * @throws ApiError
   */
  public static getOperationExecutionControl({
    id,
    xFaasInvocationId,
    xGregaleOperationAttempt,
    xGregaleOperationCapability,
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
  }): CancelablePromise<OperationExecutionControlResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/runtime/operations/{id}/control',
      path: {
        'id': id,
      },
      headers: {
        'X-Faas-Invocation-Id': xFaasInvocationId,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Capability': xGregaleOperationCapability,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * List completed workflows with explicit business outcomes.
   * Requires account read scope and MFA. Optional tenant selection remains within account ownership. Explicit app and environment are required. Only latest retained terminal snapshots with reported outcomes are included; each instance counts once. Reopened instances leave these totals. Earlier outcomes remain in retained workflow history. Terminal state alone does not imply an outcome. Totals cover all matching instances independently of group pagination; cursors are separate from the attention queue. Reports and retention may change during browsing.
   * @returns OperationWorkflowOutcomesResponse A bounded page ordered by latest report publication time descending.
   * @throws ApiError
   */
  public static listAccountWorkflowOutcomes({
    slug,
    scope,
    workflow,
    code,
    tenantId,
    limit = 20,
    cursor,
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
     * For the account outcome queue, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the account outcome queue, restrict terminal results to this explicit business outcome code.
     */
    code?: string,
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
  }): CancelablePromise<OperationWorkflowOutcomesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-outcomes',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'code': code,
        'tenant_id': tenantId,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List ranked contributors to a workflow performance group.
   * Requires account read scope and MFA. Optional tenant selection stays within account ownership. Explicit app/environment/workflow and cohort/dimension selection are required. Uses the same latest 100 instances per cohort and complete-history eligibility as the performance summary. State groups require state and contract_version; blocker groups require operation/code/contract_version and exactly one of owner or unassigned=true; verification_owner groups require exactly one of owner or unassigned=true. Overall dimensions reject group selectors. An optional cohort_token binds the evaluation time and retained evidence to a prior summary; changed or expired cohorts return 409 and require refreshing the summary. Without a token this evaluates a fresh cohort. Returns every matching eligible contributor up to 100 sorted by observed duration descending with stable identity ties. No cursor is supported. Current blockers can differ from historical contributing groups; pending verification previews are bounded independently of exact counts. Decision dimensions require completed cohort and cohort_token, an exact kind/contract_version/outcome_code and decision_code/rule_id/rule_version for decision groups. Path groups additionally require group_id/path_id; the decision triple is either fully supplied or fully omitted. The exact group must be present in the bounded decision_outcomes summary. Decision dimensions reject state/blocker/owner selectors. Returns the earliest qualifying report and optional business decision fact per contributor; reports after the first terminal revision do not qualify. Condition dimensions also require completed cohort and cohort_token and an exact returned condition_outcomes group selected by contract_version and decision_code/rule_id/rule_version and condition_id/condition_description/condition_result and outcome_code. They reject kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed results can produce overlapping groups. The earliest qualifying fact and all results observed for that exact identity are returned. Missing conditions are not classified as unknown. Active condition dimensions require ongoing cohort and cohort_token, an exact returned active_conditions identity selected by contract_version and decision_code/rule_id/rule_version and condition_id/condition_description/condition_result. Outcome and kind/path/state/blocker/owner selectors are rejected. They use latest explicit decision snapshots in the current contract, return evidence age at the bound evaluation time and measure whole-workflow elapsed or blocked duration. Metadata-only reports preserve evidence; explicit condition omission clears prior results. The optional condition_evidence_status selector restricts active contributors to within_budget, overdue or no_budget. Status counters partition each returned group and the chosen status must have contributors. Budgets come from the reporting Operation pinned definition; deadlines measure report age, not business verification age. The condition_refresh_owner dimension requires ongoing cohort and cohort_token and an exact returned active_conditions.refresh_owners group selected by exactly one of owner or unassigned=true. It rejects contract/state/blocker/condition/result/status and decision/path/outcome selectors. Contributors are ranked by whole-workflow blocked duration and include bounded overdue condition facts with pinned refresh guidance; counts remain exact beyond the preview limit. The condition_refresh_age and condition_refresh_blocked dimensions require ongoing cohort and cohort_token and reject all group selectors. Each returns eligible workflows with at least one overdue selected condition once, across all refresh owners. The former ranks by oldest overdue report age in whole seconds, the latter by full workflow blocked duration; stable workflow identity breaks ties. These overall rankings remain available with zero contributors and are independent of condition and owner display caps. Each contributor includes refresh_priority with the oldest qualifying fact, complete assigned owner list, exact unassigned count and explanation, plus the bounded condition preview. Age ranking does not mean most past deadline or highest business severity. Conflicting snapshots and incomplete histories are excluded. The condition_missing_owner dimension requires ongoing cohort and cohort_token and an exact returned condition_coverage.missing_groups row selected by contract_version, current state and exactly one of owner or unassigned=true. All condition/result/status, decision/path/outcome and blocker selectors are rejected. Counts and eligibility use full condition coverage before previews. Each workflow appears once in its selected group and ranks by full observed workflow blocked duration with stable identity ties. Missing evidence has no report timestamp and is not failed verification; expectations come from the latest reporting Operation pinned definition. Missing condition entries and pinned guidance are previewed up to 16 with exact counts and explicit truncation. The condition_gap_owner dimension requires ongoing cohort, cohort_token and baseline_token bound to the same scope. The baseline must not be later than the current token evaluation. Select an exact returned new_missing_progress.groups row with contract_version, current state, gap_status and exactly one of owner or unassigned=true. All other selectors are rejected; dimensions outside condition_gap_owner and condition_gap_queue reject baseline_token and gap_status. Counts and grouping precede previews; each contributor includes up to 16 matching gap_progress changes with exact counts, prior evidence and pinned guidance. Current report comparisons permit contract boundaries while baseline history must be continuous within its contract; ranking uses full observed report-only blocked intervals without changing eligibility for existing duration groups. Generic duration coverage remains separate from new_missing_progress coverage. The condition_gap_queue dimension requires ongoing cohort and both cohort_token and baseline_token with the same comparison scope and baseline time rules. It returns each eligible workflow once, independent of displayed group caps. Optional gap_status and exactly one of owner or unassigned=true filter matching current gaps; both may be omitted. Contract/state/blocker/condition/decision/path/outcome selectors are rejected. Empty results are valid. Each contributor includes gap_queue_priority with all category counts, sorted assigned owners, exact unassigned count, all pinned missing expectation guidance and a ranking explanation before filters or previews. gap_progress counts and previews contain only filtered gaps. Ranking measures observed report-only blocked intervals, omitting intervals spanning contract boundaries, with stable workflow identity ties.
   * @returns OperationWorkflowPerformanceInstancesResponse Ranked complete-history contributors and exact selected group distribution.
   * @throws ApiError
   */
  public static listAccountWorkflowPerformanceInstances({
    slug,
    scope,
    workflow,
    cohort,
    dimension,
    tenantId,
    baselineToken,
    gapStatus,
    contractVersion,
    state,
    operation,
    code,
    owner,
    unassigned,
    cohortToken,
    kind,
    decisionCode,
    ruleId,
    ruleVersion,
    groupId,
    pathId,
    conditionId,
    conditionDescription,
    conditionEvidenceStatus,
    conditionResult,
    outcomeCode,
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
     * Declared business workflow to investigate.
     */
    workflow: string,
    /**
     * Completed or ongoing cohort.
     */
    cohort: 'completed' | 'ongoing',
    /**
     * Overall measure or exact breakdown dimension.
     */
    dimension: 'state_time' | 'blocked_time' | 'verification_wait' | 'state' | 'blocker' | 'verification_owner' | 'decision_completion' | 'decision_blocked' | 'condition_completion' | 'condition_blocked' | 'active_condition_elapsed' | 'active_condition_blocked' | 'condition_refresh_owner' | 'condition_refresh_age' | 'condition_refresh_blocked' | 'condition_missing_owner' | 'condition_gap_owner' | 'condition_gap_queue',
    /**
     * Optional account-owned customer selector on an operator milestone timeline.
     */
    tenantId?: string,
    /**
     * Saved account performance baseline; required for condition_gap_owner and condition_gap_queue; rejected by other dimensions.
     */
    baselineToken?: string,
    /**
     * Exact account returned reporting-gap category; required for condition_gap_owner, optional for condition_gap_queue.
     */
    gapStatus?: 'report_lost' | 'new_expectation' | 'still_missing' | 'expectation_changed' | 'contract_changed',
    /**
     * Required for state and blocker dimensions.
     */
    contractVersion?: number,
    /**
     * Exact contributing state. Required for the state dimension.
     */
    state?: string,
    /**
     * Exact blocker target Operation. Required for the blocker dimension.
     */
    operation?: string,
    /**
     * Exact blocker code. Required for the blocker dimension.
     */
    code?: string,
    /**
     * Exact public owner. Mutually exclusive with unassigned.
     */
    owner?: string,
    /**
     * Select the empty owner group. Mutually exclusive with owner.
     */
    unassigned?: boolean,
    /**
     * Opaque selector-bound cohort token from the summary or a previous contributor response.
     */
    cohortToken?: string,
    /**
     * Decision outcome group kind.
     */
    kind?: 'decision' | 'path',
    /**
     * Exact reported decision code for decision outcome dimensions.
     */
    decisionCode?: string,
    /**
     * Exact rule ID for decision outcome dimensions.
     */
    ruleId?: string,
    /**
     * Exact rule version for decision outcome dimensions.
     */
    ruleVersion?: string,
    /**
     * Exact selected prerequisite group for path outcome dimensions.
     */
    groupId?: string,
    /**
     * Exact selected path for path outcome dimensions.
     */
    pathId?: string,
    /**
     * Exact reported condition ID for account condition outcome dimensions.
     */
    conditionId?: string,
    /**
     * Exact public condition description for the account returned condition group; UTF-8 byte bound without controls.
     */
    conditionDescription?: string,
    /**
     * Account active-condition report freshness filter. Only ongoing active condition dimensions accept it; the selected status must have contributors in the returned group.
     */
    conditionEvidenceStatus?: 'within_budget' | 'overdue' | 'no_budget',
    /**
     * Exact observed condition result for the account condition outcome group.
     */
    conditionResult?: 'pass' | 'fail' | 'unknown',
    /**
     * Exact latest reported outcome for decision outcome dimensions.
     */
    outcomeCode?: string,
  }): CancelablePromise<OperationWorkflowPerformanceInstancesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-performance/instances',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'tenant_id': tenantId,
        'cohort': cohort,
        'dimension': dimension,
        'baseline_token': baselineToken,
        'gap_status': gapStatus,
        'contract_version': contractVersion,
        'state': state,
        'operation': operation,
        'code': code,
        'owner': owner,
        'unassigned': unassigned,
        'cohort_token': cohortToken,
        'kind': kind,
        'decision_code': decisionCode,
        'rule_id': ruleId,
        'rule_version': ruleVersion,
        'group_id': groupId,
        'path_id': pathId,
        'condition_id': conditionId,
        'condition_description': conditionDescription,
        'condition_evidence_status': conditionEvidenceStatus,
        'condition_result': conditionResult,
        'outcome_code': outcomeCode,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The retained cohort changed. Refresh the summary and use its new cohort_token.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List ranked performance contributors for the authenticated customer.
   * Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides are rejected. Explicit app/environment/workflow and cohort/dimension selection are required. Uses the same latest 100 instances per cohort and complete-history eligibility as the performance summary. State groups require state and contract_version; blocker groups require operation/code/contract_version and exactly one of owner or unassigned=true; verification_owner groups require exactly one of owner or unassigned=true. Overall dimensions reject group selectors. An optional cohort_token binds the evaluation time and retained evidence to a prior summary; changed or expired cohorts return 409 and require refreshing the summary. Without a token this evaluates a fresh cohort. Returns every matching eligible contributor up to 100 sorted by observed duration descending with stable identity ties. No cursor is supported. Current blockers can differ from historical contributing groups; pending verification previews are bounded independently of exact counts. Decision dimensions require completed cohort and cohort_token, an exact kind/contract_version/outcome_code and decision_code/rule_id/rule_version for decision groups. Path groups additionally require group_id/path_id; the decision triple is either fully supplied or fully omitted. The exact group must be present in the bounded decision_outcomes summary. Decision dimensions reject state/blocker/owner selectors. Returns the earliest qualifying report and optional business decision fact per contributor; reports after the first terminal revision do not qualify. Condition dimensions also require completed cohort and cohort_token and an exact returned condition_outcomes group selected by contract_version and decision_code/rule_id/rule_version and condition_id/condition_description/condition_result and outcome_code. They reject kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed results can produce overlapping groups. The earliest qualifying fact and all results observed for that exact identity are returned. Missing conditions are not classified as unknown. Active condition dimensions require ongoing cohort and cohort_token, an exact returned active_conditions identity selected by contract_version and decision_code/rule_id/rule_version and condition_id/condition_description/condition_result. Outcome and kind/path/state/blocker/owner selectors are rejected. They use latest explicit decision snapshots in the current contract, return evidence age at the bound evaluation time and measure whole-workflow elapsed or blocked duration. Metadata-only reports preserve evidence; explicit condition omission clears prior results. The optional condition_evidence_status selector restricts active contributors to within_budget, overdue or no_budget. Status counters partition each returned group and the chosen status must have contributors. Budgets come from the reporting Operation pinned definition; deadlines measure report age, not business verification age. The condition_refresh_owner dimension requires ongoing cohort and cohort_token and an exact returned active_conditions.refresh_owners group selected by exactly one of owner or unassigned=true. It rejects contract/state/blocker/condition/result/status and decision/path/outcome selectors. Contributors are ranked by whole-workflow blocked duration and include bounded overdue condition facts with pinned refresh guidance; counts remain exact beyond the preview limit. The condition_refresh_age and condition_refresh_blocked dimensions require ongoing cohort and cohort_token and reject all group selectors. Each returns eligible workflows with at least one overdue selected condition once, across all refresh owners. The former ranks by oldest overdue report age in whole seconds, the latter by full workflow blocked duration; stable workflow identity breaks ties. These overall rankings remain available with zero contributors and are independent of condition and owner display caps. Each contributor includes refresh_priority with the oldest qualifying fact, complete assigned owner list, exact unassigned count and explanation, plus the bounded condition preview. Age ranking does not mean most past deadline or highest business severity. Conflicting snapshots and incomplete histories are excluded. The condition_missing_owner dimension requires ongoing cohort and cohort_token and an exact returned condition_coverage.missing_groups row selected by contract_version, current state and exactly one of owner or unassigned=true. All condition/result/status, decision/path/outcome and blocker selectors are rejected. Counts and eligibility use full condition coverage before previews. Each workflow appears once in its selected group and ranks by full observed workflow blocked duration with stable identity ties. Missing evidence has no report timestamp and is not failed verification; expectations come from the latest reporting Operation pinned definition. Missing condition entries and pinned guidance are previewed up to 16 with exact counts and explicit truncation. The condition_gap_owner dimension requires ongoing cohort, cohort_token and baseline_token bound to the same scope. The baseline must not be later than the current token evaluation. Select an exact returned new_missing_progress.groups row with contract_version, current state, gap_status and exactly one of owner or unassigned=true. All other selectors are rejected; dimensions outside condition_gap_owner and condition_gap_queue reject baseline_token and gap_status. Counts and grouping precede previews; each contributor includes up to 16 matching gap_progress changes with exact counts, prior evidence and pinned guidance. Current report comparisons permit contract boundaries while baseline history must be continuous within its contract; ranking uses full observed report-only blocked intervals without changing eligibility for existing duration groups. Generic duration coverage remains separate from new_missing_progress coverage. The condition_gap_queue dimension requires ongoing cohort and both cohort_token and baseline_token with the same comparison scope and baseline time rules. It returns each eligible workflow once, independent of displayed group caps. Optional gap_status and exactly one of owner or unassigned=true filter matching current gaps; both may be omitted. Contract/state/blocker/condition/decision/path/outcome selectors are rejected. Empty results are valid. Each contributor includes gap_queue_priority with all category counts, sorted assigned owners, exact unassigned count, all pinned missing expectation guidance and a ranking explanation before filters or previews. gap_progress counts and previews contain only filtered gaps. Ranking measures observed report-only blocked intervals, omitting intervals spanning contract boundaries, with stable workflow identity ties.
   * @returns OperationWorkflowPerformanceInstancesResponse Ranked complete-history contributors within the authenticated customer's selected group.
   * @throws ApiError
   */
  public static listPlatformTenantSelfWorkflowPerformanceInstances({
    appId,
    scope,
    workflow,
    cohort,
    dimension,
    baselineToken,
    gapStatus,
    contractVersion,
    state,
    operation,
    code,
    owner,
    unassigned,
    cohortToken,
    kind,
    decisionCode,
    ruleId,
    ruleVersion,
    groupId,
    pathId,
    conditionId,
    conditionDescription,
    conditionEvidenceStatus,
    conditionResult,
    outcomeCode,
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
     * Declared workflow to investigate for the authenticated customer.
     */
    workflow: string,
    /**
     * Completed or ongoing customer workflow cohort.
     */
    cohort: 'completed' | 'ongoing',
    /**
     * Customer cohort duration measure or exact breakdown dimension.
     */
    dimension: 'state_time' | 'blocked_time' | 'verification_wait' | 'state' | 'blocker' | 'verification_owner' | 'decision_completion' | 'decision_blocked' | 'condition_completion' | 'condition_blocked' | 'active_condition_elapsed' | 'active_condition_blocked' | 'condition_refresh_owner' | 'condition_refresh_age' | 'condition_refresh_blocked' | 'condition_missing_owner' | 'condition_gap_owner' | 'condition_gap_queue',
    /**
     * Saved authenticated customer performance baseline; required for condition_gap_owner and condition_gap_queue; rejected by other dimensions.
     */
    baselineToken?: string,
    /**
     * Exact authenticated customer returned reporting-gap category; required for condition_gap_owner, optional for condition_gap_queue.
     */
    gapStatus?: 'report_lost' | 'new_expectation' | 'still_missing' | 'expectation_changed' | 'contract_changed',
    /**
     * Contract version required when selecting a customer state or blocker group.
     */
    contractVersion?: number,
    /**
     * Contributing state in the customer's state dimension.
     */
    state?: string,
    /**
     * Blocker target Operation in the authenticated customer's blocker dimension.
     */
    operation?: string,
    /**
     * Blocker code selecting the customer's exact blocker group.
     */
    code?: string,
    /**
     * Public owner of the customer's selected group; mutually exclusive with unassigned.
     */
    owner?: string,
    /**
     * Select an empty-owner customer group; mutually exclusive with owner.
     */
    unassigned?: boolean,
    /**
     * Customer selector-bound cohort token from a prior summary or contributor response.
     */
    cohortToken?: string,
    /**
     * Decision outcome group kind. Scoped to the authenticated customer.
     */
    kind?: 'decision' | 'path',
    /**
     * Exact reported decision code for decision outcome dimensions. Scoped to the authenticated customer.
     */
    decisionCode?: string,
    /**
     * Exact rule ID for decision outcome dimensions. Scoped to the authenticated customer.
     */
    ruleId?: string,
    /**
     * Exact rule version for decision outcome dimensions. Scoped to the authenticated customer.
     */
    ruleVersion?: string,
    /**
     * Exact selected prerequisite group for path outcome dimensions. Scoped to the authenticated customer.
     */
    groupId?: string,
    /**
     * Exact selected path for path outcome dimensions. Scoped to the authenticated customer.
     */
    pathId?: string,
    /**
     * Exact reported condition ID for customer condition outcome dimensions.
     */
    conditionId?: string,
    /**
     * Exact public condition description for the customer returned condition group; UTF-8 byte bound without controls.
     */
    conditionDescription?: string,
    /**
     * Customer active-condition report freshness filter. Only ongoing active condition dimensions accept it; the selected status must have contributors in the returned group.
     */
    conditionEvidenceStatus?: 'within_budget' | 'overdue' | 'no_budget',
    /**
     * Exact observed condition result for the customer condition outcome group.
     */
    conditionResult?: 'pass' | 'fail' | 'unknown',
    /**
     * Exact latest reported outcome for decision outcome dimensions. Scoped to the authenticated customer.
     */
    outcomeCode?: string,
  }): CancelablePromise<OperationWorkflowPerformanceInstancesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-performance/instances',
      query: {
        'app_id': appId,
        'scope': scope,
        'workflow': workflow,
        'cohort': cohort,
        'dimension': dimension,
        'baseline_token': baselineToken,
        'gap_status': gapStatus,
        'contract_version': contractVersion,
        'state': state,
        'operation': operation,
        'code': code,
        'owner': owner,
        'unassigned': unassigned,
        'cohort_token': cohortToken,
        'kind': kind,
        'decision_code': decisionCode,
        'rule_id': ruleId,
        'rule_version': ruleVersion,
        'group_id': groupId,
        'path_id': pathId,
        'condition_id': conditionId,
        'condition_description': conditionDescription,
        'condition_evidence_status': conditionEvidenceStatus,
        'condition_result': conditionResult,
        'outcome_code': outcomeCode,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The customer's retained cohort changed; refresh its performance summary for a new cohort_token.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Summarize business workflow performance across retained instances.
   * Requires account read scope and MFA. Optional customer selection stays within account ownership. Explicit app/environment/workflow selection is required. The latest 100 completed and 100 ongoing retained instances form separate cohorts. Matching counts include all retained instances. Incomplete histories are excluded from durations and nearest-rank p50/p95 percentiles with explicit coverage reasons. Each instance contributes one accumulated duration per reported group. At most 1024 reports per instance and 32 output groups per dimension; group truncation does not change cohort totals. Ongoing durations run through evaluation time. No cursor or time-window filter is supported. Retention and newly published reports can change results.
   * @returns OperationWorkflowPerformanceSummary Separate completed and ongoing cohorts with coverage and ranked durations.
   * @throws ApiError
   */
  public static summarizeAccountWorkflowPerformance({
    slug,
    scope,
    workflow,
    tenantId,
    baselineToken,
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
     * Declared workflow name to compare across instances.
     */
    workflow: string,
    /**
     * Optional account-owned customer selector on an operator milestone timeline.
     */
    tenantId?: string,
    /**
     * Account optional saved performance token anchoring refresh progress by evaluation time and exact query scope. Changed evidence is expected; the old fingerprint is not validated. Progress reconstructs visible reports in current completed and ongoing samples, not original cohort membership. Future or malformed tokens and scope mismatches are rejected.
     */
    baselineToken?: string,
  }): CancelablePromise<OperationWorkflowPerformanceSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-performance/summary',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'tenant_id': tenantId,
        'baseline_token': baselineToken,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Summarize retained business workflow performance for the authenticated customer.
   * Customer identity comes from credentials and tenant overrides are rejected. Requires platform_tenant:operations:read. Explicit app/environment/workflow selection is required. The latest 100 completed and 100 ongoing retained instances form separate cohorts. Matching counts include all retained instances. Incomplete histories are excluded from durations and nearest-rank p50/p95 percentiles with explicit coverage reasons. Each instance contributes one accumulated duration per reported group. At most 1024 reports per instance and 32 output groups per dimension; group truncation does not change cohort totals. Ongoing durations run through evaluation time. No cursor or time-window filter is supported. Retention and newly published reports can change results.
   * @returns OperationWorkflowPerformanceSummary Authenticated customer performance cohorts with observed durations and retained-history coverage.
   * @throws ApiError
   */
  public static summarizePlatformTenantSelfWorkflowPerformance({
    appId,
    scope,
    workflow,
    baselineToken,
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
     * Business workflow to compare within the authenticated customer app and environment.
     */
    workflow: string,
    /**
     * Customer optional saved performance token anchoring refresh progress by evaluation time and exact query scope. Changed evidence is expected; the old fingerprint is not validated. Progress reconstructs visible reports in current completed and ongoing samples, not original cohort membership. Future or malformed tokens and scope mismatches are rejected.
     */
    baselineToken?: string,
  }): CancelablePromise<OperationWorkflowPerformanceSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-performance/summary',
      query: {
        'app_id': appId,
        'scope': scope,
        'workflow': workflow,
        'baseline_token': baselineToken,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Summarize completed workflows with explicit business outcomes.
   * Account outcome summary — Requires account read scope and MFA. Optional tenant selection remains within account ownership. Explicit app and environment are required. Only latest retained terminal snapshots with reported outcomes are included; each instance counts once. Reopened instances leave these totals. Earlier outcomes remain in retained workflow history. Terminal state alone does not imply an outcome. Totals cover all matching instances independently of group pagination; cursors are separate from the attention queue. Reports and retention may change during browsing.
   * @returns OperationWorkflowOutcomeSummary All matching workflow count and a bounded group page ordered by value ascending.
   * @throws ApiError
   */
  public static summarizeAccountWorkflowOutcomes({
    slug,
    scope,
    workflow,
    code,
    tenantId,
    groupBy = 'outcome',
    limit = 20,
    cursor,
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
     * For the account outcome summary, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the account outcome summary, restrict terminal results to this explicit business outcome code.
     */
    code?: string,
    /**
     * Optional account-owned customer selector on an operator milestone timeline.
     */
    tenantId?: string,
    /**
     * For the account outcome summary, dimension used to aggregate matching workflow instances.
     */
    groupBy?: 'outcome' | 'workflow' | 'customer',
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationWorkflowOutcomeSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-outcomes/summary',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'code': code,
        'tenant_id': tenantId,
        'group_by': groupBy,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Customer outcome queue — List completed workflows with explicit business outcomes.
   * Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides are rejected. Explicit app and environment are required. Only latest retained terminal snapshots with reported outcomes are included; each instance counts once. Reopened instances leave these totals. Earlier outcomes remain in retained workflow history. Terminal state alone does not imply an outcome. Totals cover all matching instances independently of group pagination; cursors are separate from the attention queue. Reports and retention may change during browsing.
   * @returns OperationWorkflowOutcomesResponse Customer outcome queue — A bounded page ordered by latest report publication time descending.
   * @throws ApiError
   */
  public static listPlatformTenantSelfWorkflowOutcomes({
    appId,
    scope,
    workflow,
    code,
    limit = 20,
    cursor,
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
     * For the customer outcome queue, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the customer outcome queue, restrict terminal results to this explicit business outcome code.
     */
    code?: string,
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationWorkflowOutcomesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-outcomes',
      query: {
        'app_id': appId,
        'scope': scope,
        'workflow': workflow,
        'code': code,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Customer outcome summary — Summarize completed workflows with explicit business outcomes.
   * Customer outcome summary — Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides are rejected. Explicit app and environment are required. Only latest retained terminal snapshots with reported outcomes are included; each instance counts once. Reopened instances leave these totals. Earlier outcomes remain in retained workflow history. Terminal state alone does not imply an outcome. Totals cover all matching instances independently of group pagination; cursors are separate from the attention queue. Reports and retention may change during browsing.
   * @returns OperationWorkflowOutcomeSummary Customer outcome summary — All matching workflow count and a bounded group page ordered by value ascending.
   * @throws ApiError
   */
  public static summarizePlatformTenantSelfWorkflowOutcomes({
    appId,
    scope,
    workflow,
    code,
    groupBy = 'outcome',
    limit = 20,
    cursor,
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
     * For the customer outcome summary, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the customer outcome summary, restrict terminal results to this explicit business outcome code.
     */
    code?: string,
    /**
     * For the customer outcome summary, dimension used to aggregate matching workflow instances.
     */
    groupBy?: 'outcome' | 'workflow',
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationWorkflowOutcomeSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-outcomes/summary',
      query: {
        'app_id': appId,
        'scope': scope,
        'workflow': workflow,
        'code': code,
        'group_by': groupBy,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Preview business workflow actions
   * Read-only candidates from the retained current state, evaluated with an empty evidence plan. Does not execute, authorize, reserve, or mutate an action. At most 100 actions; optional operation filter. Application must recheck locked rows and readiness before writing.
   * @returns OperationWorkflowActionPreviewResponse Observed transition readiness including reasons and advisories.
   * @throws ApiError
   */
  public static previewAccountWorkflowActions({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: OperationWorkflowActionPreviewRequest,
  }): CancelablePromise<OperationWorkflowActionPreviewResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-actions/preview',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Customer action preview — Preview business workflow actions
   * Customer action preview — Read-only candidates from the retained current state, evaluated with an empty evidence plan. Does not execute, authorize, reserve, or mutate an action. At most 100 actions; optional operation filter. Application must recheck locked rows and readiness before writing.
   * @returns OperationWorkflowActionPreviewResponse Customer action preview — Observed transition readiness including reasons and advisories.
   * @throws ApiError
   */
  public static previewPlatformTenantSelfWorkflowActions({
    requestBody,
  }: {
    requestBody: OperationWorkflowActionPreviewRequest,
  }): CancelablePromise<OperationWorkflowActionPreviewResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflow-actions/preview',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Check reported requirements for a proposed workflow transition.
   * Requires account read scope and MFA. tenant_id is mandatory and app_id must be omitted; the path selects the owned application. Evaluates a proposed declared edge against retained state, target-specific blockers, direct prerequisites and planned milestone names. Ready is observational and grants no execution authority. Planned names are not committed evidence; transaction-time business checks and report validation still apply. A denied readiness result is a successful HTTP 200 response.
   * @returns OperationWorkflowReadinessResponse Account transition readiness — Observed transition readiness including reasons and advisories.
   * @throws ApiError
   */
  public static checkAccountWorkflowReadiness({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: OperationWorkflowReadinessRequest,
  }): CancelablePromise<OperationWorkflowReadinessResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Compare account transition readiness with a saved queue snapshot.
   * Requires account read scope and MFA; customer selectors stay within account ownership.
   * This read-only comparison accepts a caller-held snapshot emitted by the queue.
   * Snapshot scope binds the account, app, environment, customer selector and role.
   * Baseline data is advisory and is not authenticated historical evidence or execution authority.
   * At most 100 saved transitions are accepted in a body of at most 1048576 bytes.
   * Each distinct saved workflow is reread once with readiness-only projections,
   * independent of recent queue sampling and current owner or requirement filters.
   * The exact Operation/from/to edge and declared requirements must remain comparable.
   * Contract changes, terminal or unavailable state, unavailable edges and older retained
   * revisions are counted separately and never classified as newly ready or newly blocked.
   * Comparisons provide added and resolved requirement codes plus changed blocker,
   * prerequisite, pending-verification and reporting evidence categories.
   * Age counters alone are excluded from evidence fingerprints; freshness expiry can change readiness.
   * Change filters and the row limit apply after comparison; counts cover all saved transitions.
   * The returned snapshot includes available current edges before filtering and can be saved
   * as the next baseline. It omits unavailable edges. Reads are live and not atomic.
   * Empty snapshots are valid. Snapshot rows are limited to previously returned queue rows,
   * not the complete sampled backlog. Applications retain authorization and execution.
   *
   * @returns OperationWorkflowReadinessComparisonResponse Account readiness changes and comparison coverage over saved transitions.
   * @throws ApiError
   */
  public static compareAccountWorkflowReadiness({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: OperationWorkflowReadinessComparisonRequest,
  }): CancelablePromise<OperationWorkflowReadinessComparisonResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness/compare',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
      },
    });
  }
  /**
   * Customer transition readiness — Check reported requirements for a proposed workflow transition.
   * Requires platform-tenant operations read scope. app_id is mandatory and tenant_id must be omitted; ownership comes from the authenticated customer. Evaluates a proposed declared edge against retained state, target-specific blockers, direct prerequisites and planned milestone names. Ready is observational and grants no execution authority. Planned names are not committed evidence; transaction-time business checks and report validation still apply. A denied readiness result is a successful HTTP 200 response.
   * @returns OperationWorkflowReadinessResponse Customer transition readiness — Observed transition readiness including reasons and advisories.
   * @throws ApiError
   */
  public static checkPlatformTenantSelfWorkflowReadiness({
    requestBody,
  }: {
    requestBody: OperationWorkflowReadinessRequest,
  }): CancelablePromise<OperationWorkflowReadinessResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflow-readiness',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Compare customer transition readiness with a saved queue snapshot.
   * Requires platform-tenant Operations read scope; ownership comes from authentication and overrides are rejected.
   * This read-only comparison accepts a caller-held snapshot emitted by the queue.
   * Snapshot scope binds the account, app, environment, customer selector and role.
   * Baseline data is advisory and is not authenticated historical evidence or execution authority.
   * At most 100 saved transitions are accepted in a body of at most 1048576 bytes.
   * Each distinct saved workflow is reread once with readiness-only projections,
   * independent of recent queue sampling and current owner or requirement filters.
   * The exact Operation/from/to edge and declared requirements must remain comparable.
   * Contract changes, terminal or unavailable state, unavailable edges and older retained
   * revisions are counted separately and never classified as newly ready or newly blocked.
   * Comparisons provide added and resolved requirement codes plus changed blocker,
   * prerequisite, pending-verification and reporting evidence categories.
   * Age counters alone are excluded from evidence fingerprints; freshness expiry can change readiness.
   * Change filters and the row limit apply after comparison; counts cover all saved transitions.
   * The returned snapshot includes available current edges before filtering and can be saved
   * as the next baseline. It omits unavailable edges. Reads are live and not atomic.
   * Empty snapshots are valid. Snapshot rows are limited to previously returned queue rows,
   * not the complete sampled backlog. Applications retain authorization and execution.
   *
   * @returns OperationWorkflowReadinessComparisonResponse Customer readiness changes and comparison coverage over saved transitions.
   * @throws ApiError
   */
  public static comparePlatformTenantSelfWorkflowReadiness({
    requestBody,
  }: {
    requestBody: OperationWorkflowReadinessComparisonRequest,
  }): CancelablePromise<OperationWorkflowReadinessComparisonResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflow-readiness/compare',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
      },
    });
  }
  /**
   * List retained blocked, stale, overdue, or dependency-waiting business workflow instances.
   * Requires account read scope and MFA. Optional tenant selection remains within account ownership. Explicit app and environment selectors are required. Staleness and overdue deadlines are evaluated at the first page time carried by the cursor; current reports and retention can change during browsing. Unknown/unreported state is not enumerated. Target Operations match reported blockers or applicable selected-contract edges on stale or overdue workflows. The queue grants no execution authority.
   * @returns OperationWorkflowAttentionResponse One bounded page ordered by latest report publication time descending.
   * @throws ApiError
   */
  public static listAccountWorkflowAttention({
    slug,
    scope,
    workflow,
    targetOperation,
    dependencyStatus,
    requiredOutcomeCode,
    priority,
    sort = 'updated_at',
    owner,
    unassigned,
    blockerCode,
    reason,
    tenantId,
    limit = 20,
    cursor,
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
     * For the account attention queue, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the account attention queue, restrict attention to blockers targeting this Operation name.
     */
    targetOperation?: string,
    /**
     * Filter unresolved direct prerequisites by status. Both dependency filters must match the same reference.
     */
    dependencyStatus?: 'waiting' | 'unknown' | 'outcome_mismatch',
    /**
     * For the account attention queue, restrict unresolved prerequisites to this required business outcome code.
     */
    requiredOutcomeCode?: string,
    /**
     * Exact effective blocker priority matched with owner and other blocker filters. Omitted blocker priority counts as normal.
     */
    priority?: 'low' | 'normal' | 'high' | 'urgent',
    /**
     * Queue ordering. Deadline sorts earliest business deadline first with undated workflows last. Summary groups remain in group-value order.
     */
    sort?: 'updated_at' | 'deadline',
    /**
     * Exact case-sensitive public application-assigned blocker owner, limited to 128 UTF-8 bytes without control characters. Mutually exclusive with unassigned=true; owner, blocker code, and target filters must match the same blocker.
     */
    owner?: string,
    /**
     * Select workflows with a matching blocker whose owner is empty or absent. Mutually exclusive with owner. Explicit values must be true or false.
     */
    unassigned?: boolean,
    /**
     * For the account attention queue, restrict attention to this application-reported blocker code.
     */
    blockerCode?: string,
    /**
     * For the account attention queue, select the attention reason to include in the queue.
     */
    reason?: 'blocked' | 'stale' | 'overdue' | 'dependency' | 'escalated' | 'unacknowledged' | 'follow_up_overdue' | 'awaiting_verification' | 'sla_breached' | 'sla_at_risk',
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
  }): CancelablePromise<OperationWorkflowAttentionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-attention',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'target_operation': targetOperation,
        'dependency_status': dependencyStatus,
        'required_outcome_code': requiredOutcomeCode,
        'priority': priority,
        'sort': sort,
        'owner': owner,
        'unassigned': unassigned,
        'blocker_code': blockerCode,
        'reason': reason,
        'tenant_id': tenantId,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List account transition readiness across ongoing business workflows.
   * Requires account read scope and MFA. Optional customer selection stays within account ownership. Explicit app and environment required. Samples the 100 most recently updated retained ongoing workflows, ordered by update time then stable identity descending. Evaluates up to 100 sorted current-state transitions per workflow with no planned evidence. Target Operation filters before edge sampling; owner and requirement filters apply after evaluation but before row limit. Owner means an application-assigned blocker owner or pending verification owner for the target Operation; all retained pending obligations are considered before preview limits. Unassigned means one such finding has no owner. Reporting advisories never satisfy the requirement filter or change readiness. Unmet rows precede ready rows, preserving workflow sample order and Operation/from/to edge order. Sampling and row truncation are reported separately; counts cover evaluated transitions, not an account-wide backlog. Reads are live and not an atomic snapshot; changed workflow identities are excluded. No execution authority is granted. Unknown or terminal state is not enumerated. Duplicate, empty, and unsupported query parameters are rejected.
   * @returns OperationWorkflowReadinessQueueResponse Account transition readiness rows with explicit live sampling counts.
   * @throws ApiError
   */
  public static listAccountWorkflowReadinessQueue({
    slug,
    scope,
    workflow,
    targetOperation,
    owner,
    unassigned,
    tenantId,
    requirement,
    limit = 20,
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
     * Account readiness queue. Optional declared workflow name within the app and environment.
     */
    workflow?: string,
    /**
     * Account readiness queue. Select declared transitions targeting this Operation name before edge sampling.
     */
    targetOperation?: string,
    /**
     * Account readiness queue. Exact case-sensitive blocker or pending verification owner for the transition Operation. Maximum 128 UTF-8 bytes without controls. Mutually exclusive with unassigned=true.
     */
    owner?: string,
    /**
     * Account readiness queue. Select transitions with an unassigned blocker or pending verification obligation. Mutually exclusive with owner. Explicit values must be true or false.
     */
    unassigned?: boolean,
    /**
     * Account readiness queue. Optional customer UUID within the authenticated account.
     */
    tenantId?: string,
    /**
     * Account readiness queue. Select an unmet declared readiness requirement. Reporting advisories are excluded.
     */
    requirement?: 'state_unknown' | 'terminal' | 'transition_undeclared' | 'from_state_mismatch' | 'revision_mismatch' | 'contract_version_mismatch' | 'application_blocked' | 'dependency_unmet' | 'milestone_required' | 'policy_evidence_required' | 'dependency_required' | 'invariant_evidence_required' | 'effect_evidence_required' | 'retained_milestone_required' | 'retained_milestone_invalidated' | 'retained_milestone_expired' | 'retained_milestone_group_unmet' | 'path_selection_invalid',
    /**
     * Account readiness queue. Maximum matching transitions returned after filtering. No cursor is issued for this live sample.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowReadinessQueueResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/queue',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'workflow': workflow,
        'target_operation': targetOperation,
        'owner': owner,
        'unassigned': unassigned,
        'tenant_id': tenantId,
        'requirement': requirement,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Summarize all matching retained workflow attention.
   * Requires account read scope and MFA. Optional tenant selection remains within account ownership. Explicit app and environment selectors are required. Staleness and overdue deadlines are evaluated at the first page time carried by the cursor; current reports and retention can change during browsing. Unknown/unreported state is not enumerated. Target Operations match reported blockers or applicable selected-contract edges on stale or overdue workflows. Totals cover all matching retained snapshots regardless of group pagination. Groups may overlap and should not be added together. Blocker statistics within code and target groups cover that code or target only. Ages derive from application-reported first_observed_at; missing ages are counted separately. Group cursors are separate from queue cursors. The summary grants no execution authority.
   * @returns OperationWorkflowAttentionSummary Totals over all matching workflows and one bounded page of groups ordered by value ascending.
   * @throws ApiError
   */
  public static summarizeAccountWorkflowAttention({
    slug,
    scope,
    groupBy = 'workflow',
    workflow,
    targetOperation,
    dependencyStatus,
    requiredOutcomeCode,
    priority,
    sort = 'updated_at',
    owner,
    unassigned,
    blockerCode,
    reason,
    tenantId,
    limit = 20,
    cursor,
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
     * For the account attention summary, dimension used to aggregate matching workflow instances.
     */
    groupBy?: 'owner' | 'workflow' | 'blocker_code' | 'target_operation' | 'customer' | 'dependency_status' | 'required_outcome_code',
    /**
     * For the account attention summary, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the account attention summary, restrict attention to blockers targeting this Operation name.
     */
    targetOperation?: string,
    /**
     * Account attention summary — Filter unresolved direct prerequisites by status. Both dependency filters must match the same reference.
     */
    dependencyStatus?: 'waiting' | 'unknown' | 'outcome_mismatch',
    /**
     * For the account attention summary, restrict unresolved prerequisites to this required business outcome code.
     */
    requiredOutcomeCode?: string,
    /**
     * Account attention summary. Exact effective blocker priority matched with owner and other blocker filters. Omitted blocker priority counts as normal.
     */
    priority?: 'low' | 'normal' | 'high' | 'urgent',
    /**
     * Account attention summary. Queue ordering. Deadline sorts earliest business deadline first with undated workflows last. Summary groups remain in group-value order.
     */
    sort?: 'updated_at' | 'deadline',
    /**
     * Account attention summary. Exact case-sensitive public application-assigned blocker owner, limited to 128 UTF-8 bytes without control characters. Mutually exclusive with unassigned=true; owner, blocker code, and target filters must match the same blocker.
     */
    owner?: string,
    /**
     * Account attention summary. Select workflows with a matching blocker whose owner is empty or absent. Mutually exclusive with owner. Explicit values must be true or false.
     */
    unassigned?: boolean,
    /**
     * For the account attention summary, restrict attention to this application-reported blocker code.
     */
    blockerCode?: string,
    /**
     * For the account attention summary, select the attention reason to include in the queue.
     */
    reason?: 'blocked' | 'stale' | 'overdue' | 'dependency' | 'escalated' | 'unacknowledged' | 'follow_up_overdue' | 'awaiting_verification' | 'sla_breached' | 'sla_at_risk',
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
  }): CancelablePromise<OperationWorkflowAttentionSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-attention/summary',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'group_by': groupBy,
        'workflow': workflow,
        'target_operation': targetOperation,
        'dependency_status': dependencyStatus,
        'required_outcome_code': requiredOutcomeCode,
        'priority': priority,
        'sort': sort,
        'owner': owner,
        'unassigned': unassigned,
        'blocker_code': blockerCode,
        'reason': reason,
        'tenant_id': tenantId,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Customer attention queue — List retained blocked, stale, overdue, or dependency-waiting business workflow instances.
   * Requires platform_tenant:operations:read. Customer identity comes only from credentials; tenant overrides are rejected. Explicit app and environment selectors are required. Staleness and overdue deadlines are evaluated at the first page time carried by the cursor; current reports and retention can change during browsing. Unknown/unreported state is not enumerated. Target Operations match reported blockers or applicable selected-contract edges on stale or overdue workflows. The queue grants no execution authority.
   * @returns OperationWorkflowAttentionResponse Customer attention queue — One bounded page ordered by latest report publication time descending.
   * @throws ApiError
   */
  public static listPlatformTenantSelfWorkflowAttention({
    appId,
    scope,
    workflow,
    targetOperation,
    dependencyStatus,
    requiredOutcomeCode,
    priority,
    sort = 'updated_at',
    owner,
    unassigned,
    blockerCode,
    reason,
    limit = 20,
    cursor,
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
     * For the customer attention queue, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the customer attention queue, restrict attention to blockers targeting this Operation name.
     */
    targetOperation?: string,
    /**
     * Customer attention queue — Filter unresolved direct prerequisites by status. Both dependency filters must match the same reference.
     */
    dependencyStatus?: 'waiting' | 'unknown' | 'outcome_mismatch',
    /**
     * For the customer attention queue, restrict unresolved prerequisites to this required business outcome code.
     */
    requiredOutcomeCode?: string,
    /**
     * Customer attention queue. Exact effective blocker priority matched with owner and other blocker filters. Omitted blocker priority counts as normal.
     */
    priority?: 'low' | 'normal' | 'high' | 'urgent',
    /**
     * Customer attention queue. Queue ordering. Deadline sorts earliest business deadline first with undated workflows last. Summary groups remain in group-value order.
     */
    sort?: 'updated_at' | 'deadline',
    /**
     * Customer attention queue. Exact case-sensitive public application-assigned blocker owner, limited to 128 UTF-8 bytes without control characters. Mutually exclusive with unassigned=true; owner, blocker code, and target filters must match the same blocker.
     */
    owner?: string,
    /**
     * Customer attention queue. Select workflows with a matching blocker whose owner is empty or absent. Mutually exclusive with owner. Explicit values must be true or false.
     */
    unassigned?: boolean,
    /**
     * For the customer attention queue, restrict attention to this application-reported blocker code.
     */
    blockerCode?: string,
    /**
     * For the customer attention queue, select the attention reason to include in the queue.
     */
    reason?: 'blocked' | 'stale' | 'overdue' | 'dependency' | 'escalated' | 'unacknowledged' | 'follow_up_overdue' | 'awaiting_verification' | 'sla_breached' | 'sla_at_risk',
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationWorkflowAttentionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-attention',
      query: {
        'app_id': appId,
        'scope': scope,
        'workflow': workflow,
        'target_operation': targetOperation,
        'dependency_status': dependencyStatus,
        'required_outcome_code': requiredOutcomeCode,
        'priority': priority,
        'sort': sort,
        'owner': owner,
        'unassigned': unassigned,
        'blocker_code': blockerCode,
        'reason': reason,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List customer transition readiness across ongoing business workflows.
   * Requires platform-tenant operations read scope. Customer ownership derives from authentication. Explicit app and environment required. Samples the 100 most recently updated retained ongoing workflows, ordered by update time then stable identity descending. Evaluates up to 100 sorted current-state transitions per workflow with no planned evidence. Target Operation filters before edge sampling; owner and requirement filters apply after evaluation but before row limit. Owner means an application-assigned blocker owner or pending verification owner for the target Operation; all retained pending obligations are considered before preview limits. Unassigned means one such finding has no owner. Reporting advisories never satisfy the requirement filter or change readiness. Unmet rows precede ready rows, preserving workflow sample order and Operation/from/to edge order. Sampling and row truncation are reported separately; counts cover evaluated transitions, not an account-wide backlog. Reads are live and not an atomic snapshot; changed workflow identities are excluded. No execution authority is granted. Unknown or terminal state is not enumerated. Duplicate, empty, and unsupported query parameters are rejected.
   * @returns OperationWorkflowReadinessQueueResponse Customer transition readiness rows with explicit live sampling counts.
   * @throws ApiError
   */
  public static listPlatformTenantSelfWorkflowReadinessQueue({
    scope,
    appId,
    workflow,
    targetOperation,
    owner,
    unassigned,
    requirement,
    limit = 20,
  }: {
    /**
     * Explicit environment containing the related business Operations.
     */
    scope: string,
    /**
     * Customer readiness queue. Explicit application UUID within the authenticated account.
     */
    appId: string,
    /**
     * Customer readiness queue. Optional declared workflow name within the app and environment.
     */
    workflow?: string,
    /**
     * Customer readiness queue. Select declared transitions targeting this Operation name before edge sampling.
     */
    targetOperation?: string,
    /**
     * Customer readiness queue. Exact case-sensitive blocker or pending verification owner for the transition Operation. Maximum 128 UTF-8 bytes without controls. Mutually exclusive with unassigned=true.
     */
    owner?: string,
    /**
     * Customer readiness queue. Select transitions with an unassigned blocker or pending verification obligation. Mutually exclusive with owner. Explicit values must be true or false.
     */
    unassigned?: boolean,
    /**
     * Customer readiness queue. Select an unmet declared readiness requirement. Reporting advisories are excluded.
     */
    requirement?: 'state_unknown' | 'terminal' | 'transition_undeclared' | 'from_state_mismatch' | 'revision_mismatch' | 'contract_version_mismatch' | 'application_blocked' | 'dependency_unmet' | 'milestone_required' | 'policy_evidence_required' | 'dependency_required' | 'invariant_evidence_required' | 'effect_evidence_required' | 'retained_milestone_required' | 'retained_milestone_invalidated' | 'retained_milestone_expired' | 'retained_milestone_group_unmet' | 'path_selection_invalid',
    /**
     * Customer readiness queue. Maximum matching transitions returned after filtering. No cursor is issued for this live sample.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowReadinessQueueResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-readiness/queue',
      query: {
        'scope': scope,
        'workflow': workflow,
        'target_operation': targetOperation,
        'owner': owner,
        'unassigned': unassigned,
        'app_id': appId,
        'requirement': requirement,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Customer attention summary — Summarize all matching retained workflow attention.
   * Requires platform_tenant:operations:read. Customer identity comes only from credentials; tenant overrides are rejected. Explicit app and environment selectors are required. Staleness and overdue deadlines are evaluated at the first page time carried by the cursor; current reports and retention can change during browsing. Unknown/unreported state is not enumerated. Target Operations match reported blockers or applicable selected-contract edges on stale or overdue workflows. Totals cover all matching retained snapshots regardless of group pagination. Groups may overlap and should not be added together. Blocker statistics within code and target groups cover that code or target only. Ages derive from application-reported first_observed_at; missing ages are counted separately. Group cursors are separate from queue cursors. The summary grants no execution authority.
   * @returns OperationWorkflowAttentionSummary Customer attention summary — Totals over all matching workflows and one bounded page of groups ordered by value ascending.
   * @throws ApiError
   */
  public static summarizePlatformTenantSelfWorkflowAttention({
    appId,
    scope,
    groupBy = 'workflow',
    workflow,
    targetOperation,
    dependencyStatus,
    requiredOutcomeCode,
    priority,
    sort = 'updated_at',
    owner,
    unassigned,
    blockerCode,
    reason,
    limit = 20,
    cursor,
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
     * For the customer attention summary, dimension used to aggregate matching workflow instances.
     */
    groupBy?: 'owner' | 'workflow' | 'blocker_code' | 'target_operation' | 'dependency_status' | 'required_outcome_code',
    /**
     * For the customer attention summary, restrict results to this declared workflow name.
     */
    workflow?: string,
    /**
     * For the customer attention summary, restrict attention to blockers targeting this Operation name.
     */
    targetOperation?: string,
    /**
     * Customer attention summary — Filter unresolved direct prerequisites by status. Both dependency filters must match the same reference.
     */
    dependencyStatus?: 'waiting' | 'unknown' | 'outcome_mismatch',
    /**
     * For the customer attention summary, restrict unresolved prerequisites to this required business outcome code.
     */
    requiredOutcomeCode?: string,
    /**
     * Customer attention summary. Exact effective blocker priority matched with owner and other blocker filters. Omitted blocker priority counts as normal.
     */
    priority?: 'low' | 'normal' | 'high' | 'urgent',
    /**
     * Customer attention summary. Queue ordering. Deadline sorts earliest business deadline first with undated workflows last. Summary groups remain in group-value order.
     */
    sort?: 'updated_at' | 'deadline',
    /**
     * Customer attention summary. Exact case-sensitive public application-assigned blocker owner, limited to 128 UTF-8 bytes without control characters. Mutually exclusive with unassigned=true; owner, blocker code, and target filters must match the same blocker.
     */
    owner?: string,
    /**
     * Customer attention summary. Select workflows with a matching blocker whose owner is empty or absent. Mutually exclusive with owner. Explicit values must be true or false.
     */
    unassigned?: boolean,
    /**
     * For the customer attention summary, restrict attention to this application-reported blocker code.
     */
    blockerCode?: string,
    /**
     * For the customer attention summary, select the attention reason to include in the queue.
     */
    reason?: 'blocked' | 'stale' | 'overdue' | 'dependency' | 'escalated' | 'unacknowledged' | 'follow_up_overdue' | 'awaiting_verification' | 'sla_breached' | 'sla_at_risk',
    /**
     * Maximum retained public facts in a milestone page.
     */
    limit?: number,
    /**
     * Continuation for the same milestone feed, identity, workflow filter, and other selectors.
     */
    cursor?: string,
  }): CancelablePromise<OperationWorkflowAttentionSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-attention/summary',
      query: {
        'app_id': appId,
        'scope': scope,
        'group_by': groupBy,
        'workflow': workflow,
        'target_operation': targetOperation,
        'dependency_status': dependencyStatus,
        'required_outcome_code': requiredOutcomeCode,
        'priority': priority,
        'sort': sort,
        'owner': owner,
        'unassigned': unassigned,
        'blocker_code': blockerCode,
        'reason': reason,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read a business entity timeline across related Operations.
   * Requires account read scope and MFA. Explicit app/environment/reference selectors and optional customer selection remain within account ownership. Paired workflow and workflow-instance selectors narrow the feed to one run and include a grouped workflow_instance view with ordered declared steps, latest matching facts, current state, and independently paginated transition history. Facts are ordered by first platform publication time, not inferred business causality.
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
   * Requires platform_tenant:operations:read. Identity comes only from credentials. The app, environment, and paired public reference select related retained work. Paired workflow and workflow-instance selectors narrow the feed to one run and include a grouped workflow_instance view with ordered declared steps, latest matching facts, current state, and independently paginated transition history. Opaque pagination binds all selectors; other customers with the same entity ID remain isolated.
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
   * Requires the current workload and invocation claim. The state name and contract version must match the pinned workflow definition. A transition's referenced milestones must already be retained for the same Operation.
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
   * Requires the active workload and invocation claim. Validates the workflow contract version, transition edge, required milestone evidence and transaction-assigned revisions before commit. Evidence IDs must refer to facts in the same submitted transaction batch. This does not publish facts or reserve capacity. Maximum request size is 131072 bytes.
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
   * Observe the current single-task Job Operation lease.
   * Observe cancellation, the current lease and the fixed deadline using the scheduler-injected native task proof. Reads grant no renewal, result publication or retry authority. Account credentials and other runtime proof kinds cannot substitute. Admission remains closed pending native qualification.
   * @returns OperationJobControlResponse Current task control observation, with no-store caching.
   * @throws ApiError
   */
  public static getJobOperationExecutionControl({
    id,
    xGregaleOperationJobRunId,
    xGregaleOperationJobInstanceId,
    xGregaleOperationGeneration,
    xGregaleOperationAttempt,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Native Job run UUID retained by the accepted operation generation.
     */
    xGregaleOperationJobRunId: string,
    /**
     * Current single-task instance UUID whose host claim grants runtime authority.
     */
    xGregaleOperationJobInstanceId: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
  }): CancelablePromise<OperationJobControlResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/runtime/job-operations/{id}/control',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Job-Run-Id': xGregaleOperationJobRunId,
        'X-Gregale-Operation-Job-Instance-Id': xGregaleOperationJobInstanceId,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Report progress for the current single-task Job Operation lease.
   * Report bounded stage progress for the current native task. Stable report IDs are immutable within this generation and share the result and file namespace. The scheduler capability, run, instance, generation and attempt must match an active claim. Reporting never completes or retries a task.
   * @returns OperationResponse Operation snapshot after a durable progress report.
   * @throws ApiError
   */
  public static reportJobOperationProgress({
    id,
    xGregaleOperationJobRunId,
    xGregaleOperationJobInstanceId,
    xGregaleOperationGeneration,
    xGregaleOperationAttempt,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Native Job run UUID retained by the accepted operation generation.
     */
    xGregaleOperationJobRunId: string,
    /**
     * Current single-task instance UUID whose host claim grants runtime authority.
     */
    xGregaleOperationJobInstanceId: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    requestBody: OperationJobReportRequest,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/job-operations/{id}/progress',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Job-Run-Id': xGregaleOperationJobRunId,
        'X-Gregale-Operation-Job-Instance-Id': xGregaleOperationJobInstanceId,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Report result for the current single-task Job Operation lease.
   * Prepare schema-validated typed output for the current leased Job task. A stable result report is immutable and grants no business success until host-confirmed task exit. Uncertain exit requires reconciliation. Progress and private file reports share its ID namespace.
   * @returns OperationResponse Operation snapshot with its typed result privately prepared.
   * @throws ApiError
   */
  public static prepareJobOperationResult({
    id,
    xGregaleOperationJobRunId,
    xGregaleOperationJobInstanceId,
    xGregaleOperationGeneration,
    xGregaleOperationAttempt,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Native Job run UUID retained by the accepted operation generation.
     */
    xGregaleOperationJobRunId: string,
    /**
     * Current single-task instance UUID whose host claim grants runtime authority.
     */
    xGregaleOperationJobInstanceId: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    requestBody: OperationJobReportRequest,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/job-operations/{id}/result',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Job-Run-Id': xGregaleOperationJobRunId,
        'X-Gregale-Operation-Job-Instance-Id': xGregaleOperationJobInstanceId,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Verify and retain a private file for the current Job Operation task.
   * Requires the scheduler-injected task capability and matching run, instance, generation, attempt, unexpired lease and active owner. Stable report IDs share the progress/result namespace. Changed declarations conflict. Preparation verifies source ownership, private scope, size and SHA-256 and retains a private platform copy. Same-generation replay does not reread the source. Files remain private until host-confirmed success with a typed result or explicit account success recovery. Safe retry clears file receipts. Admission remains closed pending native qualification.
   * @returns OperationJobArtifactResponse Verified private managed-source file preparation receipt.
   * @throws ApiError
   */
  public static prepareJobOperationArtifact({
    id,
    xGregaleOperationJobRunId,
    xGregaleOperationJobInstanceId,
    xGregaleOperationGeneration,
    xGregaleOperationAttempt,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Native Job run UUID retained by the accepted operation generation.
     */
    xGregaleOperationJobRunId: string,
    /**
     * Current single-task instance UUID whose host claim grants runtime authority.
     */
    xGregaleOperationJobInstanceId: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    requestBody: OperationArtifactRequest,
  }): CancelablePromise<OperationJobArtifactResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/job-operations/{id}/artifacts',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Job-Run-Id': xGregaleOperationJobRunId,
        'X-Gregale-Operation-Job-Instance-Id': xGregaleOperationJobInstanceId,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Look up a verified file receipt for the current Job Operation task.
   * Look up an existing verified managed-source file receipt with the live native task proof. Matching metadata returns the retained copy without reading or modifying the source. Changed declarations conflict. This lookup grants neither publication nor business retry authority.
   * @returns OperationJobArtifactResponse Existing managed-source receipt availability and metadata.
   * @throws ApiError
   */
  public static reuseJobOperationArtifact({
    id,
    xGregaleOperationJobRunId,
    xGregaleOperationJobInstanceId,
    xGregaleOperationGeneration,
    xGregaleOperationAttempt,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Native Job run UUID retained by the accepted operation generation.
     */
    xGregaleOperationJobRunId: string,
    /**
     * Current single-task instance UUID whose host claim grants runtime authority.
     */
    xGregaleOperationJobInstanceId: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    requestBody: OperationArtifactRequest,
  }): CancelablePromise<OperationJobArtifactResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/job-operations/{id}/artifact-receipts',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Job-Run-Id': xGregaleOperationJobRunId,
        'X-Gregale-Operation-Job-Instance-Id': xGregaleOperationJobInstanceId,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Upload direct private file bytes with current HTTP invocation authority.
   * Stable report ID, name, exact size and SHA-256 bind a private upload receipt to the current HTTP invocation attempt. Requires a workload JWT for gregale:operations plus current invocation, attempt and capability. No bucket, source URI, provider credential or storage key is accepted. Bytes are verified under existing artifact quotas and transfer budgets. Each copy reserves fresh staging storage; concurrent copies converge and losing objects are cleaned up. Receipt lookup and committed replay do not repeat business code. New attachments are fenced by cancellation and live dispatch authority. Files download only after confirmed HTTP success or existing explicit success recovery; retaining bytes never settles the invocation.
   * @returns OperationArtifactUploadResponse Verified HTTP file receipt; the invocation remains unsettled.
   * @throws ApiError
   */
  public static uploadOperationArtifact({
    id,
    xFaasInvocationId,
    xGregaleOperationCapability,
    xGregaleOperationAttempt,
    reportId,
    name,
    sizeBytes,
    sha256,
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
     * Ephemeral 256-bit claim capability supplied only to the active handler.
     */
    xGregaleOperationCapability: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Stable immutable report identity within the current HTTP invocation attempt.
     */
    reportId: string,
    /**
     * Filename for the private HTTP result, without path separators.
     */
    name: string,
    /**
     * Declared HTTP result byte count within captured file quotas.
     */
    sizeBytes: number,
    /**
     * HTTP file checksum as lowercase hexadecimal with a sha256: prefix.
     */
    sha256: string,
    requestBody: Blob,
  }): CancelablePromise<OperationArtifactUploadResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/operations/{id}/artifact-uploads',
      path: {
        'id': id,
      },
      headers: {
        'X-Faas-Invocation-Id': xFaasInvocationId,
        'X-Gregale-Operation-Capability': xGregaleOperationCapability,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
      },
      query: {
        'report_id': reportId,
        'name': name,
        'size_bytes': sizeBytes,
        'sha256': sha256,
      },
      body: requestBody,
      mediaType: 'application/octet-stream',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
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
   * Look up a private HTTP file receipt without sending bytes.
   * Requires a current HTTP workload assertion and matching invocation proof. Checks the immutable declaration and live invocation attempt without reserving storage or reading bytes. Available false is returned only after authorization with no matching verified receipt. Matching committed receipts can be replayed after cancellation while the claim remains live; new files cannot be attached.
   * @returns OperationArtifactUploadResponse HTTP receipt availability and private metadata without a byte transfer.
   * @throws ApiError
   */
  public static reuseOperationUpload({
    id,
    xFaasInvocationId,
    xGregaleOperationCapability,
    xGregaleOperationAttempt,
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
     * Ephemeral 256-bit claim capability supplied only to the active handler.
     */
    xGregaleOperationCapability: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    requestBody: OperationArtifactUploadRequest,
  }): CancelablePromise<OperationArtifactUploadResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/operations/{id}/artifact-upload-receipts',
      path: {
        'id': id,
      },
      headers: {
        'X-Faas-Invocation-Id': xFaasInvocationId,
        'X-Gregale-Operation-Capability': xGregaleOperationCapability,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
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
   * Upload direct private file bytes with current native Job authority.
   * Stable report ID, name, exact size and SHA-256 bind a private upload receipt. Requires the current scheduler task proof and active owner. No source URI, bucket, provider credential or storage key is accepted. Exact bytes are verified under existing artifact quotas and bounded transfer budgets. A fresh staging object is reserved for each copy; concurrent copies converge and losing objects are cleaned up. Committed replay can return without reading the body. Files publish only with confirmed task success and typed output or explicit account success recovery. Production admission remains closed pending native qualification.
   * @returns OperationJobArtifactResponse Direct upload receipt for verified private bytes; preparation is not business success.
   * @throws ApiError
   */
  public static uploadJobOperationArtifact({
    id,
    xGregaleOperationJobRunId,
    xGregaleOperationJobInstanceId,
    xGregaleOperationGeneration,
    xGregaleOperationAttempt,
    reportId,
    name,
    sizeBytes,
    sha256,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Native Job run UUID retained by the accepted operation generation.
     */
    xGregaleOperationJobRunId: string,
    /**
     * Current single-task instance UUID whose host claim grants runtime authority.
     */
    xGregaleOperationJobInstanceId: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Stable immutable report identity within the native generation.
     */
    reportId: string,
    /**
     * Customer-visible filename without path separators.
     */
    name: string,
    /**
     * Exact number of uploaded bytes under captured operation limits.
     */
    sizeBytes: number,
    /**
     * Lowercase SHA-256 digest prefixed with sha256: for byte verification.
     */
    sha256: string,
    requestBody: Blob,
  }): CancelablePromise<OperationJobArtifactResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/job-operations/{id}/artifact-uploads',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Job-Run-Id': xGregaleOperationJobRunId,
        'X-Gregale-Operation-Job-Instance-Id': xGregaleOperationJobInstanceId,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
      },
      query: {
        'report_id': reportId,
        'name': name,
        'size_bytes': sizeBytes,
        'sha256': sha256,
      },
      body: requestBody,
      mediaType: 'application/octet-stream',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
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
   * Look up a direct private file upload receipt without sending bytes.
   * Requires the current native Job proof. Checks the immutable declaration and current task authority, including generation and ownership. Available false is returned only after successful authorization with no matching retained copy. Committed replay never reads a source object or repeats a business handler.
   * @returns OperationJobArtifactResponse Direct upload receipt lookup without receiving or writing bytes.
   * @throws ApiError
   */
  public static reuseJobOperationUpload({
    id,
    xGregaleOperationJobRunId,
    xGregaleOperationJobInstanceId,
    xGregaleOperationGeneration,
    xGregaleOperationAttempt,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Native Job run UUID retained by the accepted operation generation.
     */
    xGregaleOperationJobRunId: string,
    /**
     * Current single-task instance UUID whose host claim grants runtime authority.
     */
    xGregaleOperationJobInstanceId: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    requestBody: OperationArtifactUploadRequest,
  }): CancelablePromise<OperationJobArtifactResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/job-operations/{id}/artifact-upload-receipts',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Job-Run-Id': xGregaleOperationJobRunId,
        'X-Gregale-Operation-Job-Instance-Id': xGregaleOperationJobInstanceId,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
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
   * Upload direct private result bytes with current final workflow action authority.
   * Requires a current Operations workload assertion and native final-action proof for the pinned workflow run, step, generation and attempt. The descriptor declares immutable report ID, filename, byte count and SHA-256 without a source URI or storage credential. Each copy reserves unique staging storage and verifies exact bytes under existing quotas and transfer budgets. Current native owner, cancellation, lease and deadline are rechecked before private I/O and commit. Receipt identity remains stable across approved resumes; copies stay private until confirmed final-step success or explicit success recovery. Retention never settles a step or retries business effects.
   * @returns OperationWorkflowArtifactResponse Retained private workflow upload receipt awaiting final-action confirmation.
   * @throws ApiError
   */
  public static uploadWorkflowOperationArtifact({
    id,
    xGregaleOperationAttempt,
    xGregaleOperationExecutionKind,
    xGregaleOperationWorkflowRunId,
    xGregaleOperationWorkflowStep,
    xGregaleOperationGeneration,
    xGregaleOperationWorkflowCapability,
    reportId,
    name,
    sizeBytes,
    sha256,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Explicit native execution family; must be workflow.
     */
    xGregaleOperationExecutionKind: 'workflow',
    /**
     * Current operation workflow run UUID.
     */
    xGregaleOperationWorkflowRunId: string,
    /**
     * Final workflow action name.
     */
    xGregaleOperationWorkflowStep: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Private current step nonce; never log or retain in application state.
     */
    xGregaleOperationWorkflowCapability: string,
    /**
     * Immutable final-action file report identity across approved workflow resumes.
     */
    reportId: string,
    /**
     * Workflow result filename containing no path separators.
     */
    name: string,
    /**
     * Exact final-action upload size subject to retained workflow file quotas.
     */
    sizeBytes: number,
    /**
     * Workflow upload digest encoded as sha256: and lowercase hexadecimal.
     */
    sha256: string,
    requestBody: Blob,
  }): CancelablePromise<OperationWorkflowArtifactResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/workflow-operations/{id}/artifact-uploads',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Execution-Kind': xGregaleOperationExecutionKind,
        'X-Gregale-Operation-Workflow-Run-Id': xGregaleOperationWorkflowRunId,
        'X-Gregale-Operation-Workflow-Step': xGregaleOperationWorkflowStep,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Workflow-Capability': xGregaleOperationWorkflowCapability,
      },
      query: {
        'report_id': reportId,
        'name': name,
        'size_bytes': sizeBytes,
        'sha256': sha256,
      },
      body: requestBody,
      mediaType: 'application/octet-stream',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
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
   * Reuse a direct workflow file receipt without transferring bytes.
   * Checks a direct file declaration under current native final-step authority. Matching retained receipts are rebound to an approved resumed attempt without reserving storage, consuming reports or transferring bytes. A missing receipt returns available false only after authorization and quota validation. Conflicts, cancellation and stale proofs fail closed. Lookup does not confirm the workflow action or grant recovery authority.
   * @returns OperationWorkflowArtifactResponse Direct workflow receipt availability with any current-attempt rebind applied.
   * @throws ApiError
   */
  public static reuseWorkflowOperationUpload({
    id,
    xGregaleOperationAttempt,
    xGregaleOperationExecutionKind,
    xGregaleOperationWorkflowRunId,
    xGregaleOperationWorkflowStep,
    xGregaleOperationGeneration,
    xGregaleOperationWorkflowCapability,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Explicit native execution family; must be workflow.
     */
    xGregaleOperationExecutionKind: 'workflow',
    /**
     * Current operation workflow run UUID.
     */
    xGregaleOperationWorkflowRunId: string,
    /**
     * Final workflow action name.
     */
    xGregaleOperationWorkflowStep: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Private current step nonce; never log or retain in application state.
     */
    xGregaleOperationWorkflowCapability: string,
    requestBody: OperationArtifactUploadRequest,
  }): CancelablePromise<OperationWorkflowArtifactResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/workflow-operations/{id}/artifact-upload-receipts',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Execution-Kind': xGregaleOperationExecutionKind,
        'X-Gregale-Operation-Workflow-Run-Id': xGregaleOperationWorkflowRunId,
        'X-Gregale-Operation-Workflow-Step': xGregaleOperationWorkflowStep,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Workflow-Capability': xGregaleOperationWorkflowCapability,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
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
   * Observe cancellation and time bounds for a native workflow attempt.
   * Requires a fresh Operations workload assertion and current native run, step, recovery generation, attempt and capability proof on the pinned deployment. Available to every linear HTTP action of a workflow Operation. The fixed deadline is current attempt started_at plus the captured step timeout (30 seconds when omitted). The usable budget ends at the earlier native lease or deadline. Reads never renew leases, consume reports or authorize retries. Cancellation atomically interrupts native authority; a denied control read also stops the cooperating handler. Existing verified copies remain retained under normal recovery policy. Available while new admission is closed.
   * @returns OperationWorkflowControlResponse Read-only current native attempt observation. Cache-Control is no-store.
   * @throws ApiError
   */
  public static getWorkflowOperationExecutionControl({
    id,
    xGregaleOperationAttempt,
    xGregaleOperationExecutionKind,
    xGregaleOperationWorkflowRunId,
    xGregaleOperationWorkflowStep,
    xGregaleOperationGeneration,
    xGregaleOperationWorkflowCapability,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Explicit native execution family; must be workflow.
     */
    xGregaleOperationExecutionKind: 'workflow',
    /**
     * Current operation workflow run UUID.
     */
    xGregaleOperationWorkflowRunId: string,
    /**
     * Final workflow action name.
     */
    xGregaleOperationWorkflowStep: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Private current step nonce; never log or retain in application state.
     */
    xGregaleOperationWorkflowCapability: string,
  }): CancelablePromise<OperationWorkflowControlResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/runtime/workflow-operations/{id}/control',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Execution-Kind': xGregaleOperationExecutionKind,
        'X-Gregale-Operation-Workflow-Run-Id': xGregaleOperationWorkflowRunId,
        'X-Gregale-Operation-Workflow-Step': xGregaleOperationWorkflowStep,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Workflow-Capability': xGregaleOperationWorkflowCapability,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Check or reuse a verified workflow result copy.
   * Requires workload identity and a current final-step proof. An explicit available=false authorizes a new upload. A matching durable receipt is rebound to the current attempt without rereading or rewriting the source. Invalid or expired proofs fail; they never return available=false.
   * @returns OperationWorkflowArtifactResponse Current verified-copy availability for this declaration and attempt.
   * @throws ApiError
   */
  public static reuseWorkflowOperationArtifact({
    id,
    xGregaleOperationAttempt,
    xGregaleOperationExecutionKind,
    xGregaleOperationWorkflowRunId,
    xGregaleOperationWorkflowStep,
    xGregaleOperationGeneration,
    xGregaleOperationWorkflowCapability,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Explicit native execution family; must be workflow.
     */
    xGregaleOperationExecutionKind: 'workflow',
    /**
     * Current operation workflow run UUID.
     */
    xGregaleOperationWorkflowRunId: string,
    /**
     * Final workflow action name.
     */
    xGregaleOperationWorkflowStep: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Private current step nonce; never log or retain in application state.
     */
    xGregaleOperationWorkflowCapability: string,
    requestBody: OperationArtifactRequest,
  }): CancelablePromise<OperationWorkflowArtifactResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/workflow-operations/{id}/artifact-receipts',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Execution-Kind': xGregaleOperationExecutionKind,
        'X-Gregale-Operation-Workflow-Run-Id': xGregaleOperationWorkflowRunId,
        'X-Gregale-Operation-Workflow-Step': xGregaleOperationWorkflowStep,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Workflow-Capability': xGregaleOperationWorkflowCapability,
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
   * Prepare a private workflow result copy.
   * Verifies an owned private managed object and retains its exact bytes under a platform key. The durable receipt survives a lost response and an approved operation resume. Files are published only after confirmed final-step success, or explicit operator success reconciliation; preparing a file does not complete the operation.
   * @returns OperationWorkflowArtifactResponse Prepared verified-copy receipt, pending final-step confirmation.
   * @throws ApiError
   */
  public static prepareWorkflowOperationArtifact({
    id,
    xGregaleOperationAttempt,
    xGregaleOperationExecutionKind,
    xGregaleOperationWorkflowRunId,
    xGregaleOperationWorkflowStep,
    xGregaleOperationGeneration,
    xGregaleOperationWorkflowCapability,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Current fenced claim attempt.
     */
    xGregaleOperationAttempt: number,
    /**
     * Explicit native execution family; must be workflow.
     */
    xGregaleOperationExecutionKind: 'workflow',
    /**
     * Current operation workflow run UUID.
     */
    xGregaleOperationWorkflowRunId: string,
    /**
     * Final workflow action name.
     */
    xGregaleOperationWorkflowStep: string,
    /**
     * Current operation recovery generation.
     */
    xGregaleOperationGeneration: number,
    /**
     * Private current step nonce; never log or retain in application state.
     */
    xGregaleOperationWorkflowCapability: string,
    requestBody: OperationArtifactRequest,
  }): CancelablePromise<OperationWorkflowArtifactResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/workflow-operations/{id}/artifacts',
      path: {
        'id': id,
      },
      headers: {
        'X-Gregale-Operation-Attempt': xGregaleOperationAttempt,
        'X-Gregale-Operation-Execution-Kind': xGregaleOperationExecutionKind,
        'X-Gregale-Operation-Workflow-Run-Id': xGregaleOperationWorkflowRunId,
        'X-Gregale-Operation-Workflow-Step': xGregaleOperationWorkflowStep,
        'X-Gregale-Operation-Generation': xGregaleOperationGeneration,
        'X-Gregale-Operation-Workflow-Capability': xGregaleOperationWorkflowCapability,
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
    state?: 'accepted' | 'running' | 'waiting' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation',
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
   * Deliver a named event to an operation that waits for it.
   * Requires deploy:write and MFA (ADR-1197). The event must be one the operation's workflow waits for. signal_id makes delivery idempotent per operation; a repeat with the same event and payload returns the original receipt, a different event or payload under it conflicts. A signal that arrives before its wait is held until the wait starts. Settled operations take no new signals.
   * @returns OperationSignalReceipt The delivery receipt; duplicate marks a repeat of an earlier signal.
   * @throws ApiError
   */
  public static signalOperation({
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
    requestBody: OperationSignalRequest,
  }): CancelablePromise<OperationSignalReceipt> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/operations/{id}/signals',
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
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Deliver a named event to every active operation about one business subject.
   * Requires deploy:write and MFA (ADR-1197). Reaches at most 16 active operations of the named definition in the scope whose subject matches, oldest first; more is a limit error. Operations that settle during delivery are skipped. Each delivery is idempotent per operation by signal_id.
   * @returns OperationSubjectSignalResponse One receipt per operation reached.
   * @throws ApiError
   */
  public static signalOperationsBySubject({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: OperationSubjectSignalRequest,
  }): CancelablePromise<OperationSubjectSignalResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/operation-signals',
      path: {
        'slug': slug,
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
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Decide an account approval on waiting work (ADR-1194).
   * Requires deploy:write and MFA. Only a step whose definition names the account approver. The same decision is idempotent; a different one conflicts. Rejection settles the operation failed with approval_rejected.
   * @returns OperationResponse The decision is recorded; the operation resumes or settles approval_rejected.
   * @throws ApiError
   */
  public static decideOperationApproval({
    slug,
    id,
    step,
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
    /**
     * Approval step declared by the operation definition.
     */
    step: string,
    requestBody: OperationApprovalRequest,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/operations/{id}/approvals/{step}',
      path: {
        'slug': slug,
        'id': id,
        'step': step,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
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
    state?: 'accepted' | 'running' | 'waiting' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation',
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
   * Resolve a customer's retained submission without admitting work.
   * Requires platform_tenant:operations:read. Reads the idempotency ledger scoped to the authenticated account/customer, app, environment, operation name and key. Available while admission is closed, independently of current code or definition availability. accepted includes the original acceptance time and receipt; fetch status for current business state. expired means a known identity no longer has a retained usable acceptance. unresolved is not proof of rejection: admission may be in flight or a receipt pruned. No work is submitted and Cache-Control is no-store. expected_identity is an optional principal fence, never an ownership grant. Selectors belong in the bounded JSON body, not query parameters.
   * @returns OperationSubmissionLookupResponse Retained acceptance observation.
   * @throws ApiError
   */
  public static lookupPlatformTenantSelfOperationSubmission({
    requestBody,
  }: {
    requestBody: OperationSubmissionLookupRequest,
  }): CancelablePromise<OperationSubmissionLookupResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/customer-operations/submissions/lookup',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
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
  /**
   * Decide this customer's approval on its waiting operation (ADR-1194).
   * Requires platform_tenant:operations:manage. Only a step whose definition names the customer approver, on the customer's own operation. The same decision is idempotent; a different one conflicts.
   * @returns OperationResponse The customer's decision is recorded; its operation resumes or settles approval_rejected.
   * @throws ApiError
   */
  public static decidePlatformTenantSelfOperationApproval({
    id,
    step,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    /**
     * Approval step that the definition assigns to the customer.
     */
    step: string,
    requestBody: OperationApprovalRequest,
  }): CancelablePromise<OperationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/customer-operations/{id}/approvals/{step}',
      path: {
        'id': id,
        'step': step,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `A pinned contract, idempotency payload or execution generation conflicts with the retained state.`,
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
   * Deliver a named event to this customer's own waiting operation (ADR-20261017100853).
   * Requires platform_tenant:operations:manage. Works like the account signal route on the customer's own operation only; another customer's operation is not found. The operation's history records the signal with source customer. signal_id is shared with the account route, so a repeat from either is the same signal.
   * @returns OperationSignalReceipt The customer's delivery receipt; duplicate marks a repeat of an earlier signal.
   * @throws ApiError
   */
  public static signalPlatformTenantSelfOperation({
    id,
    requestBody,
  }: {
    /**
     * Stable logical operation identity.
     */
    id: string,
    requestBody: OperationSignalRequest,
  }): CancelablePromise<OperationSignalReceipt> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/customer-operations/{id}/signals',
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
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Deliver a named event to this customer's own active operations about one business subject (ADR-20261017100853).
   * Requires platform_tenant:operations:manage. The definition fixes the app, environment and operation name. Reaches at most 16 of the customer's own active operations whose subject matches; other customers' operations about the same subject are never reached. Each delivery is idempotent per operation by signal_id and recorded with source customer.
   * @returns OperationSubjectSignalResponse One receipt per customer operation reached; empty when none is active.
   * @throws ApiError
   */
  public static signalPlatformTenantSelfOperationsBySubject({
    requestBody,
  }: {
    requestBody: PlatformTenantSubjectSignalRequest,
  }): CancelablePromise<OperationSubjectSignalResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/customer-operation-signals',
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
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Account create WorkflowReadinessSubscription
   * Account durable readiness subscription create operation. Observations are bounded and advisory; execution remains application-owned. Requires account deploy-write scope and MFA.
   * @returns OperationWorkflowReadinessSubscription Account Create readiness subscription result.
   * @throws ApiError
   */
  public static createAccountWorkflowReadinessSubscription({
    slug,
    requestBody,
  }: {
    /**
     * CreateAccountWorkflowReadinessSubscription. Application slug.
     */
    slug: string,
    /**
     * Account subscription selectors; bounded to 4096 bytes.
     */
    requestBody: OperationWorkflowReadinessSubscriptionRequest,
  }): CancelablePromise<OperationWorkflowReadinessSubscription> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions',
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
   * Account list WorkflowReadinessSubscriptions
   * Account durable readiness subscription list operation. Observations are bounded and advisory; execution remains application-owned. Requires account read scope and MFA.
   * @returns OperationWorkflowReadinessSubscriptionsResponse Account List readiness subscription result.
   * @throws ApiError
   */
  public static listAccountWorkflowReadinessSubscriptions({
    slug,
    scope,
    tenantId,
  }: {
    /**
     * ListAccountWorkflowReadinessSubscriptions. Application slug.
     */
    slug: string,
    /**
     * ListAccountWorkflowReadinessSubscriptions. Application environment scope.
     */
    scope: string,
    /**
     * ListAccountWorkflowReadinessSubscriptions. Optional account customer selector.
     */
    tenantId?: string,
  }): CancelablePromise<OperationWorkflowReadinessSubscriptionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Account delete WorkflowReadinessSubscription
   * Account durable readiness subscription delete operation. Observations are bounded and advisory; execution remains application-owned. Requires account deploy-write scope and MFA.
   * @returns void
   * @throws ApiError
   */
  public static deleteAccountWorkflowReadinessSubscription({
    slug,
    id,
    scope,
    tenantId,
  }: {
    /**
     * DeleteAccountWorkflowReadinessSubscription. Application slug.
     */
    slug: string,
    /**
     * DeleteAccountWorkflowReadinessSubscription. Subscription UUID.
     */
    id: string,
    /**
     * DeleteAccountWorkflowReadinessSubscription. Application environment scope.
     */
    scope: string,
    /**
     * DeleteAccountWorkflowReadinessSubscription. Optional account customer selector.
     */
    tenantId?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Account get WorkflowReadinessInbox
   * Account durable readiness subscription get operation. Observations are bounded and advisory; execution remains application-owned. Requires account read scope and MFA.
   * @returns OperationWorkflowReadinessInboxResponse Account Get readiness inbox page.
   * @throws ApiError
   */
  public static getAccountWorkflowReadinessInbox({
    slug,
    id,
    scope,
    tenantId,
    afterSequence,
    limit,
  }: {
    /**
     * GetAccountWorkflowReadinessInbox. Application slug.
     */
    slug: string,
    /**
     * GetAccountWorkflowReadinessInbox. Subscription UUID.
     */
    id: string,
    /**
     * GetAccountWorkflowReadinessInbox. Application environment scope.
     */
    scope: string,
    /**
     * GetAccountWorkflowReadinessInbox. Optional account customer selector.
     */
    tenantId?: string,
    /**
     * GetAccountWorkflowReadinessInbox. Last consumed sequence; defaults to zero.
     */
    afterSequence?: number,
    /**
     * GetAccountWorkflowReadinessInbox. Page size; defaults to 20.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowReadinessInboxResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions/{id}/notifications',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'after_sequence': afterSequence,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Customer create WorkflowReadinessSubscription
   * Customer durable readiness subscription create operation. Observations are bounded and advisory; execution remains application-owned. Requires platform-tenant Operations manage scope.
   * @returns OperationWorkflowReadinessSubscription Customer Create readiness subscription result.
   * @throws ApiError
   */
  public static createPlatformTenantSelfWorkflowReadinessSubscription({
    requestBody,
  }: {
    /**
     * Customer subscription selectors; bounded to 4096 bytes.
     */
    requestBody: (OperationWorkflowReadinessSubscriptionRequest & Record<string, any>),
  }): CancelablePromise<OperationWorkflowReadinessSubscription> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflow-readiness/subscriptions',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
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
   * Customer list WorkflowReadinessSubscriptions
   * Customer durable readiness subscription list operation. Observations are bounded and advisory; execution remains application-owned. Requires platform-tenant Operations read scope.
   * @returns OperationWorkflowReadinessSubscriptionsResponse Customer List readiness subscription result.
   * @throws ApiError
   */
  public static listPlatformTenantSelfWorkflowReadinessSubscriptions({
    scope,
    appId,
  }: {
    /**
     * ListPlatformTenantSelfWorkflowReadinessSubscriptions. Application environment scope.
     */
    scope: string,
    /**
     * ListPlatformTenantSelfWorkflowReadinessSubscriptions. Application UUID.
     */
    appId: string,
  }): CancelablePromise<OperationWorkflowReadinessSubscriptionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-readiness/subscriptions',
      query: {
        'scope': scope,
        'app_id': appId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Customer delete WorkflowReadinessSubscription
   * Customer durable readiness subscription delete operation. Observations are bounded and advisory; execution remains application-owned. Requires platform-tenant Operations manage scope.
   * @returns void
   * @throws ApiError
   */
  public static deletePlatformTenantSelfWorkflowReadinessSubscription({
    id,
    scope,
    appId,
  }: {
    /**
     * DeletePlatformTenantSelfWorkflowReadinessSubscription. Subscription UUID.
     */
    id: string,
    /**
     * DeletePlatformTenantSelfWorkflowReadinessSubscription. Application environment scope.
     */
    scope: string,
    /**
     * DeletePlatformTenantSelfWorkflowReadinessSubscription. Application UUID.
     */
    appId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/platform-tenant-self/workflow-readiness/subscriptions/{id}',
      path: {
        'id': id,
      },
      query: {
        'scope': scope,
        'app_id': appId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Customer get WorkflowReadinessInbox
   * Customer durable readiness subscription get operation. Observations are bounded and advisory; execution remains application-owned. Requires platform-tenant Operations read scope.
   * @returns OperationWorkflowReadinessInboxResponse Customer Get readiness inbox page.
   * @throws ApiError
   */
  public static getPlatformTenantSelfWorkflowReadinessInbox({
    id,
    scope,
    appId,
    afterSequence,
    limit,
  }: {
    /**
     * GetPlatformTenantSelfWorkflowReadinessInbox. Subscription UUID.
     */
    id: string,
    /**
     * GetPlatformTenantSelfWorkflowReadinessInbox. Application environment scope.
     */
    scope: string,
    /**
     * GetPlatformTenantSelfWorkflowReadinessInbox. Application UUID.
     */
    appId: string,
    /**
     * GetPlatformTenantSelfWorkflowReadinessInbox. Last consumed sequence; defaults to zero.
     */
    afterSequence?: number,
    /**
     * GetPlatformTenantSelfWorkflowReadinessInbox. Page size; defaults to 20.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowReadinessInboxResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-readiness/subscriptions/{id}/notifications',
      path: {
        'id': id,
      },
      query: {
        'scope': scope,
        'app_id': appId,
        'after_sequence': afterSequence,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Check Account readiness notification
   * CheckAccountWorkflowReadinessNotification. Recheck retained application evidence; does not execute actions.
   * @returns OperationWorkflowReadinessNotificationCheck CheckAccountWorkflowReadinessNotification. Result.
   * @throws ApiError
   */
  public static checkAccountWorkflowReadinessNotification({
    slug,
    id,
    scope,
    sequence,
    tenantId,
  }: {
    /**
     * CheckAccountWorkflowReadinessNotification. Application slug.
     */
    slug: string,
    /**
     * CheckAccountWorkflowReadinessNotification. Subscription UUID.
     */
    id: string,
    /**
     * CheckAccountWorkflowReadinessNotification. Application environment scope.
     */
    scope: string,
    /**
     * CheckAccountWorkflowReadinessNotification. Retained subscription-local notification sequence.
     */
    sequence: number,
    /**
     * CheckAccountWorkflowReadinessNotification. Optional account customer selector.
     */
    tenantId?: string,
  }): CancelablePromise<OperationWorkflowReadinessNotificationCheck> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions/{id}/notifications/{sequence}',
      path: {
        'slug': slug,
        'id': id,
        'sequence': sequence,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Update Account readiness notification
   * UpdateAccountWorkflowReadinessNotification. Replace shared inbox coordination with a matching revision. Requires account deploy-write and MFA or customer operations manage. Actor derives from authentication. Revision zero addresses an untouched notification.
   * @returns OperationWorkflowReadinessNotification UpdateAccountWorkflowReadinessNotification. Result.
   * @throws ApiError
   */
  public static updateAccountWorkflowReadinessNotification({
    slug,
    id,
    scope,
    sequence,
    requestBody,
    tenantId,
  }: {
    /**
     * UpdateAccountWorkflowReadinessNotification. Application slug.
     */
    slug: string,
    /**
     * UpdateAccountWorkflowReadinessNotification. Subscription UUID.
     */
    id: string,
    /**
     * UpdateAccountWorkflowReadinessNotification. Application environment scope.
     */
    scope: string,
    /**
     * UpdateAccountWorkflowReadinessNotification. Retained subscription-local notification sequence.
     */
    sequence: number,
    requestBody: OperationWorkflowReadinessCoordinationRequest,
    /**
     * UpdateAccountWorkflowReadinessNotification. Optional account customer selector.
     */
    tenantId?: string,
  }): CancelablePromise<OperationWorkflowReadinessNotification> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/workflow-readiness/subscriptions/{id}/notifications/{sequence}',
      path: {
        'slug': slug,
        'id': id,
        'sequence': sequence,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
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
   * Check PlatformTenantSelf readiness notification
   * CheckPlatformTenantSelfWorkflowReadinessNotification. Recheck retained application evidence; does not execute actions.
   * @returns OperationWorkflowReadinessNotificationCheck CheckPlatformTenantSelfWorkflowReadinessNotification. Result.
   * @throws ApiError
   */
  public static checkPlatformTenantSelfWorkflowReadinessNotification({
    id,
    scope,
    appId,
    sequence,
  }: {
    /**
     * CheckPlatformTenantSelfWorkflowReadinessNotification. Subscription UUID.
     */
    id: string,
    /**
     * CheckPlatformTenantSelfWorkflowReadinessNotification. Application environment scope.
     */
    scope: string,
    /**
     * CheckPlatformTenantSelfWorkflowReadinessNotification. Application UUID.
     */
    appId: string,
    /**
     * CheckPlatformTenantSelfWorkflowReadinessNotification. Retained subscription-local notification sequence.
     */
    sequence: number,
  }): CancelablePromise<OperationWorkflowReadinessNotificationCheck> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-readiness/subscriptions/{id}/notifications/{sequence}',
      path: {
        'id': id,
        'sequence': sequence,
      },
      query: {
        'scope': scope,
        'app_id': appId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Update PlatformTenantSelf readiness notification
   * UpdatePlatformTenantSelfWorkflowReadinessNotification. Replace shared inbox coordination with a matching revision. Requires account deploy-write and MFA or customer operations manage. Actor derives from authentication. Revision zero addresses an untouched notification.
   * @returns OperationWorkflowReadinessNotification UpdatePlatformTenantSelfWorkflowReadinessNotification. Result.
   * @throws ApiError
   */
  public static updatePlatformTenantSelfWorkflowReadinessNotification({
    id,
    scope,
    appId,
    sequence,
    requestBody,
  }: {
    /**
     * UpdatePlatformTenantSelfWorkflowReadinessNotification. Subscription UUID.
     */
    id: string,
    /**
     * UpdatePlatformTenantSelfWorkflowReadinessNotification. Application environment scope.
     */
    scope: string,
    /**
     * UpdatePlatformTenantSelfWorkflowReadinessNotification. Application UUID.
     */
    appId: string,
    /**
     * UpdatePlatformTenantSelfWorkflowReadinessNotification. Retained subscription-local notification sequence.
     */
    sequence: number,
    requestBody: OperationWorkflowReadinessCoordinationRequest,
  }): CancelablePromise<OperationWorkflowReadinessNotification> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/platform-tenant-self/workflow-readiness/subscriptions/{id}/notifications/{sequence}',
      path: {
        'id': id,
        'sequence': sequence,
      },
      query: {
        'scope': scope,
        'app_id': appId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
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
   * List Account readiness follow-up work queue
   * ListAccountWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an explicit application and environment. Due-time ordering puts overdue first. Pages are live and may move; refresh after edits. Account reads require read scope and MFA; customer reads require Operations read.
   * @returns OperationWorkflowReadinessFollowUpsResponse ListAccountWorkflowReadinessFollowUps. Retained follow-up page.
   * @throws ApiError
   */
  public static listAccountWorkflowReadinessFollowUps({
    slug,
    scope,
    view,
    actionKind,
    actionTrigger,
    tenantId,
    owner,
    workflow,
    due,
    offset,
    limit,
    reminderOnly,
    impactPriority,
    impactKind,
    impactCode,
    sort,
  }: {
    /**
     * ListAccountWorkflowReadinessFollowUps. Application slug.
     */
    slug: string,
    /**
     * ListAccountWorkflowReadinessFollowUps. Application environment scope.
     */
    scope: string,
    /**
     * ListAccountWorkflowReadinessFollowUps.view. Select follow-ups (default) or current cross-case next actions. Next-action view uses action ownership/deadlines; reminder and business-impact selectors are incompatible.
     */
    view?: 'follow_ups' | 'next_actions',
    /**
     * ListAccountWorkflowReadinessFollowUps.action_kind. Exact action kind in next-action view.
     */
    actionKind?: 'response_check_in' | 'decision_revalidation' | 'handoff_acceptance' | 'handoff_reassignment' | 'escalation_acknowledgment' | 'coordinator_completion' | 'scheduled_evidence_check' | 'coordinator_evidence_review' | 'coordinator_revision' | 'dependency_review' | 'follow_up' | 'fulfillment_review',
    /**
     * ListAccountWorkflowReadinessFollowUps.action_trigger. Exact trigger in next-action view.
     */
    actionTrigger?: 'human' | 'automatic' | 'suggested',
    /**
     * ListAccountWorkflowReadinessFollowUps.tenant_id. Optional customer selector matches notification evidence or a customer-specific subscription, including broader account watches.
     */
    tenantId?: string,
    /**
     * ListAccountWorkflowReadinessFollowUps.owner. Exact coordination owner, distinct from application blocker ownership.
     */
    owner?: string,
    /**
     * ListAccountWorkflowReadinessFollowUps.workflow. Exact workflow name; global evaluation failures have no workflow.
     */
    workflow?: string,
    /**
     * ListAccountWorkflowReadinessFollowUps.due. Defaults to all pending follow-ups.
     */
    due?: 'all' | 'overdue' | 'upcoming',
    /**
     * ListAccountWorkflowReadinessFollowUps.offset. Live zero-based offset, defaults to zero.
     */
    offset?: number,
    /**
     * ListAccountWorkflowReadinessFollowUps.limit. Page size, defaults to 20.
     */
    limit?: number,
    /**
     * ListAccountWorkflowReadinessFollowUps.reminder_only. Defaults to false; true selects active durable due reminders using the same owner, customer, workflow and environment filters.
     */
    reminderOnly?: boolean,
    /**
     * ListAccountWorkflowReadinessFollowUps.impact_priority. Exact pinned priority on an unresolved validity review; excludes ordinary follow-ups without review impact.
     */
    impactPriority?: 'normal' | 'high' | 'critical',
    /**
     * ListAccountWorkflowReadinessFollowUps.impact_kind. Protected business result category on an unresolved review.
     */
    impactKind?: 'milestone' | 'outcome',
    /**
     * ListAccountWorkflowReadinessFollowUps.impact_code. Exact protected result code; requires impact_kind to avoid milestone/outcome ambiguity.
     */
    impactCode?: string,
    /**
     * ListAccountWorkflowReadinessFollowUps.sort. Defaults to due. Business impact ordering puts overdue follow-ups first, then critical/high/normal unresolved review priority, then due time and stable notification identity. Other follow-ups have no declared review priority. Filtering and sorting precede live pagination within the retained candidate window.
     */
    sort?: 'due' | 'business_impact',
  }): CancelablePromise<OperationWorkflowReadinessFollowUpsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/follow-ups',
      path: {
        'slug': slug,
      },
      query: {
        'view': view,
        'action_kind': actionKind,
        'action_trigger': actionTrigger,
        'scope': scope,
        'tenant_id': tenantId,
        'owner': owner,
        'workflow': workflow,
        'due': due,
        'offset': offset,
        'limit': limit,
        'reminder_only': reminderOnly,
        'impact_priority': impactPriority,
        'impact_kind': impactKind,
        'impact_code': impactCode,
        'sort': sort,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * List PlatformTenantSelf readiness follow-up work queue
   * ListPlatformTenantSelfWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an explicit application and environment. Due-time ordering puts overdue first. Pages are live and may move; refresh after edits. Account reads require read scope and MFA; customer reads require Operations read.
   * @returns OperationWorkflowReadinessFollowUpsResponse ListPlatformTenantSelfWorkflowReadinessFollowUps. Retained follow-up page.
   * @throws ApiError
   */
  public static listPlatformTenantSelfWorkflowReadinessFollowUps({
    scope,
    appId,
    view,
    actionKind,
    actionTrigger,
    owner,
    workflow,
    due,
    offset,
    limit,
    reminderOnly,
    impactPriority,
    impactKind,
    impactCode,
    sort,
  }: {
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps. Application environment scope.
     */
    scope: string,
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps. Application UUID.
     */
    appId: string,
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.view. Select follow-ups (default) or current cross-case next actions. Next-action view uses action ownership/deadlines; reminder and business-impact selectors are incompatible.
     */
    view?: 'follow_ups' | 'next_actions',
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.action_kind. Exact action kind in next-action view.
     */
    actionKind?: 'response_check_in' | 'decision_revalidation' | 'handoff_acceptance' | 'handoff_reassignment' | 'escalation_acknowledgment' | 'coordinator_completion' | 'scheduled_evidence_check' | 'coordinator_evidence_review' | 'coordinator_revision' | 'dependency_review' | 'follow_up' | 'fulfillment_review',
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.action_trigger. Exact trigger in next-action view.
     */
    actionTrigger?: 'human' | 'automatic' | 'suggested',
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.owner. Exact coordination owner, distinct from application blocker ownership.
     */
    owner?: string,
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.workflow. Exact workflow name; global evaluation failures have no workflow.
     */
    workflow?: string,
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.due. Defaults to all pending follow-ups.
     */
    due?: 'all' | 'overdue' | 'upcoming',
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.offset. Live zero-based offset, defaults to zero.
     */
    offset?: number,
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.limit. Page size, defaults to 20.
     */
    limit?: number,
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.reminder_only. Defaults to false; true selects active durable due reminders using the same owner, customer, workflow and environment filters.
     */
    reminderOnly?: boolean,
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.impact_priority. Exact pinned priority on an unresolved validity review; excludes ordinary follow-ups without review impact.
     */
    impactPriority?: 'normal' | 'high' | 'critical',
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.impact_kind. Protected business result category on an unresolved review.
     */
    impactKind?: 'milestone' | 'outcome',
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.impact_code. Exact protected result code; requires impact_kind to avoid milestone/outcome ambiguity.
     */
    impactCode?: string,
    /**
     * ListPlatformTenantSelfWorkflowReadinessFollowUps.sort. Defaults to due. Business impact ordering puts overdue follow-ups first, then critical/high/normal unresolved review priority, then due time and stable notification identity. Other follow-ups have no declared review priority. Filtering and sorting precede live pagination within the retained candidate window.
     */
    sort?: 'due' | 'business_impact',
  }): CancelablePromise<OperationWorkflowReadinessFollowUpsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-readiness/follow-ups',
      query: {
        'view': view,
        'action_kind': actionKind,
        'action_trigger': actionTrigger,
        'scope': scope,
        'app_id': appId,
        'owner': owner,
        'workflow': workflow,
        'due': due,
        'offset': offset,
        'limit': limit,
        'reminder_only': reminderOnly,
        'impact_priority': impactPriority,
        'impact_kind': impactKind,
        'impact_code': impactCode,
        'sort': sort,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Read account workflow coordination timeline
   * GetAccountWorkflowCoordinationTimeline. Read a bounded chronological projection from current retained readiness inboxes for an exact subject and workflow instance. Observation clocks are server times; latest snapshots do not reconstruct every past edit. This is not a complete audit trail and does not authorize execution. Sources are read independently and live offset pages may move. Account access requires read scope and MFA; customer access requires Operations read.
   * @returns OperationWorkflowCoordinationTimeline GetAccountWorkflowCoordinationTimeline. Retained workflow coordination timeline with source coverage and limitations.
   * @throws ApiError
   */
  public static getAccountWorkflowCoordinationTimeline({
    slug,
    scope,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    tenantId,
    offset,
    limit = 20,
  }: {
    /**
     * GetAccountWorkflowCoordinationTimeline.slug. Application slug.
     */
    slug: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.scope. Explicit application environment.
     */
    scope: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.tenant_id. Exact customer identity. Omission selects account-owned workflows only, not all customers.
     */
    tenantId?: string,
    /**
     * GetAccountWorkflowCoordinationTimeline.offset. Live zero-based event offset. Refresh after edits or retention changes.
     */
    offset?: number,
    /**
     * GetAccountWorkflowCoordinationTimeline.limit. Maximum events per page.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowCoordinationTimeline> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/timeline',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
        'offset': offset,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Read customer workflow coordination timeline
   * GetPlatformTenantSelfWorkflowCoordinationTimeline. Read a bounded chronological projection from current retained readiness inboxes for an exact subject and workflow instance. Observation clocks are server times; latest snapshots do not reconstruct every past edit. This is not a complete audit trail and does not authorize execution. Sources are read independently and live offset pages may move. Account access requires read scope and MFA; customer access requires Operations read.
   * @returns OperationWorkflowCoordinationTimeline GetPlatformTenantSelfWorkflowCoordinationTimeline. Retained workflow coordination timeline with source coverage and limitations.
   * @throws ApiError
   */
  public static getPlatformTenantSelfWorkflowCoordinationTimeline({
    appId,
    scope,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    offset,
    limit = 20,
  }: {
    /**
     * GetPlatformTenantSelfWorkflowCoordinationTimeline.app_id. Application UUID; customer identity comes from authentication.
     */
    appId: string,
    /**
     * GetPlatformTenantSelfWorkflowCoordinationTimeline.scope. Explicit application environment.
     */
    scope: string,
    /**
     * GetPlatformTenantSelfWorkflowCoordinationTimeline.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * GetPlatformTenantSelfWorkflowCoordinationTimeline.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * GetPlatformTenantSelfWorkflowCoordinationTimeline.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * GetPlatformTenantSelfWorkflowCoordinationTimeline.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * GetPlatformTenantSelfWorkflowCoordinationTimeline.offset. Live zero-based event offset. Refresh after edits or retention changes.
     */
    offset?: number,
    /**
     * GetPlatformTenantSelfWorkflowCoordinationTimeline.limit. Maximum events per page.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowCoordinationTimeline> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-readiness/timeline',
      query: {
        'app_id': appId,
        'scope': scope,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
        'offset': offset,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Read account workflow case overview
   * GetAccountWorkflowCase. Combine retained current application state, blockers, dependencies, verification and readiness with exact matching open follow-ups, advisory next steps and a retained coordination timeline. Evidence and coordination are independently read. Missing or stale reports cannot establish readiness or business success. The application retains outcome interpretation, authorization and execution. Account reads require an explicit customer selector, read scope and MFA; customer identity derives from authentication.
   * @returns OperationWorkflowCase GetAccountWorkflowCase. Current evidence and retained coordination for one exact business workflow case.
   * @throws ApiError
   */
  public static getAccountWorkflowCase({
    slug,
    scope,
    tenantId,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    offset,
    limit = 20,
  }: {
    /**
     * GetAccountWorkflowCase.slug. Application slug.
     */
    slug: string,
    /**
     * GetAccountWorkflowCase.scope. Explicit application environment.
     */
    scope: string,
    /**
     * GetAccountWorkflowCase.tenant_id. Required exact customer UUID; broad business-reference projections are not accepted.
     */
    tenantId: string,
    /**
     * GetAccountWorkflowCase.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * GetAccountWorkflowCase.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * GetAccountWorkflowCase.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * GetAccountWorkflowCase.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * GetAccountWorkflowCase.offset. Live event offset for the nested timeline only; follow-ups return their own bounded overview.
     */
    offset?: number,
    /**
     * GetAccountWorkflowCase.limit. Nested timeline page size; defaults to 20 with maximum 100.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowCase> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-readiness/case',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
        'offset': offset,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Read customer workflow case overview
   * GetPlatformTenantSelfWorkflowCase. Combine retained current application state, blockers, dependencies, verification and readiness with exact matching open follow-ups, advisory next steps and a retained coordination timeline. Evidence and coordination are independently read. Missing or stale reports cannot establish readiness or business success. The application retains outcome interpretation, authorization and execution. Account reads require an explicit customer selector, read scope and MFA; customer identity derives from authentication.
   * @returns OperationWorkflowCase GetPlatformTenantSelfWorkflowCase. Current evidence and retained coordination for one exact business workflow case.
   * @throws ApiError
   */
  public static getPlatformTenantSelfWorkflowCase({
    appId,
    scope,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    offset,
    limit = 20,
  }: {
    /**
     * GetPlatformTenantSelfWorkflowCase.app_id. Application UUID; customer identity comes from authentication.
     */
    appId: string,
    /**
     * GetPlatformTenantSelfWorkflowCase.scope. Explicit application environment.
     */
    scope: string,
    /**
     * GetPlatformTenantSelfWorkflowCase.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * GetPlatformTenantSelfWorkflowCase.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * GetPlatformTenantSelfWorkflowCase.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * GetPlatformTenantSelfWorkflowCase.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * GetPlatformTenantSelfWorkflowCase.offset. Live event offset for the nested timeline only; follow-ups return their own bounded overview.
     */
    offset?: number,
    /**
     * GetPlatformTenantSelfWorkflowCase.limit. Nested timeline page size; defaults to 20 with maximum 100.
     */
    limit?: number,
  }): CancelablePromise<OperationWorkflowCase> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflow-readiness/case',
      query: {
        'app_id': appId,
        'scope': scope,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
        'offset': offset,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Start pinned workflow playbook progress
   * StartAccountWorkflowPlaybookProgress. Start from a currently matched fresh application playbook. Pin exact rule, workflow version and immutable reporting definition. Repeated starts return the existing checklist. Requires account write scope and MFA or customer operations-manage scope. Customer identity derives from authentication. Checklist completion does not establish readiness, authorize execution or report business success. At most 100 records per exact case; the latest 64 review changes per record are retained.
   * @returns OperationWorkflowPlaybookProgress StartAccountWorkflowPlaybookProgress. Durable pinned checklist after the accepted start or review.
   * @throws ApiError
   */
  public static startAccountWorkflowPlaybookProgress({
    slug,
    scope,
    tenantId,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    requestBody,
  }: {
    /**
     * StartAccountWorkflowPlaybookProgress.slug. Application slug.
     */
    slug: string,
    /**
     * StartAccountWorkflowPlaybookProgress.scope. Explicit application environment.
     */
    scope: string,
    /**
     * StartAccountWorkflowPlaybookProgress.tenant_id. Required exact customer UUID; broad business-reference projections are not accepted.
     */
    tenantId: string,
    /**
     * StartAccountWorkflowPlaybookProgress.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * StartAccountWorkflowPlaybookProgress.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * StartAccountWorkflowPlaybookProgress.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * StartAccountWorkflowPlaybookProgress.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    requestBody: OperationWorkflowPlaybookProgressStartRequest,
  }): CancelablePromise<OperationWorkflowPlaybookProgress> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness/playbook-progress',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Update pinned workflow playbook progress
   * UpdateAccountWorkflowPlaybookProgress. Replace one frozen checklist item using the expected revision. Record authenticated reviewer, note and scoped operation or milestone evidence. Conflicts return 409. Existing guidance is never upgraded. Requires account write scope and MFA or customer operations-manage scope. Customer identity derives from authentication. Checklist completion does not establish readiness, authorize execution or report business success. At most 100 records per exact case; the latest 64 review changes per record are retained.
   * @returns OperationWorkflowPlaybookProgress UpdateAccountWorkflowPlaybookProgress. Durable pinned checklist after the accepted start or review.
   * @throws ApiError
   */
  public static updateAccountWorkflowPlaybookProgress({
    slug,
    scope,
    tenantId,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    id,
    requestBody,
  }: {
    /**
     * UpdateAccountWorkflowPlaybookProgress.slug. Application slug.
     */
    slug: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.scope. Explicit application environment.
     */
    scope: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.tenant_id. Required exact customer UUID; broad business-reference projections are not accepted.
     */
    tenantId: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * UpdateAccountWorkflowPlaybookProgress.id. Durable checklist UUID belonging to this exact case.
     */
    id: string,
    requestBody: OperationWorkflowPlaybookProgressUpdateRequest,
  }): CancelablePromise<OperationWorkflowPlaybookProgress> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/workflow-readiness/playbook-progress/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Start customer pinned workflow playbook progress
   * StartPlatformTenantSelfWorkflowPlaybookProgress. Start from a currently matched fresh application playbook. Pin exact rule, workflow version and immutable reporting definition. Repeated starts return the existing checklist. Requires account write scope and MFA or customer operations-manage scope. Customer identity derives from authentication. Checklist completion does not establish readiness, authorize execution or report business success. At most 100 records per exact case; the latest 64 review changes per record are retained.
   * @returns OperationWorkflowPlaybookProgress StartPlatformTenantSelfWorkflowPlaybookProgress. Durable pinned checklist after the accepted start or review.
   * @throws ApiError
   */
  public static startPlatformTenantSelfWorkflowPlaybookProgress({
    appId,
    scope,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    requestBody,
  }: {
    /**
     * StartPlatformTenantSelfWorkflowPlaybookProgress.app_id. Application UUID; customer identity comes from authentication.
     */
    appId: string,
    /**
     * StartPlatformTenantSelfWorkflowPlaybookProgress.scope. Explicit application environment.
     */
    scope: string,
    /**
     * StartPlatformTenantSelfWorkflowPlaybookProgress.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * StartPlatformTenantSelfWorkflowPlaybookProgress.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * StartPlatformTenantSelfWorkflowPlaybookProgress.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * StartPlatformTenantSelfWorkflowPlaybookProgress.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    requestBody: OperationWorkflowPlaybookProgressStartRequest,
  }): CancelablePromise<OperationWorkflowPlaybookProgress> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflow-readiness/playbook-progress',
      query: {
        'app_id': appId,
        'scope': scope,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Update customer pinned workflow playbook progress
   * UpdatePlatformTenantSelfWorkflowPlaybookProgress. Replace one frozen checklist item using the expected revision. Record authenticated reviewer, note and scoped operation or milestone evidence. Conflicts return 409. Existing guidance is never upgraded. Requires account write scope and MFA or customer operations-manage scope. Customer identity derives from authentication. Checklist completion does not establish readiness, authorize execution or report business success. At most 100 records per exact case; the latest 64 review changes per record are retained.
   * @returns OperationWorkflowPlaybookProgress UpdatePlatformTenantSelfWorkflowPlaybookProgress. Durable pinned checklist after the accepted start or review.
   * @throws ApiError
   */
  public static updatePlatformTenantSelfWorkflowPlaybookProgress({
    appId,
    scope,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    id,
    requestBody,
  }: {
    /**
     * UpdatePlatformTenantSelfWorkflowPlaybookProgress.app_id. Application UUID; customer identity comes from authentication.
     */
    appId: string,
    /**
     * UpdatePlatformTenantSelfWorkflowPlaybookProgress.scope. Explicit application environment.
     */
    scope: string,
    /**
     * UpdatePlatformTenantSelfWorkflowPlaybookProgress.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * UpdatePlatformTenantSelfWorkflowPlaybookProgress.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * UpdatePlatformTenantSelfWorkflowPlaybookProgress.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * UpdatePlatformTenantSelfWorkflowPlaybookProgress.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * UpdatePlatformTenantSelfWorkflowPlaybookProgress.id. Durable checklist UUID belonging to this exact case.
     */
    id: string,
    requestBody: OperationWorkflowPlaybookProgressUpdateRequest,
  }): CancelablePromise<OperationWorkflowPlaybookProgress> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/platform-tenant-self/workflow-readiness/playbook-progress/{id}',
      path: {
        'id': id,
      },
      query: {
        'app_id': appId,
        'scope': scope,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Configure account checklist evidence monitoring
   * ConfigureAccountWorkflowPlaybookEvidenceWatch. Explicitly enroll or stop a pinned checklist in an existing readiness inbox. Requires the exact checklist revision and watch configuration revision (zero for a new watch). Enabling captures a baseline without delivering a notice. Status or reason changes subsequently publish one grouped notification; clocks and report revisions alone do not notify. Account write scope and MFA or tenant operations-manage scope are required. Closure and revision are rechecked under the shared writer lock before publication. Polling does not change checklist progress or business state. At most 20 retained watches per subscription; stopped entries may be reclaimed for new watches.
   * @returns OperationWorkflowPlaybookEvidenceWatch ConfigureAccountWorkflowPlaybookEvidenceWatch. Watch configuration and saved evidence baseline.
   * @throws ApiError
   */
  public static configureAccountWorkflowPlaybookEvidenceWatch({
    slug,
    scope,
    tenantId,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    id,
    requestBody,
  }: {
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.slug. Application slug.
     */
    slug: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.scope. Explicit application environment.
     */
    scope: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.tenant_id. Required exact customer UUID; broad business-reference projections are not accepted.
     */
    tenantId: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * ConfigureAccountWorkflowPlaybookEvidenceWatch.id. Durable checklist UUID belonging to this exact case.
     */
    id: string,
    requestBody: OperationWorkflowPlaybookEvidenceWatchRequest,
  }): CancelablePromise<OperationWorkflowPlaybookEvidenceWatch> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness/playbook-progress/{id}/evidence-watch',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Record account playbook review decision
   * ReviewAccountWorkflowPlaybookProgress. Record continue, retire or replace with a required human reason and expected source revision. Retire and replace permanently close source coordination. Replacement requires an already-started active applicable target in the same exact case, its expected revision, and an atomic target-state check. No item evidence, pinned guidance, business outcome or execution authority is changed. Account write scope and MFA or customer operations-manage scope are required; customer ownership derives from authentication. Applicability is observed before the write, not transactionally locked application evidence.
   * @returns OperationWorkflowPlaybookProgress ReviewAccountWorkflowPlaybookProgress. Source checklist with the committed durable decision and updated shared revision.
   * @throws ApiError
   */
  public static reviewAccountWorkflowPlaybookProgress({
    slug,
    scope,
    tenantId,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    id,
    requestBody,
  }: {
    /**
     * ReviewAccountWorkflowPlaybookProgress.slug. Application slug.
     */
    slug: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.scope. Explicit application environment.
     */
    scope: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.tenant_id. Required exact customer UUID; broad business-reference projections are not accepted.
     */
    tenantId: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * ReviewAccountWorkflowPlaybookProgress.id. Durable checklist UUID belonging to this exact case.
     */
    id: string,
    requestBody: OperationWorkflowPlaybookReviewRequest,
  }): CancelablePromise<OperationWorkflowPlaybookProgress> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-readiness/playbook-progress/{id}/reviews',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'scope': scope,
        'tenant_id': tenantId,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Configure customer checklist evidence monitoring
   * ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch. Explicitly enroll or stop a pinned checklist in an existing readiness inbox. Requires the exact checklist revision and watch configuration revision (zero for a new watch). Enabling captures a baseline without delivering a notice. Status or reason changes subsequently publish one grouped notification; clocks and report revisions alone do not notify. Account write scope and MFA or tenant operations-manage scope are required. Closure and revision are rechecked under the shared writer lock before publication. Polling does not change checklist progress or business state. At most 20 retained watches per subscription; stopped entries may be reclaimed for new watches.
   * @returns OperationWorkflowPlaybookEvidenceWatch ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch. Watch configuration and saved evidence baseline.
   * @throws ApiError
   */
  public static configurePlatformTenantSelfWorkflowPlaybookEvidenceWatch({
    appId,
    scope,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    id,
    requestBody,
  }: {
    /**
     * ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch.app_id. Application UUID; customer identity comes from authentication.
     */
    appId: string,
    /**
     * ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch.scope. Explicit application environment.
     */
    scope: string,
    /**
     * ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch.id. Durable checklist UUID belonging to this exact case.
     */
    id: string,
    requestBody: OperationWorkflowPlaybookEvidenceWatchRequest,
  }): CancelablePromise<OperationWorkflowPlaybookEvidenceWatch> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflow-readiness/playbook-progress/{id}/evidence-watch',
      path: {
        'id': id,
      },
      query: {
        'app_id': appId,
        'scope': scope,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Record customer playbook review decision
   * ReviewPlatformTenantSelfWorkflowPlaybookProgress. Record continue, retire or replace with a required human reason and expected source revision. Retire and replace permanently close source coordination. Replacement requires an already-started active applicable target in the same exact case, its expected revision, and an atomic target-state check. No item evidence, pinned guidance, business outcome or execution authority is changed. Account write scope and MFA or customer operations-manage scope are required; customer ownership derives from authentication. Applicability is observed before the write, not transactionally locked application evidence.
   * @returns OperationWorkflowPlaybookProgress ReviewPlatformTenantSelfWorkflowPlaybookProgress. Source checklist with the committed durable decision and updated shared revision.
   * @throws ApiError
   */
  public static reviewPlatformTenantSelfWorkflowPlaybookProgress({
    appId,
    scope,
    subjectType,
    subjectId,
    workflow,
    instanceId,
    id,
    requestBody,
  }: {
    /**
     * ReviewPlatformTenantSelfWorkflowPlaybookProgress.app_id. Application UUID; customer identity comes from authentication.
     */
    appId: string,
    /**
     * ReviewPlatformTenantSelfWorkflowPlaybookProgress.scope. Explicit application environment.
     */
    scope: string,
    /**
     * ReviewPlatformTenantSelfWorkflowPlaybookProgress.subject_type. Exact business subject type.
     */
    subjectType: string,
    /**
     * ReviewPlatformTenantSelfWorkflowPlaybookProgress.subject_id. Exact business subject identifier.
     */
    subjectId: string,
    /**
     * ReviewPlatformTenantSelfWorkflowPlaybookProgress.workflow. Exact declared workflow name.
     */
    workflow: string,
    /**
     * ReviewPlatformTenantSelfWorkflowPlaybookProgress.instance_id. Exact business workflow instance.
     */
    instanceId: string,
    /**
     * ReviewPlatformTenantSelfWorkflowPlaybookProgress.id. Durable checklist UUID belonging to this exact case.
     */
    id: string,
    requestBody: OperationWorkflowPlaybookReviewRequest,
  }): CancelablePromise<OperationWorkflowPlaybookProgress> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflow-readiness/playbook-progress/{id}/reviews',
      path: {
        'id': id,
      },
      query: {
        'app_id': appId,
        'scope': scope,
        'subject_type': subjectType,
        'subject_id': subjectId,
        'workflow': workflow,
        'instance_id': instanceId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
