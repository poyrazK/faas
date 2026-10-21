/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppEventPublicationVerification } from '../models/AppEventPublicationVerification.js';
import type { AppEventPublishStatusResponse } from '../models/AppEventPublishStatusResponse.js';
import type { AppPublishEventRequest } from '../models/AppPublishEventRequest.js';
import type { AppPublishEventResponse } from '../models/AppPublishEventResponse.js';
import type { CancelEventExecutionLineageRetryJobRequest } from '../models/CancelEventExecutionLineageRetryJobRequest.js';
import type { ControlEventExecutionLineageCaseBackfillRequest } from '../models/ControlEventExecutionLineageCaseBackfillRequest.js';
import type { ControlEventExecutionLineageRetryJobRequest } from '../models/ControlEventExecutionLineageRetryJobRequest.js';
import type { ControlWorkflowRecoveryPolicyRequest } from '../models/ControlWorkflowRecoveryPolicyRequest.js';
import type { CreateEventExecutionLineageRetryJobRequest } from '../models/CreateEventExecutionLineageRetryJobRequest.js';
import type { EventAcknowledgmentFailureRequest } from '../models/EventAcknowledgmentFailureRequest.js';
import type { EventAcknowledgmentHistory } from '../models/EventAcknowledgmentHistory.js';
import type { EventAcknowledgmentHistoryPage } from '../models/EventAcknowledgmentHistoryPage.js';
import type { EventAcknowledgmentPolicy } from '../models/EventAcknowledgmentPolicy.js';
import type { EventAcknowledgmentPolicyRequest } from '../models/EventAcknowledgmentPolicyRequest.js';
import type { EventAcknowledgmentRequest } from '../models/EventAcknowledgmentRequest.js';
import type { EventAcknowledgmentStatus } from '../models/EventAcknowledgmentStatus.js';
import type { EventBacklogResponse } from '../models/EventBacklogResponse.js';
import type { EventCircuitBreakerPolicy } from '../models/EventCircuitBreakerPolicy.js';
import type { EventCircuitBreakerResponse } from '../models/EventCircuitBreakerResponse.js';
import type { EventConsumerDeliveryHealth } from '../models/EventConsumerDeliveryHealth.js';
import type { EventConsumerExecutionHealth } from '../models/EventConsumerExecutionHealth.js';
import type { EventConsumerHealth } from '../models/EventConsumerHealth.js';
import type { EventDeliveryListResponse } from '../models/EventDeliveryListResponse.js';
import type { EventExecutionLineageCase } from '../models/EventExecutionLineageCase.js';
import type { EventExecutionLineageCaseBackfill } from '../models/EventExecutionLineageCaseBackfill.js';
import type { EventExecutionLineageCases } from '../models/EventExecutionLineageCases.js';
import type { EventExecutionLineageRetryJob } from '../models/EventExecutionLineageRetryJob.js';
import type { EventExecutionLineageRetryJobControls } from '../models/EventExecutionLineageRetryJobControls.js';
import type { EventExecutionLineageRetryJobs } from '../models/EventExecutionLineageRetryJobs.js';
import type { EventExecutionReconciliation } from '../models/EventExecutionReconciliation.js';
import type { EventFanoutAttemptHistoryResponse } from '../models/EventFanoutAttemptHistoryResponse.js';
import type { EventReceiptAttemptHistoryResponse } from '../models/EventReceiptAttemptHistoryResponse.js';
import type { EventReceiptReplayHistoryResponse } from '../models/EventReceiptReplayHistoryResponse.js';
import type { EventReceiptResponse } from '../models/EventReceiptResponse.js';
import type { EventRecipientBlockerBatchRequest } from '../models/EventRecipientBlockerBatchRequest.js';
import type { EventRecipientBlockerBatchResponse } from '../models/EventRecipientBlockerBatchResponse.js';
import type { EventRecipientBlockerResponse } from '../models/EventRecipientBlockerResponse.js';
import type { EventRecoveryControlRequest } from '../models/EventRecoveryControlRequest.js';
import type { EventRecoveryHealth } from '../models/EventRecoveryHealth.js';
import type { EventRecoveryHistory } from '../models/EventRecoveryHistory.js';
import type { EventRecoveryItems } from '../models/EventRecoveryItems.js';
import type { EventRecoveryJob } from '../models/EventRecoveryJob.js';
import type { EventRecoveryJobs } from '../models/EventRecoveryJobs.js';
import type { EventRecoveryNotificationRetryBacklog } from '../models/EventRecoveryNotificationRetryBacklog.js';
import type { EventRecoveryNotificationRetryDecisionDetail } from '../models/EventRecoveryNotificationRetryDecisionDetail.js';
import type { EventRecoveryNotificationRetryHistory } from '../models/EventRecoveryNotificationRetryHistory.js';
import type { EventRecoveryNotificationRetryPreview } from '../models/EventRecoveryNotificationRetryPreview.js';
import type { EventRecoveryNotificationRetryRequest } from '../models/EventRecoveryNotificationRetryRequest.js';
import type { EventRecoveryNotificationRetryResponse } from '../models/EventRecoveryNotificationRetryResponse.js';
import type { EventRecoveryNotifications } from '../models/EventRecoveryNotifications.js';
import type { EventRecoveryPreflight } from '../models/EventRecoveryPreflight.js';
import type { EventRecoveryPreview } from '../models/EventRecoveryPreview.js';
import type { EventRecoveryRateRequest } from '../models/EventRecoveryRateRequest.js';
import type { EventRecoveryRequest } from '../models/EventRecoveryRequest.js';
import type { EventReplayBackfillItemsResponse } from '../models/EventReplayBackfillItemsResponse.js';
import type { EventReplayBackfillJobResponse } from '../models/EventReplayBackfillJobResponse.js';
import type { EventReplayBackfillRequest } from '../models/EventReplayBackfillRequest.js';
import type { EventReplayBackfillRetryRequest } from '../models/EventReplayBackfillRetryRequest.js';
import type { EventReplayBackfillRetryResponse } from '../models/EventReplayBackfillRetryResponse.js';
import type { EventReplayPreviewResponse } from '../models/EventReplayPreviewResponse.js';
import type { EventRetentionHealth } from '../models/EventRetentionHealth.js';
import type { EventRoutingRetryPolicy } from '../models/EventRoutingRetryPolicy.js';
import type { EventRoutingRetryPolicyResponse } from '../models/EventRoutingRetryPolicyResponse.js';
import type { EventSchema } from '../models/EventSchema.js';
import type { EventSchemaRolloutRequest } from '../models/EventSchemaRolloutRequest.js';
import type { EventSchemaRolloutResponse } from '../models/EventSchemaRolloutResponse.js';
import type { EventStorageUsageResponse } from '../models/EventStorageUsageResponse.js';
import type { EventSubscriptionCapacityCommandReceipt } from '../models/EventSubscriptionCapacityCommandReceipt.js';
import type { EventSubscriptionCapacityCommands } from '../models/EventSubscriptionCapacityCommands.js';
import type { EventSubscriptionCapacityRequest } from '../models/EventSubscriptionCapacityRequest.js';
import type { EventSubscriptionDeadlinePage } from '../models/EventSubscriptionDeadlinePage.js';
import type { EventSubscriptionDeadlineWindow } from '../models/EventSubscriptionDeadlineWindow.js';
import type { EventSubscriptionDeliveryControl } from '../models/EventSubscriptionDeliveryControl.js';
import type { EventSubscriptionListResponse } from '../models/EventSubscriptionListResponse.js';
import type { EventSubscriptionPendingSummary } from '../models/EventSubscriptionPendingSummary.js';
import type { EventSubscriptionRateCommandReceipt } from '../models/EventSubscriptionRateCommandReceipt.js';
import type { EventSubscriptionRateCommands } from '../models/EventSubscriptionRateCommands.js';
import type { EventSubscriptionRatePreview } from '../models/EventSubscriptionRatePreview.js';
import type { EventSubscriptionRatePreviewRequest } from '../models/EventSubscriptionRatePreviewRequest.js';
import type { EventSubscriptionRateRequest } from '../models/EventSubscriptionRateRequest.js';
import type { EventSubscriptionResumeRequest } from '../models/EventSubscriptionResumeRequest.js';
import type { EventSubscriptionSchemaVersionsRequest } from '../models/EventSubscriptionSchemaVersionsRequest.js';
import type { EventSubscriptionSchemaVersionsResponse } from '../models/EventSubscriptionSchemaVersionsResponse.js';
import type { PlatformTenantPublishEventResponse } from '../models/PlatformTenantPublishEventResponse.js';
import type { PreviewEventRequest } from '../models/PreviewEventRequest.js';
import type { PreviewEventResponse } from '../models/PreviewEventResponse.js';
import type { Problem } from '../models/Problem.js';
import type { PublishEventBatchRequest } from '../models/PublishEventBatchRequest.js';
import type { PublishEventBatchResponse } from '../models/PublishEventBatchResponse.js';
import type { PublishEventRequest } from '../models/PublishEventRequest.js';
import type { PublishEventResponse } from '../models/PublishEventResponse.js';
import type { RegisterEventSchemaRequest } from '../models/RegisterEventSchemaRequest.js';
import type { RegisterEventSchemaResponse } from '../models/RegisterEventSchemaResponse.js';
import type { ReplayEventFanoutFailureRequest } from '../models/ReplayEventFanoutFailureRequest.js';
import type { ReplayEventFanoutFailureResponse } from '../models/ReplayEventFanoutFailureResponse.js';
import type { ReplayRetryableEventFanoutFailuresRequest } from '../models/ReplayRetryableEventFanoutFailuresRequest.js';
import type { ReplayRetryableEventFanoutFailuresResponse } from '../models/ReplayRetryableEventFanoutFailuresResponse.js';
import type { RequestEventExecutionLineageCaseBackfillRequest } from '../models/RequestEventExecutionLineageCaseBackfillRequest.js';
import type { RetryEventExecutionLineageCaseRequest } from '../models/RetryEventExecutionLineageCaseRequest.js';
import type { SetWorkflowRecoveryPolicyRequest } from '../models/SetWorkflowRecoveryPolicyRequest.js';
import type { SuppressWorkflowRecoveryAutomaticActionRequest } from '../models/SuppressWorkflowRecoveryAutomaticActionRequest.js';
import type { WorkflowEventReplayBackfillRequest } from '../models/WorkflowEventReplayBackfillRequest.js';
import type { WorkflowEventReplayPreviewResponse } from '../models/WorkflowEventReplayPreviewResponse.js';
import type { WorkflowInitialEvidenceDiagnosis } from '../models/WorkflowInitialEvidenceDiagnosis.js';
import type { WorkflowInitialEvidenceRepairRequest } from '../models/WorkflowInitialEvidenceRepairRequest.js';
import type { WorkflowInitialFailures } from '../models/WorkflowInitialFailures.js';
import type { WorkflowInitialRecoveryPreview } from '../models/WorkflowInitialRecoveryPreview.js';
import type { WorkflowInitialRecoveryReview } from '../models/WorkflowInitialRecoveryReview.js';
import type { WorkflowInitialRecoveryReviewDecisionRequest } from '../models/WorkflowInitialRecoveryReviewDecisionRequest.js';
import type { WorkflowInitialRecoveryReviewRequest } from '../models/WorkflowInitialRecoveryReviewRequest.js';
import type { WorkflowInitialRecoveryReviews } from '../models/WorkflowInitialRecoveryReviews.js';
import type { WorkflowRecoveryActionReceipt } from '../models/WorkflowRecoveryActionReceipt.js';
import type { WorkflowRecoveryAutomaticAction } from '../models/WorkflowRecoveryAutomaticAction.js';
import type { WorkflowRecoveryAutomaticQueue } from '../models/WorkflowRecoveryAutomaticQueue.js';
import type { WorkflowRecoveryCancellationRequest } from '../models/WorkflowRecoveryCancellationRequest.js';
import type { WorkflowRecoveryCancellationResponse } from '../models/WorkflowRecoveryCancellationResponse.js';
import type { WorkflowRecoveryHistory } from '../models/WorkflowRecoveryHistory.js';
import type { WorkflowRecoveryPolicyChangePreview } from '../models/WorkflowRecoveryPolicyChangePreview.js';
import type { WorkflowRecoveryPolicyEvaluation } from '../models/WorkflowRecoveryPolicyEvaluation.js';
import type { WorkflowRecoveryPolicyEvaluationRequest } from '../models/WorkflowRecoveryPolicyEvaluationRequest.js';
import type { WorkflowRecoveryPolicyHealth } from '../models/WorkflowRecoveryPolicyHealth.js';
import type { WorkflowRecoveryPolicyResponse } from '../models/WorkflowRecoveryPolicyResponse.js';
import type { WorkflowRecoveryPolicyReview } from '../models/WorkflowRecoveryPolicyReview.js';
import type { WorkflowRecoveryPolicyReviews } from '../models/WorkflowRecoveryPolicyReviews.js';
import type { WorkflowRecoveryReport } from '../models/WorkflowRecoveryReport.js';
import type { WorkflowRecoveryReviewDecisionRequest } from '../models/WorkflowRecoveryReviewDecisionRequest.js';
import type { WorkflowRecoveryReviewStatus } from '../models/WorkflowRecoveryReviewStatus.js';
import type { WorkflowRetryEvidenceDiagnosis } from '../models/WorkflowRetryEvidenceDiagnosis.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class EventsService {
  /**
   * Preview event routing without publishing.
   * Evaluates an event against the authenticated account's enabled
   * subscriptions using the same matcher as asynchronous fanout. The
   * preview is read-only: it does not persist the event or enqueue work.
   * Counts cover every source/type candidate; response lists are bounded
   * samples and `truncated` is true when either sample omits candidates.
   *
   * @returns PreviewEventResponse Routing preview completed without publishing the event.
   * @throws ApiError
   */
  public static previewEvent({
    requestBody,
  }: {
    requestBody: PreviewEventRequest,
  }): CancelablePromise<PreviewEventResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/events:preview',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        500: `The router could not read the current subscription set.`,
      },
    });
  }
  /**
   * Compare intended publication content with retained acceptance.
   * Read-only despite POST: requires an owned app, apps:read/admin, MFA and rate limits.
   * Accepts the original app publish body within its existing 1 MiB limit and
   * derives the same stable app/key identity. Compares normalized type, schema
   * version and semantic JSON data using the ordinary publication identity rules.
   * Occurrence time and trace metadata are excluded. Current schema admission
   * rules do not invalidate verification of previously accepted content.
   * Returns match, conflict or unavailable with HTTP 200. Match and conflict
   * include the retained original receipt from the same comparison snapshot.
   * Conflict is an observation, not a publish rejection. Unavailable cannot
   * distinguish never accepted, pruning or concurrent acceptance not yet visible.
   * No append, fanout, lease, replay, retention refresh or idempotency response
   * cache occurs. Neither match nor conflict establishes consumer execution.
   * Request and stored payloads and raw producer keys are not returned.
   * Read errors remain errors rather than being reported as unavailable.
   * An optional expected_accepted_at returns acceptance=same_acceptance,
   * replacement_acceptance or unavailable independently of content status.
   * Returned receipts describe the currently observed acceptance even on mismatch.
   * Only expected_accepted_at is accepted as a query parameter; use the status read for consumer evidence.
   * An optional expected_app_id in the body returns 409 before comparison if
   * the resolved application UUID differs.
   *
   * @returns AppEventPublicationVerification Content comparison observation with retained acceptance when available.
   * @throws ApiError
   */
  public static verifyAppEventPublication({
    slug,
    requestBody,
    expectedAcceptedAt,
  }: {
    /**
     * Owned application for the exact producer-key content comparison.
     */
    slug: string,
    /**
     * Original application producer-key publication intent.
     */
    requestBody: AppPublishEventRequest,
    /**
     * Exact saved acceptance instant, with original fractional precision; offsets compare by UTC instant.
     */
    expectedAcceptedAt?: string,
  }): CancelablePromise<AppEventPublicationVerification> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/events/verify-publication',
      path: {
        'slug': slug,
      },
      query: {
        'expected_accepted_at': expectedAcceptedAt,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        413: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        500: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Reconcile an app producer key without publishing another event.
   * Requires an owned app, apps:read/admin scopes, MFA and existing rate limits.
   * Derives exactly the same source and event ID as app key publication.
   * Read-only five-second receipt snapshot; no append, claim, replay or retention refresh.
   * Returns processing while retained routing is unsettled, accepted after routing
   * settles, or unavailable when no retained receipt is visible. Both processing
   * and accepted prove durable acceptance. Accepted does not prove delivery or
   * handler success; consult independent recipient execution and workflow evidence.
   * Unavailable cannot distinguish never accepted, concurrent acceptance not yet
   * visible, or pruning. It must not automatically trigger a replacement publish.
   * Evidence includes full routing summaries and a bounded recipient page.
   * Follow evidence.next_after using after; pages are live snapshots and cursors
   * bind account, source, event ID and acceptance identity. Stale cursors fail 400.
   * With expected_accepted_at, acceptance is same_acceptance, replacement_acceptance
   * or unavailable separately from routing status. Evidence describes the currently
   * observed acceptance, which can be a replacement. Never treat its outcomes as
   * proof for the expected acceptance. Raw keys and event payloads are not returned. App renames preserve
   * identity; replacement apps have a different UUID namespace. Matching legacy
   * account/source/ID publication addresses the same identity.
   *
   * @returns AppEventPublishStatusResponse Retained acceptance evidence or explicitly unavailable observation.
   * @throws ApiError
   */
  public static getAppEventPublishStatus({
    slug,
    key,
    after,
    limit = 100,
    expectedAcceptedAt,
  }: {
    /**
     * Owned application whose producer-key namespace is inspected.
     */
    slug: string,
    /**
     * Exact original printable ASCII producer key, without spaces.
     */
    key: string,
    /**
     * Bound receipt recipient continuation cursor from evidence.next_after.
     */
    after?: string,
    /**
     * Number of consumer evidence rows in this page.
     */
    limit?: number,
    /**
     * Saved acceptance timestamp for routing and consumer evidence; preserve its exact precision.
     */
    expectedAcceptedAt?: string,
  }): CancelablePromise<AppEventPublishStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/events/publish-status',
      path: {
        'slug': slug,
      },
      query: {
        'key': key,
        'after': after,
        'limit': limit,
        'expected_accepted_at': expectedAcceptedAt,
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
        500: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Publish with an application-scoped stable producer key.
   * Requires an owned app, MFA and events:publish, deploy:write or admin.
   * Source is app.<canonical-app-UUID>; event ID is key.<SHA-256 hex of key>.
   * The key is exact, case-sensitive printable ASCII without spaces, 1..256 bytes.
   * Identical type, schema version and JSON data return the original retained
   * acceptance with duplicate=true, without extra fanout or storage charge.
   * Changed content for the same app/key returns 409, including concurrent
   * requests. Retained duplicate lookup precedes current schema admission.
   * Occurrence time and trace context are first-publication metadata and do
   * not change duplicate identity. Fanout uses normal account subscriptions
   * matching the generated source; the publishing app is not the only consumer.
   * Deduplication lasts while the normal receipt is retained: settled receipts
   * become eligible for pruning after 30 days; holds can extend this window.
   * After pruning the same key may be accepted again. App renames preserve
   * identity; replacement apps have a new UUID and a new key namespace.
   * Optional expected_app_id must match the resolved canonical app UUID;
   * mismatches return 409 before accepting any publication.
   * No request-wide Idempotency-Key middleware is used. Retry uncertain outcomes
   * with the same app, key and content. Acceptance does not mean delivery success.
   *
   * @returns AppPublishEventResponse Original or new durable event acceptance.
   * @throws ApiError
   */
  public static publishAppEvent({
    slug,
    requestBody,
  }: {
    /**
     * Owned producer application slug.
     */
    slug: string,
    requestBody: AppPublishEventRequest,
  }): CancelablePromise<AppPublishEventResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/events:publish',
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
        409: `code: conflict`,
        413: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Publish one tenant-scoped internal event.
   * Persists a canonical CloudEvents-shaped envelope for later content
   * matching and delivery. The authenticated account owns the event;
   * accountid is server-stamped and a supplied value must match it.
   * Event identity is unique per account and source; reusing an id with
   * different type, schema version, or data returns 409. Older snake_case input attribute
   * names remain accepted during migration. API keys require
   * `events:publish`, `deploy:write`, or `admin`.
   * Matching and delivery are asynchronous follow-up work. The Location header
   * and receipt_url point to GET /v1/events/receipt for delivery evidence.
   * Identical identity retries return the original accepted_at timestamp,
   * including when retained customer event storage is full. New identities
   * exceeding the account count or byte budget return 429 with code
   * event_storage_capacity_exhausted, limit/observed and Retry-After. No ledger
   * or receipt is committed on rejection. Storage is released by receipt
   * retention pruning, not by delivery completion. GET /v1/events/storage
   * reports current usage and plan limits.
   *
   * @returns PublishEventResponse Event accepted for durable processing.
   * @throws ApiError
   */
  public static publishEvent({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: PublishEventRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<PublishEventResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/events:publish',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `code: conflict`,
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
   * Inspect receipt pruning eligibility, retention holds and account storage.
   * Read-only account snapshot under apps:read/admin scopes and MFA.
   * Receipt retention is 30 days after routing settles; unsettled receipts
   * have no pruning deadline. Reports current eligible, upcoming expiring
   * and held receipts using the pruning worker's shared hold predicate.
   * Running backfills pin their acceptance ranges; retained completed
   * backfills pin retryable failed items. Opted-in active recovery jobs hold
   * pending items until admission or job expiry. Backfill reasons take
   * precedence over recovery_pending in primary hold counts. Eligible
   * means the nominal deadline has passed without a current hold, not
   * that pruning will occur immediately.
   * Source/app filters affect receipt counts and samples only. Storage
   * usage and utilization always cover the entire account; utilization
   * is the maximum of count and byte percentages and can exceed 100 after
   * a plan downgrade. Samples are bounded and ordered by nominal deadline,
   * source and id. No payloads, work keys, mutations or reservations.
   *
   * @returns EventRetentionHealth Current retention and account storage health.
   * @throws ApiError
   */
  public static getEventRetentionHealth({
    source,
    app,
    window = '24h',
    limit = 100,
  }: {
    /**
     * Exact event source filter.
     */
    source?: string,
    /**
     * Owned application slug; matches captured or backfilled recipients.
     */
    app?: string,
    /**
     * Expiry lookahead as a Go duration, in whole seconds from 1s to 720h.
     */
    window?: string,
    /**
     * Maximum sampled receipts; aggregate counts include every match.
     */
    limit?: number,
  }): CancelablePromise<EventRetentionHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/events/retention',
      query: {
        'source': source,
        'app': app,
        'window': window,
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
        504: `Snapshot exceeded its bounded read budget.`,
      },
    });
  }
  /**
   * Publish up to 100 independent events in input order.
   * Accepts a nonempty batch of at most 100 events in a body of at most
   * 1 MiB. Authentication, MFA and events:publish/deploy:write/admin scopes
   * match single-event publication. Invalid outer JSON, size or count
   * rejects the entire request before acceptance. Each item otherwise has
   * its own transaction, schema validation, identity and storage charge.
   * Results use zero-based input indexes in input order. accepted and
   * duplicate include a receipt with the original acceptance timestamp.
   * rejected includes a problem; unknown means acceptance could not be
   * confirmed, including an interrupted commit. Retry retryable items or
   * an unanswered request with exactly the original source/id/content.
   * Stable per-event identities provide deduplication; there is no batch
   * transaction or request-wide Idempotency-Key replay. Processing is
   * sequential with a 30-second budget; unattempted items are rejected
   * with retryable=true and code event_publish_not_attempted.
   * Newly accepted items preserve their input acceptance order. Duplicates
   * retain their original position; rejections create none. Concurrent
   * requests may interleave. Execution ordering remains opt-in per keyed
   * lane, and delivery remains at least once.
   *
   * @returns PublishEventBatchResponse Per-event results, including partial or complete rejection.
   * @throws ApiError
   */
  public static publishEventBatch({
    requestBody,
  }: {
    requestBody: PublishEventBatchRequest,
  }): CancelablePromise<PublishEventBatchResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/events:publish-batch',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        413: `Batch request exceeds 1 MiB; no events accepted.`,
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
   * Publish an event for an automation linked to this tenant.
   * Requires platform_tenant:events:manage. The tenant identity comes from
   * the bearer token, and the app must be actively linked to that tenant.
   * The platform scopes the event to that app's published event-triggered
   * workflows; it does not fan out to other apps' subscriptions. The caller's
   * id is scoped by tenant, app, and source, so repeating the same identity
   * and content returns the original durable receipt. The response exposes
   * both the canonical platform id and the caller's client_event_id.
   *
   * @returns PlatformTenantPublishEventResponse Event accepted for tenant-scoped workflow routing.
   * @throws ApiError
   */
  public static publishPlatformTenantSelfEvent({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: PublishEventRequest,
  }): CancelablePromise<PlatformTenantPublishEventResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/apps/{slug}/events:publish',
      path: {
        'slug': slug,
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
   * Read routing and workflow-start evidence for a tenant event.
   * Requires platform_tenant:events:read. Receipts are visible only to the authenticated tenant that published the event for this linked app. Account-operator recovery actions are omitted.
   * @returns EventReceiptResponse Tenant-scoped event receipt and captured workflow recipients.
   * @throws ApiError
   */
  public static getPlatformTenantSelfEventReceipt({
    slug,
    eventId,
    source,
    limit = 100,
    after,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Canonical event id returned by tenant event publication.
     */
    eventId: string,
    /**
     * Event source used with event_id to identify the receipt.
     */
    source: string,
    /**
     * Maximum captured workflow recipients to return in this page.
     */
    limit?: number,
    /**
     * Opaque continuation cursor returned in next_after.
     */
    after?: string,
  }): CancelablePromise<EventReceiptResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/apps/{slug}/events/receipts/{event_id}',
      path: {
        'slug': slug,
        'event_id': eventId,
      },
      query: {
        'source': source,
        'limit': limit,
        'after': after,
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
        500: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Read an ordered batch of exact recipient routing diagnostics.
   * Read-only. One independent snapshot per item, up to 50 items and 65536 body bytes. Duplicate queries are processed independently. All guards validate before reads. Results preserve request order and echo each query. Per-item read failures contain no evidence. A shared five second deadline bounds work; remaining items return read_timeout. No atomic batch snapshot or handler completion guarantee.
   * @returns EventRecipientBlockerBatchResponse Independent ordered results and errors, with Cache-Control no-store.
   * @throws ApiError
   */
  public static getEventRecipientBlockers({
    requestBody,
  }: {
    requestBody: EventRecipientBlockerBatchRequest,
  }): CancelablePromise<EventRecipientBlockerBatchResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/events/recipient-blockers',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Inspect routing blockers for one exact retained recipient.
   * Requires apps:read or admin. Reads only the authenticated account, in
   * one repeatable read snapshot. Matches source, ID, precise acceptance and
   * recipient before inspecting current routing controls. No backlog pagination
   * or consumer aggregation. Returns available for current routing evidence,
   * not_waiting only when the retained recipient has left pending/processing,
   * unavailable for missing or inconsistent retained evidence, or
   * acceptance_replaced when the retained identity has a different timestamp.
   * Not_waiting never implies handler completion or replay attribution.
   * Foreign and missing receipts both return unavailable with receipt_unavailable.
   * Read-only; never follows links, claims work or submits recovery.
   *
   * @returns EventRecipientBlockerResponse Exact recipient routing observation, including unavailable evidence.
   * @throws ApiError
   */
  public static getEventRecipientBlocker({
    source,
    id,
    subscriptionId,
    expectedAcceptedAt,
  }: {
    /**
     * Original event source for the exact recipient lookup.
     */
    source: string,
    /**
     * Original event identifier within the source.
     */
    id: string,
    /**
     * Consumer subscription captured by the original event acceptance.
     */
    subscriptionId: string,
    /**
     * Exact original acceptance instant, preserving fractional precision.
     */
    expectedAcceptedAt: string,
  }): CancelablePromise<EventRecipientBlockerResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/events/recipient-blocker',
      query: {
        'source': source,
        'id': id,
        'subscription_id': subscriptionId,
        'expected_accepted_at': expectedAcceptedAt,
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
        503: `Read timeout or storage unavailable.`,
      },
    });
  }
  /**
   * Discover waiting event consumers and recipient counts.
   * Requires apps:read or admin. Reads only the authenticated account's
   * captured application and workflow recipients plus added backfill
   * recipients in pending or processing routing state, in both whole-event
   * and independent-recipient routing modes. Includes capacity waits before
   * an invocation exists; excludes settled routing and handler execution
   * queues. Returns consumer kind, origin, wait metadata and receipt/history links,
   * never envelope data. Consumers count all matching recipients, independently
   * of either bounded page. Age is measured from durable event acceptance.
   * Recipient pages are oldest accepted first, then receipt and subscription
   * identity; consumer pages use app, subscription and consumer kind. Pass each
   * continuation cursor with the same filters; page sizes may change. Cursors
   * anchor window_at and its age cutoff, while membership and counts remain
   * live on every request. Recovered rows disappear, including cursor rows.
   * Replay behind a cursor requires restarting discovery. This inspection
   * ordering does not guarantee delivery FIFO. A repeatable read keeps each
   * response consistent. Aggregation has a five-second deadline; narrow
   * filters if event_backlog_read_timeout is returned. Responses use
   * Cache-Control no-store. unattributed_receipts counts pending legacy
   * receipts without captured recipients across the account; only the
   * acceptance/age window applies to that count, including with other filters.
   *
   * @returns EventBacklogResponse Matching live backlog metadata and independently paginated consumer counts.
   * @throws ApiError
   */
  public static getEventBacklog({
    app,
    subscriptionId,
    consumerKind,
    origin,
    state,
    waitingReason,
    capacityScope,
    minAgeSeconds,
    after,
    consumersAfter,
    limit = 100,
    consumerLimit = 100,
  }: {
    /**
     * Owned app slug; an unknown or foreign app returns 404.
     */
    app?: string,
    /**
     * Captured or backfilled recipient identifier.
     */
    subscriptionId?: string,
    /**
     * Restrict to application subscriptions or workflow starts.
     */
    consumerKind?: 'application' | 'workflow',
    /**
     * Restrict to recipients captured at acceptance or added by historical backfill.
     */
    origin?: 'acceptance' | 'backfill',
    /**
     * Restrict current routing state.
     */
    state?: 'pending' | 'processing',
    /**
     * Restrict to the currently observed waiting reason before pagination and aggregation.
     */
    waitingReason?: 'circuit_open' | 'circuit_probe_wait' | 'circuit_recovery_rate_limited' | 'subscription_paused' | 'subscription_rate_limited' | 'ordering_blocked' | 'capacity_consumer' | 'capacity_app' | 'capacity_account' | 'routing_in_progress' | 'receipt_processing' | 'retry_backoff' | 'workflow_routing' | 'ready',
    /**
     * Restrict currently pending waits by the last recorded capacity scope.
     */
    capacityScope?: 'consumer' | 'app' | 'account',
    /**
     * Minimum whole seconds since acceptance, relative to window_at.
     */
    minAgeSeconds?: number,
    /**
     * Opaque next_after recipient cursor, bound to account and filters.
     */
    after?: string,
    /**
     * Opaque next_consumers_after cursor, bound to account and filters. Both cursors must share window_at.
     */
    consumersAfter?: string,
    /**
     * Maximum waiting recipients in this page.
     */
    limit?: number,
    /**
     * Maximum consumer summaries in this page.
     */
    consumerLimit?: number,
  }): CancelablePromise<EventBacklogResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/events/backlog',
      query: {
        'app': app,
        'subscription_id': subscriptionId,
        'consumer_kind': consumerKind,
        'origin': origin,
        'state': state,
        'waiting_reason': waitingReason,
        'capacity_scope': capacityScope,
        'min_age_seconds': minAgeSeconds,
        'after': after,
        'consumers_after': consumersAfter,
        'limit': limit,
        'consumer_limit': consumerLimit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect retained customer event storage and account limits.
   * Requires apps:read or admin. Returns usage only for the authenticated
   * account. Logical UTF-8 JSON bytes include envelope, duplicated event data,
   * and immutable recipient snapshots. Pending and settled receipts remain
   * charged until retention pruning. Reserved gregale.* platform events are
   * exempt. Indexes, TOAST compression, ledger and attempt history are outside
   * this budget. A null oldest_pending_at means no customer routing is pending.
   * Responses use Cache-Control no-store.
   *
   * @returns EventStorageUsageResponse Current retained storage and plan limits.
   * @throws ApiError
   */
  public static getEventStorageUsage(): CancelablePromise<EventStorageUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/events/storage',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect acceptance, routing, and execution for one event.
   * Requires apps:read or admin. Reads the retained account/source/id receipt
   * and a bounded page of captured and backfilled recipients. recipient_count
   * and routing_summary cover the immutable acceptance snapshot; separate
   * backfill counts cover added consumers, including those without invocations.
   * Enqueued routing does not imply handler success: keyed cancellation can
   * produce a cancellation receipt instead. The original deterministic
   * invocation is shown when retained; trusted generic replay lineage adds
   * the latest replay and a paginated history URL without replacing the
   * original failure. Recovery actions target the latest retained replay
   * when present. Recovery actions require their existing
   * write scopes and are revalidated when called. Legacy receipts without
   * snapshots report snapshot_captured=false and cannot reconstruct recipients.
   * Pages follow acceptance order then append-only backfill positions, which
   * survive job pruning. Inspection order does not imply delivery order;
   * outcomes may change between requests.
   *
   * @returns EventReceiptResponse Current receipt evidence and recipient page.
   * @throws ApiError
   */
  public static getEventReceipt({
    source,
    id,
    expectedAcceptedAt,
    after,
    limit = 100,
  }: {
    /**
     * Published event source paired with the required ID for this receipt lookup.
     */
    source: string,
    /**
     * Published event ID paired with the required source for this receipt lookup.
     */
    id: string,
    /**
     * Optional original acceptance time. Returns 409 if the retained event belongs to a replacement acceptance.
     */
    expectedAcceptedAt?: string,
    /**
     * Opaque next_after cursor bound to account, source, ID, and retained receipt. Mismatched or stale cursors return 400.
     */
    after?: string,
    /**
     * Maximum recipients in this page.
     */
    limit?: number,
  }): CancelablePromise<EventReceiptResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/events/receipt',
      query: {
        'expected_accepted_at': expectedAcceptedAt,
        'source': source,
        'id': id,
        'after': after,
        'limit': limit,
      },
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
        500: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect retained handler replay history for one captured or backfilled recipient.
   * Requires apps:read or admin. Returns generic invocation replays linked by
   * ledger-owned parent/root identity, newest first by created_at and ID.
   * Original and intermediate execution records may expire independently;
   * surviving descendants remain visible. Legacy replays without trusted
   * lineage cannot be reconstructed from guest headers. Unknown or foreign
   * receipts, recipients and current app owners return 404. Cursors bind the
   * account, source, event ID, subscription and retained receipt identity;
   * mismatched or stale cursors return 400. Cursor anchors remain usable after
   * their execution record expires. Outcomes may change between requests.
   *
   * @returns EventReceiptReplayHistoryResponse Retained replay execution evidence and pagination cursor.
   * @throws ApiError
   */
  public static getEventReceiptReplays({
    source,
    id,
    subscriptionId,
    after,
    limit = 100,
  }: {
    /**
     * Published event source.
     */
    source: string,
    /**
     * Published event ID.
     */
    id: string,
    /**
     * Captured consumer identifier whose retained handler replay history is requested.
     */
    subscriptionId: string,
    /**
     * Opaque next_after cursor for this replay history.
     */
    after?: string,
    /**
     * Maximum retained replays in this page.
     */
    limit?: number,
  }): CancelablePromise<EventReceiptReplayHistoryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/events/receipt/replays',
      query: {
        'source': source,
        'id': id,
        'subscription_id': subscriptionId,
        'after': after,
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
        500: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect retained handler delivery attempts for one captured or backfilled recipient.
   * Requires apps:read or admin. Includes the original async invocation and
   * trusted child replays, ordered by descending attempt history ID. Each
   * claim records its attempt and replay generation atomically with dispatch.
   * A claim does not prove handler execution. Recovered expired leases have
   * outcome unknown; external side effects still require idempotency.
   * Only attempts recorded after rollout and still retained are available.
   * Closed attempts expire after at most 30 days, earlier when result retention
   * expires or their invocation is deleted. Running attempts are not pruned.
   * Missing history does not establish that no delivery occurred. Unknown or
   * foreign receipts, recipients and current app owners return 404.
   * Cursors bind account, source, event ID, recipient and retained receipt;
   * stale or mismatched cursors return 400. New claims do not reorder older
   * pages; a running outcome may settle between reads.
   *
   * @returns EventReceiptAttemptHistoryResponse Retained dispatch evidence and pagination cursor.
   * @throws ApiError
   */
  public static getEventReceiptAttempts({
    source,
    id,
    subscriptionId,
    after,
    limit = 100,
  }: {
    /**
     * Source of the published event whose dispatch attempts are requested.
     */
    source: string,
    /**
     * Identity within the published source for this dispatch-attempt history.
     */
    id: string,
    /**
     * Captured consumer identifier.
     */
    subscriptionId: string,
    /**
     * Opaque next_after cursor for this attempt history.
     */
    after?: string,
    /**
     * Maximum retained attempts per page.
     */
    limit?: number,
  }): CancelablePromise<EventReceiptAttemptHistoryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/events/receipt/attempts',
      query: {
        'source': source,
        'id': id,
        'subscription_id': subscriptionId,
        'after': after,
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
        500: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Preview schema version consumer coverage and payload validation.
   * Read-only. Requires apps:read or admin. Supply a proposed Draft 2020-12 schema, or omit schema to check a registered version. Reports enabled application consumers whose source/type patterns match; version acceptance does not evaluate content filters or guarantee delivery. Optional retained checks scan the newest 1000 account receipts within the acceptance-time range, validate at most 100 matching payloads and at most 4 MiB, and never certify schema compatibility or complete history. No schema is registered and no events or deliveries are created.
   * @returns EventSchemaRolloutResponse Observed consumer coverage and bounded payload validation results.
   * @throws ApiError
   */
  public static previewEventSchemaRollout({
    requestBody,
  }: {
    requestBody: EventSchemaRolloutRequest,
  }): CancelablePromise<EventSchemaRolloutResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/event-schemas:preview-rollout',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid schema, oversized request, or invalid sample or range.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Version is not registered and no proposed schema was supplied.`,
        504: `Bounded rollout observation timed out.`,
      },
    });
  }
  /**
   * Register an immutable event JSON Schema version.
   * Requires deploy:write or admin. The first schema registered for a
   * source/type pair makes schemaversion mandatory on future publishes.
   * Repeating an identical version is safe; changing a version returns 409.
   *
   * @returns RegisterEventSchemaResponse Identical schema version already registered.
   * @throws ApiError
   */
  public static registerEventSchema({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: RegisterEventSchemaRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<RegisterEventSchemaResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/event-schemas',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * List registered versions for one event source and type.
   * Requires apps:read or admin.
   * @returns EventSchema Immutable schema versions ordered by version.
   * @throws ApiError
   */
  public static listEventSchemas({
    source,
    type,
  }: {
    /**
     * Event producer source.
     */
    source: string,
    /**
     * Event type within the source.
     */
    type: string,
  }): CancelablePromise<Array<EventSchema>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-schemas',
      query: {
        'source': source,
        'type': type,
      },
    });
  }
  /**
   * List event subscriptions reconciled for an app.
   * Returns the event subscriptions currently installed from the app's
   * deployment manifest. The response is read-only and account-scoped;
   * use it to verify the router will receive matching published events.
   *
   * @returns EventSubscriptionListResponse Reconciled event subscriptions, in creation order.
   * @throws ApiError
   */
  public static listEventSubscriptions({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<EventSubscriptionListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions',
      path: {
        'slug': slug,
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
   * Preview historical retained events for one subscription.
   * Read-only, account-scoped preview using the current enabled ordinary
   * application subscription and the routing matcher. Work-bound subscriptions
   * are unsupported. Select a half-open platform acceptance-time range; producer
   * event time is not the range key. A fixed cutoff excludes later acceptances.
   * Each page examines at most limit envelopes, so a page can contain no matches
   * and still have next_after. Counts describe this page only. Pass the cursor
   * unchanged with the same target and range; a changed declaration returns 409.
   * Matches contain metadata and original captured-membership status, never
   * payloads. Captured membership does not imply successful delivery or define
   * replay eligibility. No deliveries, replay jobs or retention pins are created.
   * Retention can remove rows between pages. This is a live retained view, not a
   * frozen export or a complete archive; late commits can change visible rows.
   * Settled receipts retain 30 days from routing settlement; pending receipts may
   * survive longer. Earliest retained acceptance is account-wide and does not
   * establish coverage of a requested range. Responses use Cache-Control: no-store
   * and require the existing read scopes and MFA rules.
   *
   * @returns EventReplayPreviewResponse Matching retained-event metadata and page-local counts.
   * @throws ApiError
   */
  public static previewEventReplay({
    slug,
    subscriptionId,
    from,
    until,
    after,
    limit = 50,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Current subscription owned by this app and account.
     */
    subscriptionId: string,
    /**
     * Inclusive acceptance-time lower bound, preceding the cutoff.
     */
    from: string,
    /**
     * Exclusive acceptance-time upper bound; future values are capped at the first page observation.
     */
    until: string,
    /**
     * Opaque next_after continuation bound to account, target, revision, range and fixed cutoff.
     */
    after?: string,
    /**
     * Maximum envelopes examined per page, including nonmatches.
     */
    limit?: number,
  }): CancelablePromise<EventReplayPreviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/replay-preview',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'from': from,
        'until': until,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Subscription changed, disabled or work-bound (event_replay_preview_changed, event_replay_preview_disabled, event_replay_preview_unsupported).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Retained-event read failed or exceeded the five-second deadline (event_replay_preview_read_timeout).`,
      },
    });
  }
  /**
   * Preview retained events for one captured workflow recipient.
   * Read-only, bounded inspection of retained envelopes and their immutable
   * workflow recipient snapshots. The preview evaluates the captured
   * workflow trigger filter, reports its routing checkpoint and durable
   * workflow admission receipt, and never creates a workflow run. It does
   * not inspect events where the workflow was not captured, current workflow
   * definitions, or current workflow quota/target eligibility. Potential
   * admissions are estimates for a future replay policy, not a guarantee.
   * Event payloads and captured definitions are never returned.
   *
   * @returns WorkflowEventReplayPreviewResponse Matching retained workflow-recipient metadata and page-local counts.
   * @throws ApiError
   */
  public static previewWorkflowEventReplay({
    slug,
    workflowName,
    from,
    until,
    after,
    limit = 50,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Workflow name in the immutable recipient snapshot.
     */
    workflowName: string,
    /**
     * Inclusive platform acceptance-time lower bound, preceding the cutoff.
     */
    from: string,
    /**
     * Exclusive acceptance-time upper bound; future values are capped at first-page observation.
     */
    until: string,
    /**
     * Opaque next_after cursor bound to account, app, workflow name, range and fixed cutoff.
     */
    after?: string,
    /**
     * Maximum retained envelopes examined per page, including events without this captured workflow.
     */
    limit?: number,
  }): CancelablePromise<WorkflowEventReplayPreviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-event-replay-preview',
      path: {
        'slug': slug,
      },
      query: {
        'workflow_name': workflowName,
        'from': from,
        'until': until,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Retained-event read failed or exceeded the five-second deadline (workflow_event_replay_preview_read_timeout).`,
      },
    });
  }
  /**
   * Create a resumable historical event backfill.
   * Creates a durable job for one current enabled ordinary subscription.
   * The target declaration is snapshotted at creation, the half-open range
   * uses platform acceptance time, and a fixed cutoff excludes later
   * events. Only surviving retained envelopes are considered; this does
   * not create a complete archive. The required duplicate policy is
   * skip_existing: events originally captured for the target, legacy events
   * with unknown membership, prior target rows, and unsettled receipts are
   * skipped. Only settled receipts with captured snapshots and definitively
   * absent target membership can create deliveries. The scheduler scans at
   * most 100 envelopes per page and holds at most 100 target deliveries
   * pending or processing per job. At most three jobs may run per account,
   * with one active job per subscription. Active jobs protect their
   * requested range from normal settled-receipt pruning. Routing uses the
   * existing recipient lease, capacity, retry, and handler lifecycle.
   * Work-bound subscriptions are unsupported. `enqueued` means the handler
   * invocation was admitted, not completed. Responses use no-store.
   *
   * @returns EventReplayBackfillJobResponse Durable replay job accepted.
   * @throws ApiError
   */
  public static createEventReplayBackfill({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Current ordinary subscription owned by this app and account.
     */
    subscriptionId: string,
    requestBody: EventReplayBackfillRequest,
  }): CancelablePromise<EventReplayBackfillJobResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/replays',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Target subscription is disabled or work-bound (event_replay_backfill_disabled, event_replay_backfill_unsupported).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Durable job creation timed out.`,
      },
    });
  }
  /**
   * Create a durable historical workflow-start backfill.
   * Creates a durable job for one currently eligible event-triggered workflow
   * in the app's preferred live default deployment. The selected workflow
   * definition and trigger are snapshotted at creation. The half-open range
   * uses platform acceptance time with a fixed exclusive cutoff and a
   * maximum 30-day range. Only retained events with a known immutable
   * recipient snapshot, a settled delivered receipt, and no captured
   * membership for this app workflow are considered. Legacy events with an
   * unknown snapshot, events that already captured this workflow, unsettled
   * receipts, filtered events, and any event with a durable workflow
   * admission receipt are skipped. A durable `(outbox_id, workflow
   * recipient_id)` receipt deduplicates run admission across backfill jobs,
   * including after the linked run is pruned. Admission uses the definition
   * pinned to the job and current account/app eligibility and run quotas.
   * Quota and temporary target failures can be retried with the standard
   * backfill retry operation. The scan handles at most 100 envelopes per
   * page; at most three jobs may run per account, with one active job per
   * workflow. Active jobs protect their range from normal settled-receipt
   * pruning. `enqueued` means a workflow run was admitted; run completion is
   * separate. Retained event history is not a complete archive.
   *
   * @returns EventReplayBackfillJobResponse Durable workflow replay job accepted.
   * @throws ApiError
   */
  public static createWorkflowEventReplayBackfill({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: WorkflowEventReplayBackfillRequest,
  }): CancelablePromise<EventReplayBackfillJobResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-event-replays',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Job target conflict, unsupported workflow, or workflow is not currently eligible (event_replay_backfill_conflict, event_replay_backfill_unsupported).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Workflow replay request exceeded its processing deadline.`,
      },
    });
  }
  /**
   * Inspect subscription delivery health across routing and retained execution evidence.
   * Requires `apps:read` or `admin`. Combines independent reads under one bounded deadline, not a single database snapshot. Routing counts cover the live backlog. Current execution counts include bounded retained handler replays. Attempt rates and completion latency use durable per-subscription window evidence, with explicit completeness and retention boundaries. Pending sample blocker counts cover at most 50 oldest pending application recipients and are sampled when truncated. Capacity-wait and waiting-reason counts cover all retained pending application recipients independently of that sample. Component observed_at timestamps describe each read; top-level observed_at marks assembly completion. Configured rate and pause/circuit state are controls, not proof that every recipient is blocked. Latest successful completion is the latest in bounded retained execution coverage, not an all-time watermark.
   * @returns EventConsumerDeliveryHealth Independent routing, execution, and oldest pending sample reads.
   * @throws ApiError
   */
  public static getEventConsumerDeliveryHealth({
    slug,
    subscriptionId,
    window = '5m',
  }: {
    /**
     * Application slug for this operation: inspect combined routing, execution and pending delivery health.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: inspect combined routing, execution and pending delivery health.
     */
    subscriptionId: string,
    /**
     * Retained observation window for this operation: inspect combined routing, execution and pending delivery health.
     */
    window?: '5m' | '15m' | '1h' | '6h' | '24h',
  }): CancelablePromise<EventConsumerDeliveryHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-health',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'window': window,
      },
      errors: {
        400: `Invalid subscription identifier or combined delivery observation window.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `The combined delivery health request exceeded its bounded deadline.`,
      },
    });
  }
  /**
   * Pause a retained lineage diagnostic scan.
   * Requires deploy/write scope and MFA. Scan UUID and original created_at fence the incarnation. Pauses a pending or waiting scan. Repeating pause on a paused scan leaves its metadata unchanged. Completed, cancelled and expired scans return 409. Controls serialize under the same coverage lock as scheduler batches; an already-held batch may commit before the control. Range, counters, existing diagnostics, normal repair progress and original 30-day deadline stay unchanged. Paused scans expire on their original deadline and cannot be replaced until cancelled or expired. These are desired-state controls, not an immutable command ledger; conflicting controls follow server lock order. Read status before retrying after an intervening opposite control.
   * @returns EventExecutionLineageCaseBackfill Current guarded scan state and preserved progress.
   * @throws ApiError
   */
  public static pauseEventExecutionLineageCaseBackfill({
    slug,
    subscriptionId,
    requestId,
    requestBody,
  }: {
    /**
     * Application whose retained diagnostic scan is being paused.
     */
    slug: string,
    /**
     * Event subscription identifier.
     */
    subscriptionId: string,
    /**
     * Reviewed original nonzero diagnostic scan UUID.
     */
    requestId: string,
    requestBody: ControlEventExecutionLineageCaseBackfillRequest,
  }): CancelablePromise<EventExecutionLineageCaseBackfill> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/case-backfill/{requestID}/pause',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'requestID': requestId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid identifier or missing original creation timestamp.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Scan UUID or creation guard changed, or pause/resume requested for a terminal scan; stable code event_lineage_case_backfill_changed.`,
        413: `Request exceeds the 65536-byte body limit.`,
        504: `Control timed out; read current status, then repeat the desired control with the original UUID and creation guard.`,
      },
    });
  }
  /**
   * Resume a retained lineage diagnostic scan.
   * Requires deploy/write scope and MFA. Scan UUID and original created_at fence the incarnation. Resumes a paused scan from its saved cursor and fixed retained range. Repeating resume on a pending or waiting scan leaves metadata unchanged. Completed, cancelled and expired scans return 409. Active bulk retry jobs still govern whether the scheduler can scan. Controls serialize under the same coverage lock as scheduler batches; an already-held batch may commit before the control. Range, counters, existing diagnostics, normal repair progress and original 30-day deadline stay unchanged. Paused scans expire on their original deadline and cannot be replaced until cancelled or expired. These are desired-state controls, not an immutable command ledger; conflicting controls follow server lock order. Read status before retrying after an intervening opposite control.
   * @returns EventExecutionLineageCaseBackfill Current guarded scan state and preserved progress. Context: Resume a retained lineage diagnostic scan.
   * @throws ApiError
   */
  public static resumeEventExecutionLineageCaseBackfill({
    slug,
    subscriptionId,
    requestId,
    requestBody,
  }: {
    /**
     * Application slug. Context: Resume a retained lineage diagnostic scan.
     */
    slug: string,
    /**
     * Event subscription identifier. Context: Resume a retained lineage diagnostic scan.
     */
    subscriptionId: string,
    /**
     * Reviewed original nonzero diagnostic scan UUID. Context: Resume a retained lineage diagnostic scan.
     */
    requestId: string,
    requestBody: ControlEventExecutionLineageCaseBackfillRequest,
  }): CancelablePromise<EventExecutionLineageCaseBackfill> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/case-backfill/{requestID}/resume',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'requestID': requestId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid identifier or missing original creation timestamp. Context: Resume a retained lineage diagnostic scan.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Scan UUID or creation guard changed, or pause/resume requested for a terminal scan; stable code event_lineage_case_backfill_changed. Context: Resume a retained lineage diagnostic scan.`,
        413: `Request exceeds the 65536-byte body limit. Context: Resume a retained lineage diagnostic scan.`,
        504: `Control timed out; read current status, then repeat the desired control with the original UUID and creation guard. Context: Resume a retained lineage diagnostic scan.`,
      },
    });
  }
  /**
   * Cancel a retained lineage diagnostic scan.
   * Requires deploy/write scope and MFA. Scan UUID and original created_at fence the incarnation. Cancels unprocessed diagnostic work while keeping existing case records, cursor and counters. Repeating cancel on a cancelled, completed or expired scan returns its current terminal state unchanged. A cancelled scan cannot be resumed. Controls serialize under the same coverage lock as scheduler batches; an already-held batch may commit before the control. Range, counters, existing diagnostics, normal repair progress and original 30-day deadline stay unchanged. Paused scans expire on their original deadline and cannot be replaced until cancelled or expired. These are desired-state controls, not an immutable command ledger; conflicting controls follow server lock order. Read status before retrying after an intervening opposite control.
   * @returns EventExecutionLineageCaseBackfill Current guarded scan state and preserved progress. Context: Cancel a retained lineage diagnostic scan.
   * @throws ApiError
   */
  public static cancelEventExecutionLineageCaseBackfill({
    slug,
    subscriptionId,
    requestId,
    requestBody,
  }: {
    /**
     * Application slug. Context: Cancel a retained lineage diagnostic scan.
     */
    slug: string,
    /**
     * Event subscription identifier. Context: Cancel a retained lineage diagnostic scan.
     */
    subscriptionId: string,
    /**
     * Reviewed original nonzero diagnostic scan UUID. Context: Cancel a retained lineage diagnostic scan.
     */
    requestId: string,
    requestBody: ControlEventExecutionLineageCaseBackfillRequest,
  }): CancelablePromise<EventExecutionLineageCaseBackfill> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/case-backfill/{requestID}/cancel',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'requestID': requestId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid identifier or missing original creation timestamp. Context: Cancel a retained lineage diagnostic scan.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Scan UUID or creation guard changed, or pause/resume requested for a terminal scan; stable code event_lineage_case_backfill_changed. Context: Cancel a retained lineage diagnostic scan.`,
        413: `Request exceeds the 65536-byte body limit. Context: Cancel a retained lineage diagnostic scan.`,
        504: `Control timed out; read current status, then repeat the desired control with the original UUID and creation guard. Context: Cancel a retained lineage diagnostic scan.`,
      },
    });
  }
  /**
   * Inspect the latest bounded lineage diagnostic scan.
   * Requires read scope. Reports the latest scan only, or not_requested. Counters describe examined retained receipts, not live blockers or complete history. Existing case revisions and forward repair progress are preserved. Expiry is visible immediately after the 30-day work deadline.
   * @returns EventExecutionLineageCaseBackfill Latest scan status and counters.
   * @throws ApiError
   */
  public static getEventExecutionLineageCaseBackfill({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug. Context: Inspect the latest bounded lineage diagnostic scan.
     */
    slug: string,
    /**
     * Event subscription identifier. Context: Inspect the latest bounded lineage diagnostic scan.
     */
    subscriptionId: string,
  }): CancelablePromise<EventExecutionLineageCaseBackfill> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/case-backfill',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Invalid identifier or scan guard.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Latest scan changed or is active; stable code event_lineage_case_backfill_changed.`,
        504: `Request timed out; repeat the same request UUID and previous-request guard.`,
      },
    });
  }
  /**
   * Backfill missing lineage cases from retained admission evidence.
   * Requires deploy/write scope and an existing execution coverage row. The request UUID and expected previous scan UUID fence retries and replacement of a completed, cancelled or expired scan. Only the latest scan is retained in coverage metadata; superseded requests fail their stale guards. Captures an upper receipt ID and resumes a separate bounded cursor. Waits for active bulk lineage retries on this subscription. Inserts missing unresolved/conflicting diagnostics with unique diagnostic IDs, without overwriting existing cases, associating roots, executing handlers or reconstructing outcomes. Retained receipt membership remains live during the scan. Missing or pruned evidence cannot be recovered. Work expires 30 days after request; a deliberate replacement needs a new UUID and the latest request UUID as its guard.
   * @returns EventExecutionLineageCaseBackfill Current paused, cancelled, completed or expired scan, or an empty retained range.
   * @throws ApiError
   */
  public static requestEventExecutionLineageCaseBackfill({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * Application slug. Context: Backfill missing lineage cases from retained admission evidence.
     */
    slug: string,
    /**
     * Event subscription identifier. Context: Backfill missing lineage cases from retained admission evidence.
     */
    subscriptionId: string,
    requestBody: RequestEventExecutionLineageCaseBackfillRequest,
  }): CancelablePromise<EventExecutionLineageCaseBackfill> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/case-backfill',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid identifier or scan guard. Context: Backfill missing lineage cases from retained admission evidence.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Latest scan changed or is active; stable code event_lineage_case_backfill_changed. Context: Backfill missing lineage cases from retained admission evidence.`,
        504: `Request timed out; repeat the same request UUID and previous-request guard. Context: Backfill missing lineage cases from retained admission evidence.`,
      },
    });
  }
  /**
   * Preview unresolved or conflicting retained lineage cases.
   * Requires read scope. Optional exact reason filter; live pagination, not a reserved selection. Review case acceptance timestamps and revisions before submitting a fixed job selection.
   * @returns EventExecutionLineageCases Retained eligible cases.
   * @throws ApiError
   */
  public static previewEventExecutionLineageRetries({
    slug,
    subscriptionId,
    reason,
    after,
    limit = 50,
  }: {
    /**
     * Application slug. Context: Preview unresolved or conflicting retained lineage cases.
     */
    slug: string,
    /**
     * Event subscription identifier. Context: Preview unresolved or conflicting retained lineage cases.
     */
    subscriptionId: string,
    /**
     * Exact reason code; empty matches all reasons.
     */
    reason?: string,
    /**
     * Exclusive outbox cursor.
     */
    after?: number,
    /**
     * Page size; zero selects the default.
     */
    limit?: number,
  }): CancelablePromise<EventExecutionLineageCases> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/retry-preview',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'reason': reason,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `Invalid identifier, selection or pagination.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Case guard changed or retained request identity reused with another selection.`,
        504: `Operation timed out; repeat submission with the same request identity and selection.`,
      },
    });
  }
  /**
   * List retained bulk lineage retry job summaries.
   * Requires read scope and MFA. Returns live retained jobs newest first by original creation time, with request UUID as a descending tie-breaker. Expired jobs are excluded before physical cleanup. Status and progress are current observations; pagination is not a frozen snapshot. Optional attention=any|stalled|retention_risk restricts the list to matching retained pending/paused jobs, before pagination. Any matches either warning. Stalled uses 15 minutes of inactivity; retention risk uses a 24-hour deadline. Attention responses include observation time, page coverage and has_more plus per-job progress/retention diagnostics and the original creation guard. This list is not capped by the health sample or 500-job health scan. Continue with next_after and the same status and attention filters. Jobs created or changing into a filtered status or warning above the cursor can require restarting from the first page. No frozen snapshot or complete backlog count is claimed. Status completed/cancelled with attention is invalid. Summaries omit case selections and results; GET an individual job for details or its original creation guard before cancellation.
   * @returns EventExecutionLineageRetryJobs Retained job summaries; an existing subscription with no jobs returns an empty array.
   * @throws ApiError
   */
  public static listEventExecutionLineageRetryJobs({
    slug,
    subscriptionId,
    status,
    after,
    limit = 50,
    attention,
  }: {
    /**
     * Application slug selecting the owner of this lineage operation. List retained bulk lineage retry job summaries.
     */
    slug: string,
    /**
     * Subscription UUID whose retained lineage records are inspected. List retained bulk lineage retry job summaries.
     */
    subscriptionId: string,
    /**
     * Omit for all retained states.
     */
    status?: 'pending' | 'paused' | 'completed' | 'cancelled',
    /**
     * Continuation cursor returned by next_after; pass unchanged with the same status filter.
     */
    after?: string,
    /**
     * Maximum number of retained records in this response; zero selects the default. List retained bulk lineage retry job summaries.
     */
    limit?: number,
    /**
     * Optional warning filter; applies only to active pending/paused jobs. Empty means ordinary retained listing.
     */
    attention?: '' | 'any' | 'stalled' | 'retention_risk',
  }): CancelablePromise<EventExecutionLineageRetryJobs> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/retry-jobs',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'status': status,
        'after': after,
        'limit': limit,
        'attention': attention,
      },
      errors: {
        400: `Invalid identifier, status filter or pagination.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Listing timed out.`,
      },
    });
  }
  /**
   * Submit a fixed reviewed lineage retry selection.
   * Requires deploy/write scope. At most 200 unique cases with expected revisions, original acceptance timestamps and diagnostic IDs when present. Creation validates every guard atomically. Repeating the same retained UUID and normalized selection returns current job progress; another selection returns 409. Scheduler evaluations and checkpoints commit together. Changed or unavailable cases are reported individually. Repairs lineage only, without handler execution or historical outcome reconstruction. Jobs expire 30 days after submission; use a new UUID after expiry and refresh case guards.
   * @returns EventExecutionLineageRetryJob Previously paused, completed or cancelled job.
   * @throws ApiError
   */
  public static createEventExecutionLineageRetryJob({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * Application slug. Context: Submit a fixed reviewed lineage retry selection.
     */
    slug: string,
    /**
     * Event subscription identifier. Context: Submit a fixed reviewed lineage retry selection.
     */
    subscriptionId: string,
    requestBody: CreateEventExecutionLineageRetryJobRequest,
  }): CancelablePromise<EventExecutionLineageRetryJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/retry-jobs',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid identifier, selection or pagination. Context: Submit a fixed reviewed lineage retry selection.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Case guard changed or retained request identity reused with another selection. Context: Submit a fixed reviewed lineage retry selection.`,
        504: `Operation timed out; repeat submission with the same request identity and selection. Context: Submit a fixed reviewed lineage retry selection.`,
      },
    });
  }
  /**
   * List retained immutable bulk retry control receipts.
   * Requires read scope and MFA. Fences the original job incarnation with expected_created_at. Successful UUID-keyed pause/resume/cancel commands, including new-command no-ops, are ordered by their increasing per-job sequence. Duplicate commands create no new receipt. Legacy controls without a UUID and failed requests are absent. New receipts include server-derived actor_kind, actor_id and credential_type; older receipts omit attribution. Repeats preserve the original actor even under another authorized credential. Receipts are immutable metadata observations, expire with the original job and cascade on job deletion. Optional action, actor_id and credential_type filters combine with AND before pagination. Sequence cursors retain their original per-job meaning and may have gaps; keep filters unchanged across pages or restart at after=0. Legacy unattributed receipts remain in unfiltered history and match action filters but not nonempty attribution filters. Pagination is a live append-only listing, not a reservation; timestamps are informational and sequence defines order. An existing retained job with no receipts returns an empty controls array. Expired/missing jobs return 404 and stale creation guards return 409.
   * @returns EventExecutionLineageRetryJobControls Immutable command receipts, oldest sequence first.
   * @throws ApiError
   */
  public static listEventExecutionLineageRetryJobControls({
    slug,
    subscriptionId,
    requestId,
    expectedCreatedAt,
    after,
    limit = 50,
    action,
    actorId,
    credentialType,
  }: {
    /**
     * Application slug selecting the owner of this lineage operation. List retained immutable bulk retry control receipts.
     */
    slug: string,
    /**
     * Subscription UUID whose retained lineage records are inspected. List retained immutable bulk retry control receipts.
     */
    subscriptionId: string,
    /**
     * Original bulk retry submission UUID whose command receipts are listed. List retained immutable bulk retry control receipts.
     */
    requestId: string,
    /**
     * Original nonzero creation timestamp from the reviewed job.
     */
    expectedCreatedAt: string,
    /**
     * Opaque continuation boundary returned by the preceding control-history page. List retained immutable bulk retry control receipts.
     */
    after?: number,
    /**
     * Maximum number of retained records in this response; zero selects the default. List retained immutable bulk retry control receipts.
     */
    limit?: number,
    /**
     * Exact action filter; empty means all actions.
     */
    action?: '' | 'pause' | 'resume' | 'cancel',
    /**
     * Exact original actor UUID or store for internal calls. UUIDs must use hyphenated form; casing is normalized. Empty means all actors. Attribution-free legacy receipts do not match a nonempty actor filter.
     */
    actorId?: string,
    /**
     * Exact original credential type; empty means all types. Attribution-free legacy receipts do not match a nonempty filter.
     */
    credentialType?: '' | 'session' | 'api_key' | 'deploy_token' | 'platform_tenant_token' | 'internal',
  }): CancelablePromise<EventExecutionLineageRetryJobControls> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/retry-jobs/{requestID}/controls',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'requestID': requestId,
      },
      query: {
        'expected_created_at': expectedCreatedAt,
        'after': after,
        'limit': limit,
        'action': action,
        'actor_id': actorId,
        'credential_type': credentialType,
      },
      errors: {
        400: `Invalid identifier, creation guard, pagination or history filter.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Job creation guard changed; event_lineage_retry_job_changed.`,
        504: `History read timed out.`,
      },
    });
  }
  /**
   * Pause unprocessed cases in a retained bulk lineage retry job.
   * Requires deploy/write scope and MFA. The original creation timestamp fences the job. Optional command_id enables an immutable successful-control receipt scoped to this job incarnation. The job lock commits state and receipt together. Repeating the same command UUID and original inputs returns current job state with its original control_receipt and never reapplies the action, including after another action or terminal completion. Reusing a command UUID with another action returns 409. At most 128 retained receipts per job; new UUIDs beyond that limit return 429 without changing the job. New UUIDs for completed/cancelled jobs return 409; repeated successful commands still work while the job is retained. Receipt result_status and progress describe its recorded outcome; outer job fields describe current state. Without command_id, legacy desired-state behavior remains. Pause preserves selection, results, cursor and expiry; resume continues at the next unprocessed item. An evaluation already holding the lock may commit before pause. Paused jobs still block diagnostic case scans until finished, cancelled or expired.
   * @returns EventExecutionLineageRetryJob Current job; control_receipt contains the immutable original outcome when command_id was supplied.
   * @throws ApiError
   */
  public static pauseEventExecutionLineageRetryJob({
    slug,
    subscriptionId,
    requestId,
    requestBody,
  }: {
    /**
     * Application slug. Context: Pause unprocessed cases in a retained bulk lineage retry job.
     */
    slug: string,
    /**
     * Event subscription identifier. Context: Pause unprocessed cases in a retained bulk lineage retry job.
     */
    subscriptionId: string,
    /**
     * Original nonzero submission UUID.
     */
    requestId: string,
    requestBody: ControlEventExecutionLineageRetryJobRequest,
  }): CancelablePromise<EventExecutionLineageRetryJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/retry-jobs/{requestID}/pause',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'requestID': requestId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid identifier or missing original creation timestamp. Context: Pause unprocessed cases in a retained bulk lineage retry job.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Creation guard changed or a new control targets a terminal job (event_lineage_retry_job_changed), or command UUID reused with different inputs (event_lineage_retry_control_command_changed).`,
        413: `Request exceeds the 65536-byte body limit. Context: Pause unprocessed cases in a retained bulk lineage retry job.`,
        429: `Job already has 128 retained command receipts; event_lineage_retry_control_limit. Existing command repeats remain available.`,
        504: `Control timed out; repeat the same command UUID and inputs when supplied, otherwise read current state before repeating the desired action.`,
      },
    });
  }
  /**
   * Resume unprocessed cases in a paused bulk lineage retry job.
   * Requires deploy/write scope and MFA. The original creation timestamp fences the job. Optional command_id enables an immutable successful-control receipt scoped to this job incarnation. The job lock commits state and receipt together. Repeating the same command UUID and original inputs returns current job state with its original control_receipt and never reapplies the action, including after another action or terminal completion. Reusing a command UUID with another action returns 409. At most 128 retained receipts per job; new UUIDs beyond that limit return 429 without changing the job. New UUIDs for completed/cancelled jobs return 409; repeated successful commands still work while the job is retained. Receipt result_status and progress describe its recorded outcome; outer job fields describe current state. Without command_id, legacy desired-state behavior remains. Pause preserves selection, results, cursor and expiry; resume continues at the next unprocessed item. An evaluation already holding the lock may commit before pause. Paused jobs still block diagnostic case scans until finished, cancelled or expired. Context: Resume unprocessed cases in a paused bulk lineage retry job.
   * @returns EventExecutionLineageRetryJob Current job; control_receipt contains the immutable original outcome when command_id was supplied. Context: Resume unprocessed cases in a paused bulk lineage retry job.
   * @throws ApiError
   */
  public static resumeEventExecutionLineageRetryJob({
    slug,
    subscriptionId,
    requestId,
    requestBody,
  }: {
    /**
     * Application slug. Context: Resume unprocessed cases in a paused bulk lineage retry job.
     */
    slug: string,
    /**
     * Event subscription identifier. Context: Resume unprocessed cases in a paused bulk lineage retry job.
     */
    subscriptionId: string,
    /**
     * Original nonzero submission UUID. Context: Resume unprocessed cases in a paused bulk lineage retry job.
     */
    requestId: string,
    requestBody: ControlEventExecutionLineageRetryJobRequest,
  }): CancelablePromise<EventExecutionLineageRetryJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/retry-jobs/{requestID}/resume',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'requestID': requestId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid identifier or missing original creation timestamp. Context: Resume unprocessed cases in a paused bulk lineage retry job.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Creation guard changed or a new control targets a terminal job (event_lineage_retry_job_changed), or command UUID reused with different inputs (event_lineage_retry_control_command_changed). Context: Resume unprocessed cases in a paused bulk lineage retry job.`,
        413: `Request exceeds the 65536-byte body limit. Context: Resume unprocessed cases in a paused bulk lineage retry job.`,
        429: `Job already has 128 retained command receipts; event_lineage_retry_control_limit. Existing command repeats remain available. Context: Resume unprocessed cases in a paused bulk lineage retry job.`,
        504: `Control timed out; repeat the same command UUID and inputs when supplied, otherwise read current state before repeating the desired action. Context: Resume unprocessed cases in a paused bulk lineage retry job.`,
      },
    });
  }
  /**
   * Cancel unprocessed cases in a retained bulk lineage retry job.
   * Requires deploy/write scope and MFA. The original creation timestamp fences the job incarnation. Cancellation accepts pending or paused jobs and waits for the scheduler job lock, preserves committed evaluations and marks remaining items cancelled. Processed counts evaluated items only. Repeating cancellation with the same original timestamp returns the unchanged terminal job; a completed job stays completed. No evaluation can start for this job after cancellation commits. Existing repairs remain applied. Cancellation does not extend retention, change case revisions or reset coverage. Reusing the original submission UUID and selection returns the cancelled job without restarting it. Optional nonzero command_id atomically records an immutable cancellation acknowledgment with the state change. Repeating the same UUID/action/creation guard returns current job state with the original control_receipt. A completed or already cancelled job remains unchanged and a new UUID records that terminal acknowledgment. Reusing a pause/resume command UUID returns 409 event_lineage_retry_control_command_changed. The shared cap is 128 receipts per job; a new UUID beyond the cap returns 429 without cancelling, while known command repeats and cancellation without command_id remain available. Receipts expire with the original job.
   * @returns EventExecutionLineageRetryJob Cancelled or previously terminal job, preserving evaluated results; optional control_receipt records the original command outcome.
   * @throws ApiError
   */
  public static cancelEventExecutionLineageRetryJob({
    slug,
    subscriptionId,
    requestId,
    requestBody,
  }: {
    /**
     * Application slug. Context: Cancel unprocessed cases in a retained bulk lineage retry job.
     */
    slug: string,
    /**
     * Event subscription identifier. Context: Cancel unprocessed cases in a retained bulk lineage retry job.
     */
    subscriptionId: string,
    /**
     * Original nonzero submission UUID. Context: Cancel unprocessed cases in a retained bulk lineage retry job.
     */
    requestId: string,
    requestBody: CancelEventExecutionLineageRetryJobRequest,
  }): CancelablePromise<EventExecutionLineageRetryJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/retry-jobs/{requestID}/cancel',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'requestID': requestId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid identifier, command UUID or missing original creation timestamp.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Original creation timestamp does not match (event_lineage_retry_job_changed) or command UUID reused for another action (event_lineage_retry_control_command_changed).`,
        413: `Request exceeds the 65536-byte body limit. Context: Cancel unprocessed cases in a retained bulk lineage retry job.`,
        429: `New command UUID exceeds the shared 128-receipt cap; stable code event_lineage_retry_control_limit.`,
        504: `Cancellation timed out; repeat the same job UUID, original creation timestamp and command UUID if supplied.`,
      },
    });
  }
  /**
   * Inspect retained bulk lineage retry progress and per-case results.
   * Requires read scope. Completed means all selected cases were evaluated or skipped; individual cases may remain unresolved, conflicted, changed or unavailable. Paused jobs preserve results and leave unprocessed items pending until resumed, cancelled or expired. Cancelled jobs preserve evaluated results and mark unprocessed items cancelled. Results describe recorded evaluations, not live lineage status.
   * @returns EventExecutionLineageRetryJob Retained job and case results.
   * @throws ApiError
   */
  public static getEventExecutionLineageRetryJob({
    slug,
    subscriptionId,
    requestId,
  }: {
    /**
     * Application slug. Context: Inspect retained bulk lineage retry progress and per-case results.
     */
    slug: string,
    /**
     * Event subscription identifier. Context: Inspect retained bulk lineage retry progress and per-case results.
     */
    subscriptionId: string,
    /**
     * Original nonzero submission UUID. Context: Inspect retained bulk lineage retry progress and per-case results.
     */
    requestId: string,
  }): CancelablePromise<EventExecutionLineageRetryJob> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/retry-jobs/{requestID}',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'requestID': requestId,
      },
      errors: {
        400: `Invalid identifier, selection or pagination. Context: Inspect retained bulk lineage retry progress and per-case results.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Case guard changed or retained request identity reused with another selection. Context: Inspect retained bulk lineage retry progress and per-case results.`,
        504: `Operation timed out; repeat submission with the same request identity and selection. Context: Inspect retained bulk lineage retry progress and per-case results.`,
      },
    });
  }
  /**
   * List retained lineage repair cases.
   * Requires read scope. Cases cover recorded retained scans, not all historical gaps. No event payloads or handler errors are returned.
   * @returns EventExecutionLineageCases Retained cases or the saved guarded retry result.
   * @throws ApiError
   */
  public static listEventExecutionLineageCases({
    slug,
    subscriptionId,
    after,
    limit = 50,
  }: {
    /**
     * Application slug. Context: List retained lineage repair cases.
     */
    slug: string,
    /**
     * Application event subscription identifier.
     */
    subscriptionId: string,
    /**
     * Exclusive outbox ID cursor.
     */
    after?: number,
    /**
     * Page size; zero selects the server default.
     */
    limit?: number,
  }): CancelablePromise<EventExecutionLineageCases> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/cases',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `Invalid identifier, pagination or retry guard.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Case revision or request identity changed.`,
        504: `Lineage operation timed out; repeat the same guarded request.`,
      },
    });
  }
  /**
   * Recheck one reviewed lineage case with an idempotent guard.
   * Requires deploy/write scope. Request UUID, expected revision and any diagnostic identity fence each evaluation; duplicate requests return the original result for 30 days. Reuses authoritative evidence checks and does not reset scan progress or established coverage. Known receipt acceptance and root incarnation remain guarded. No event payloads or handler errors are returned.
   * @returns EventExecutionLineageCase Retained cases or the saved guarded retry result. Context: Recheck one reviewed lineage case with an idempotent guard.
   * @throws ApiError
   */
  public static retryEventExecutionLineageCase({
    slug,
    subscriptionId,
    outboxId,
    requestBody,
  }: {
    /**
     * Application slug. Context: Recheck one reviewed lineage case with an idempotent guard.
     */
    slug: string,
    /**
     * Application event subscription identifier. Context: Recheck one reviewed lineage case with an idempotent guard.
     */
    subscriptionId: string,
    /**
     * Retained case outbox identifier.
     */
    outboxId: number,
    requestBody: RetryEventExecutionLineageCaseRequest,
  }): CancelablePromise<EventExecutionLineageCase> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation/cases/{outboxID}/retry',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'outboxID': outboxId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid identifier, pagination or retry guard. Context: Recheck one reviewed lineage case with an idempotent guard.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Case revision or request identity changed. Context: Recheck one reviewed lineage case with an idempotent guard.`,
        504: `Lineage operation timed out; repeat the same guarded request. Context: Recheck one reviewed lineage case with an idempotent guard.`,
      },
    });
  }
  /**
   * Inspect durable execution coverage reconciliation.
   * Requires `apps:read` or `admin`. Bounded durable scans repair unambiguous lineage from retained captured recipients, deterministic delivery identity and matching root content. Historical unknown outcomes are not reconstructed. Retained app executions without authoritative ledger lineage block completion, including unrelated app invocations. The scheduler establishes a new collection boundary after blockers clear. Recorded losses and unknown outcomes remain applicable.
   * @returns EventExecutionReconciliation Current reconciliation status, including completed or already qualified requests.
   * @throws ApiError
   */
  public static getEventExecutionReconciliation({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug. Context: Inspect durable execution coverage reconciliation.
     */
    slug: string,
    /**
     * Application event subscription identifier. Context: Inspect durable execution coverage reconciliation.
     */
    subscriptionId: string,
  }): CancelablePromise<EventExecutionReconciliation> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Invalid subscription identifier.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Subscription reconciliation request timed out.`,
      },
    });
  }
  /**
   * Request a forward-only execution coverage boundary.
   * Requires `apps:deploy` or `admin`; requests are idempotent per subscription. Bounded durable scans repair unambiguous lineage from retained captured recipients, deterministic delivery identity and matching root content. Historical unknown outcomes are not reconstructed. Retained app executions without authoritative ledger lineage block completion, including unrelated app invocations. The scheduler establishes a new collection boundary after blockers clear. Recorded losses and unknown outcomes remain applicable.
   * @returns EventExecutionReconciliation Current reconciliation status, including completed or already qualified requests. Context: Request a forward-only execution coverage boundary.
   * @throws ApiError
   */
  public static requestEventExecutionReconciliation({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug. Context: Request a forward-only execution coverage boundary.
     */
    slug: string,
    /**
     * Application event subscription identifier. Context: Request a forward-only execution coverage boundary.
     */
    subscriptionId: string,
  }): CancelablePromise<EventExecutionReconciliation> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Invalid subscription identifier. Context: Request a forward-only execution coverage boundary.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Subscription reconciliation request timed out. Context: Request a forward-only execution coverage boundary.`,
      },
    });
  }
  /**
   * Inspect consumer execution health over a retained history window.
   * Requires `apps:read` or `admin`. Reports current retained execution states and windowed attempt outcomes for admitted deliveries and handler replays. Current state coverage is bounded by retained roots. Windowed outcomes use an immutable per-subscription ledger with 30-day retention and independent completeness diagnostics; source cleanup does not erase retained ledger outcomes. Legacy ledger baselines remain unknown until requested forward-only reconciliation establishes a new boundary. Counts are not unique event totals. Completion latency starts at original event acceptance.
   * @returns EventConsumerExecutionHealth Retained execution states and windowed handler outcomes.
   * @throws ApiError
   */
  public static getEventConsumerExecutionHealth({
    slug,
    subscriptionId,
    window = '5m',
  }: {
    /**
     * Application slug for this operation: inspect consumer execution health over a retained history window.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: inspect consumer execution health over a retained history window.
     */
    subscriptionId: string,
    /**
     * Retained observation window for this operation: inspect consumer execution health over a retained history window.
     */
    window?: '5m' | '15m' | '1h' | '6h' | '24h',
  }): CancelablePromise<EventConsumerExecutionHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-health',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'window': window,
      },
      errors: {
        400: `Invalid subscription identifier or observation window.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Subscription control request timed out.`,
      },
    });
  }
  /**
   * Inspect consumer routing health over a retained history window.
   * Requires `apps:read` or `admin`. Current backlog is live. Rates and routing latency use bounded recorded outcomes; history_compacted marks incomplete windows. Routing success measures admission before invocation execution.
   * @returns EventConsumerHealth Consumer backlog and windowed recorded routing outcomes.
   * @throws ApiError
   */
  public static getEventConsumerHealth({
    slug,
    subscriptionId,
    window = '5m',
  }: {
    /**
     * Application slug for this operation: inspect consumer routing health over a retained history window.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: inspect consumer routing health over a retained history window.
     */
    subscriptionId: string,
    /**
     * Retained observation window for this operation: inspect consumer routing health over a retained history window.
     */
    window?: '5m' | '15m' | '1h' | '6h' | '24h',
  }): CancelablePromise<EventConsumerHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/health',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'window': window,
      },
      errors: {
        400: `Inspect consumer routing health over a retained history window: invalid subscription identifier or observation window.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Inspect consumer routing health over a retained history window: subscription control request timed out.`,
      },
    });
  }
  /**
   * Inspect consumer circuit breaker state.
   * Requires `apps:read` or `admin`. Runtime controls apply to retained pending routing for one application consumer. Manual pauses remain independent. Invocation execution failures do not count. PUT uses defaults for omitted fields and resets the observation window. Reset preserves policy and manual pause. Disable removes automatic gating. Status reports the last durable transition; cooldown progresses when work is available.
   * @returns EventCircuitBreakerResponse Durable breaker configuration and state.
   * @throws ApiError
   */
  public static getEventCircuitBreaker({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug for this operation: inspect consumer circuit breaker state.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: inspect consumer circuit breaker state.
     */
    subscriptionId: string,
  }): CancelablePromise<EventCircuitBreakerResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/circuit-breaker',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Invalid subscription identifier or circuit breaker policy.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Inspect consumer circuit breaker state: subscription control request timed out.`,
      },
    });
  }
  /**
   * Enable or replace a consumer circuit breaker policy.
   * Requires `deploy:write` or `admin`. Runtime controls apply to retained pending routing for one application consumer. Manual pauses remain independent. Invocation execution failures do not count. PUT uses defaults for omitted fields and resets the observation window. Reset preserves policy and manual pause. Disable removes automatic gating. Status reports the last durable transition; cooldown progresses when work is available.
   * @returns EventCircuitBreakerResponse Enable or replace a consumer circuit breaker policy: durable breaker configuration and state.
   * @throws ApiError
   */
  public static setEventCircuitBreaker({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * Application slug for this operation: enable or replace a consumer circuit breaker policy.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: enable or replace a consumer circuit breaker policy.
     */
    subscriptionId: string,
    requestBody: EventCircuitBreakerPolicy,
  }): CancelablePromise<EventCircuitBreakerResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/circuit-breaker',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Enable or replace a consumer circuit breaker policy: invalid subscription identifier or circuit breaker policy.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Enable or replace a consumer circuit breaker policy: subscription control request timed out.`,
      },
    });
  }
  /**
   * Disable a consumer circuit breaker.
   * Disable a consumer circuit breaker. Requires `deploy:write` or `admin`. Runtime controls apply to retained pending routing for one application consumer. Manual pauses remain independent. Invocation execution failures do not count. PUT uses defaults for omitted fields and resets the observation window. Reset preserves policy and manual pause. Disable removes automatic gating. Status reports the last durable transition; cooldown progresses when work is available.
   * @returns EventCircuitBreakerResponse Disable a consumer circuit breaker: durable breaker configuration and state.
   * @throws ApiError
   */
  public static disableEventCircuitBreaker({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug for this operation: disable a consumer circuit breaker.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: disable a consumer circuit breaker.
     */
    subscriptionId: string,
  }): CancelablePromise<EventCircuitBreakerResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/circuit-breaker',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Disable a consumer circuit breaker: invalid subscription identifier or circuit breaker policy.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Disable a consumer circuit breaker: subscription control request timed out.`,
      },
    });
  }
  /**
   * Close an enabled circuit breaker and start a fresh observation window.
   * Close an enabled circuit breaker and start a fresh observation window. Requires `deploy:write` or `admin`. Runtime controls apply to retained pending routing for one application consumer. Manual pauses remain independent. Invocation execution failures do not count. PUT uses defaults for omitted fields and resets the observation window. Reset preserves policy and manual pause. Disable removes automatic gating. Status reports the last durable transition; cooldown progresses when work is available.
   * @returns EventCircuitBreakerResponse Close an enabled circuit breaker and start a fresh observation window: durable breaker configuration and state.
   * @throws ApiError
   */
  public static resetEventCircuitBreaker({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug for this operation: close an enabled circuit breaker and start a fresh observation window.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: close an enabled circuit breaker and start a fresh observation window.
     */
    subscriptionId: string,
  }): CancelablePromise<EventCircuitBreakerResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/circuit-breaker/reset',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Close an enabled circuit breaker and start a fresh observation window: invalid subscription identifier or circuit breaker policy.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Close an enabled circuit breaker and start a fresh observation window: subscription control request timed out.`,
      },
    });
  }
  /**
   * Inspect schema version selection for future events.
   * Requires `apps:read` or `admin`. Captured events retain their acceptance-time selection. An empty selection accepts all versions, including unversioned envelopes. Only current app subscriptions can be configured.
   * @returns EventSubscriptionSchemaVersionsResponse Selected schema versions; an empty array accepts all versions.
   * @throws ApiError
   */
  public static getEventSubscriptionSchemaVersions({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug for this operation: inspect schema version selection for future events.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: inspect schema version selection for future events.
     */
    subscriptionId: string,
  }): CancelablePromise<EventSubscriptionSchemaVersionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/schema-versions',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Invalid subscription identifier or schema version selection.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Inspect schema version selection for future events: subscription control request timed out.`,
      },
    });
  }
  /**
   * Replace schema version selection for future events.
   * Requires `deploy:write` or `admin`. Captured events retain their acceptance-time selection. An empty selection accepts all versions, including unversioned envelopes. Only current app subscriptions can be configured.
   * @returns EventSubscriptionSchemaVersionsResponse Replace schema version selection for future events: selected schema versions; an empty array accepts all versions.
   * @throws ApiError
   */
  public static setEventSubscriptionSchemaVersions({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * Application slug for this operation: replace schema version selection for future events.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: replace schema version selection for future events.
     */
    subscriptionId: string,
    requestBody: EventSubscriptionSchemaVersionsRequest,
  }): CancelablePromise<EventSubscriptionSchemaVersionsResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/schema-versions',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Replace schema version selection for future events: invalid subscription identifier or schema version selection.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Replace schema version selection for future events: subscription control request timed out.`,
      },
    });
  }
  /**
   * Accept all schema versions for future events.
   * Accept all schema versions for future events. Requires `deploy:write` or `admin`. Captured events retain their acceptance-time selection. An empty selection accepts all versions, including unversioned envelopes. Only current app subscriptions can be configured.
   * @returns EventSubscriptionSchemaVersionsResponse Accept all schema versions for future events: selected schema versions; an empty array accepts all versions.
   * @throws ApiError
   */
  public static resetEventSubscriptionSchemaVersions({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug for this operation: accept all schema versions for future events.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: accept all schema versions for future events.
     */
    subscriptionId: string,
  }): CancelablePromise<EventSubscriptionSchemaVersionsResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/schema-versions',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Accept all schema versions for future events: invalid subscription identifier or schema version selection.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Accept all schema versions for future events: subscription control request timed out.`,
      },
    });
  }
  /**
   * Inspect routing retry policy for future events.
   * Requires `apps:read` or `admin`. Captured events retain their acceptance-time policy. Omitted policy uses legacy defaults. Only current app subscriptions can be configured.
   * @returns EventRoutingRetryPolicyResponse Effective policy and whether explicitly configured.
   * @throws ApiError
   */
  public static getEventSubscriptionRetryPolicy({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug for this operation: inspect routing retry policy for future events.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: inspect routing retry policy for future events.
     */
    subscriptionId: string,
  }): CancelablePromise<EventRoutingRetryPolicyResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/retry-policy',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Invalid subscription identifier or retry policy.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Inspect routing retry policy for future events: subscription control request timed out.`,
      },
    });
  }
  /**
   * Replace routing retry policy for future events.
   * Requires `deploy:write` or `admin`. Captured events retain their acceptance-time policy. Omitted policy uses legacy defaults. Only current app subscriptions can be configured.
   * @returns EventRoutingRetryPolicyResponse Replace routing retry policy for future events: effective policy and whether explicitly configured.
   * @throws ApiError
   */
  public static setEventSubscriptionRetryPolicy({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * Application slug for this operation: replace routing retry policy for future events.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: replace routing retry policy for future events.
     */
    subscriptionId: string,
    requestBody: EventRoutingRetryPolicy,
  }): CancelablePromise<EventRoutingRetryPolicyResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/retry-policy',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Replace routing retry policy for future events: invalid subscription identifier or retry policy.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Replace routing retry policy for future events: subscription control request timed out.`,
      },
    });
  }
  /**
   * Restore legacy routing retry defaults for future events.
   * Restore legacy routing retry defaults for future events. Requires `deploy:write` or `admin`. Captured events retain their acceptance-time policy. Omitted policy uses legacy defaults. Only current app subscriptions can be configured.
   * @returns EventRoutingRetryPolicyResponse Restore legacy routing retry defaults for future events: effective policy and whether explicitly configured.
   * @throws ApiError
   */
  public static resetEventSubscriptionRetryPolicy({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug for this operation: restore legacy routing retry defaults for future events.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: restore legacy routing retry defaults for future events.
     */
    subscriptionId: string,
  }): CancelablePromise<EventRoutingRetryPolicyResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/retry-policy',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Restore legacy routing retry defaults for future events: invalid subscription identifier or retry policy.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Restore legacy routing retry defaults for future events: subscription control request timed out.`,
      },
    });
  }
  /**
   * Inspect one consumer pause, pacing and backlog.
   * Requires `apps:read` or `admin`. Controls survive deployment and subscription removal. Already admitted invocations continue. Resume defaults to ten admissions per second and zero removes pacing.
   * @returns EventSubscriptionDeliveryControl Current control configuration and waiting recipient counts.
   * @throws ApiError
   */
  public static getEventSubscriptionDeliveryControl({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug for this operation: inspect one consumer pause, pacing and backlog.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: inspect one consumer pause, pacing and backlog.
     */
    subscriptionId: string,
  }): CancelablePromise<EventSubscriptionDeliveryControl> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Invalid subscription identifier or drain rate.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Inspect one consumer pause, pacing and backlog: subscription control request timed out.`,
      },
    });
  }
  /**
   * Pause new routing admissions for one event consumer.
   * Requires `deploy:write` or `admin`. Controls survive deployment and subscription removal. Already admitted invocations continue. Resume defaults to ten admissions per second and zero removes pacing.
   * @returns EventSubscriptionDeliveryControl Pause new routing admissions for one event consumer: current control configuration and waiting recipient counts.
   * @throws ApiError
   */
  public static pauseEventSubscription({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug for this operation: pause new routing admissions for one event consumer.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: pause new routing admissions for one event consumer.
     */
    subscriptionId: string,
  }): CancelablePromise<EventSubscriptionDeliveryControl> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/pause',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Pause new routing admissions for one event consumer: invalid subscription identifier or drain rate.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Pause new routing admissions for one event consumer: subscription control request timed out.`,
      },
    });
  }
  /**
   * Resume one event consumer with controlled draining.
   * Resume one event consumer with controlled draining. Requires `deploy:write` or `admin`. Controls survive deployment and subscription removal. Already admitted invocations continue. Resume defaults to ten admissions per second and zero removes pacing.
   * @returns EventSubscriptionDeliveryControl Resume one event consumer with controlled draining: current control configuration and waiting recipient counts.
   * @throws ApiError
   */
  public static resumeEventSubscription({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * Application slug for this operation: resume one event consumer with controlled draining.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: resume one event consumer with controlled draining.
     */
    subscriptionId: string,
    requestBody: EventSubscriptionResumeRequest,
  }): CancelablePromise<EventSubscriptionDeliveryControl> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/resume',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Resume one event consumer with controlled draining: invalid subscription identifier or drain rate.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Resume one event consumer with controlled draining: subscription control request timed out.`,
      },
    });
  }
  /**
   * List pending deliveries by effective deadline.
   * Read-only deadline triage for retained captured pending application recipients of one owned subscription, without payloads. Orders by effective deadline, acceptance time and internal receipt ID ascending; this does not guarantee delivery order. Optional deadline_from is inclusive and deadline_before exclusive. Expired/upcoming filters compare to the first page window_at, which also bounds acceptance time. Each page has a fresh observation and live routing state, so expired flags and waiting reasons use observed_at. Cursor is scoped to account, application, subscription, normalized filters and page size, expires after 15 minutes, and is navigational metadata rather than authorization. Membership and deadlines can change between pages; newly captured or replayed work can appear, disappear, move or be skipped. This is not a frozen snapshot, reservation or deadline-safety certificate. No effective deadline means exclusion. Reads have fixed time budgets; timeout returns no partial page.
   * @returns EventSubscriptionDeadlinePage A bounded live page of pending deliveries with effective deadlines.
   * @throws ApiError
   */
  public static listEventSubscriptionDeadlines({
    slug,
    subscriptionId,
    deadlineFrom,
    deadlineBefore,
    status = 'all',
    cursor,
    limit = 50,
    withinMillis,
    waitingReason,
  }: {
    /**
     * Application slug for a deadline-ordered pending-delivery listing.
     */
    slug: string,
    /**
     * Owned application subscription UUID whose pending deadlines are being investigated.
     */
    subscriptionId: string,
    /**
     * Inclusive effective-deadline bound, normalized to UTC microseconds.
     */
    deadlineFrom?: string,
    /**
     * Exclusive effective-deadline bound, later than deadline_from after normalization.
     */
    deadlineBefore?: string,
    /**
     * Expiry classification at the first page window_at; keep unchanged across pages.
     */
    status?: 'all' | 'expired' | 'upcoming',
    /**
     * Opaque continuation; preserve scope, filters and page size. Expires 15 minutes after the first page.
     */
    cursor?: string,
    /**
     * Maximum returned recipients; keep unchanged across pages.
     */
    limit?: number,
    /**
     * Optional upcoming window strictly after the first page window_at and before window_at plus this horizon. Normalizes all/omitted status to upcoming; expired status is rejected. Keep unchanged across pages.
     */
    withinMillis?: number,
    /**
     * Optional known observed backlog waiting reason, applied before pagination at each page observation. Keep unchanged across pages; reasons remain live.
     */
    waitingReason?: 'circuit_open' | 'circuit_probe_wait' | 'circuit_recovery_rate_limited' | 'subscription_paused' | 'subscription_rate_limited' | 'ordering_blocked' | 'capacity_consumer' | 'capacity_app' | 'capacity_account' | 'routing_in_progress' | 'receipt_processing' | 'retry_backoff' | 'workflow_routing' | 'ready',
  }): CancelablePromise<EventSubscriptionDeadlinePage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/deadlines',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'deadline_from': deadlineFrom,
        'deadline_before': deadlineBefore,
        'status': status,
        'cursor': cursor,
        'limit': limit,
        'within_millis': withinMillis,
        'waiting_reason': waitingReason,
      },
      errors: {
        400: `Subscription UUID, deadline filters, page size or cursor are invalid or expired.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Deadline listing exceeded its read budget; no partial page was returned.`,
      },
    });
  }
  /**
   * Count upcoming delivery deadlines and observed waiting reasons.
   * Read-only, payload-free full pending summary and deadline cohorts from one Repeatable Read snapshot. Upcoming deadlines are strictly after summary.observed_at and strictly before window_end_at. Already-expired recipients are separate in summary.already_expired_count. Each cohort has an exact partition of observed backlog waiting reasons, including zero keys. Counts have no sample cap and include only retained captured pending application recipients with effective deadlines; processing, future arrivals and recipients without deadlines are excluded from cohorts. Waiting reasons are observations, not unique causes or execution guarantees. Fixed request and statement budgets apply; timeout returns no partial counts. Require summary.complete before interpreting coverage or recovery. This does not predict admissions, change state, reserve capacity or extend deadlines.
   * @returns EventSubscriptionDeadlineWindow Full pending summary, separate upcoming and expired counts, and exact waiting-reason partitions.
   * @throws ApiError
   */
  public static getEventSubscriptionDeadlineWindow({
    slug,
    subscriptionId,
    withinMillis,
  }: {
    /**
     * Application slug for full deadline-window monitoring.
     */
    slug: string,
    /**
     * Owned application subscription UUID whose retained pending deadlines are being counted.
     */
    subscriptionId: string,
    /**
     * Positive whole-millisecond horizon from this snapshot observation; upper deadline boundary is exclusive.
     */
    withinMillis: number,
  }): CancelablePromise<EventSubscriptionDeadlineWindow> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/deadline-window',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'within_millis': withinMillis,
      },
      errors: {
        400: `Subscription UUID or within_millis is invalid, missing, repeated or unsupported.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Deadline-window read exceeded its budget; no partial counts were returned.`,
      },
    });
  }
  /**
   * Read complete consumer pending counts and effective deadlines.
   * Read-only, payload-free counts and earliest effective deadline across all retained captured pending application recipients for this subscription. One Repeatable Read snapshot includes current controls. Processing work, future arrivals and publications awaiting recipient capture are excluded. No sample cap; request and statement budgets bound scan time. Timeout returns 504 without partial counts. Incomplete or absent evidence is unknown, never zero or a successful recovery. Counts do not model future admission or guarantee deadline safety.
   * @returns EventSubscriptionPendingSummary Complete pending counts, effective deadlines and current controls; inspect complete before monitoring.
   * @throws ApiError
   */
  public static getEventSubscriptionPendingSummary({
    slug,
    subscriptionId,
  }: {
    /**
     * Application slug for a full pending summary.
     */
    slug: string,
    /**
     * Owned application subscription UUID whose retained pending recipients are being counted.
     */
    subscriptionId: string,
  }): CancelablePromise<EventSubscriptionPendingSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/pending-summary',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Subscription UUID is invalid.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Full pending-summary read exceeded its budget; coverage is unknown.`,
      },
    });
  }
  /**
   * Preview consumer pacing and delivery deadline risk.
   * Read-only advisory preview of proposed interval/burst settings from one owned subscription snapshot. Requires read scope. Returns the reviewed bucket revision for a subsequent guarded command, without allocating a command ID or receipt, consuming tokens, claiming work or reserving capacity. Models the oldest 1000 captured pending recipients in acceptance order, with current scheduled times and the legacy per-second cap. This models admission rather than completion and does not promise scheduler ordering. Pause, circuit, lease and capacity constraints can delay actual admission without bound; inspect constraints and completeness. Deadline-risk identities are limited to 100.
   * @returns EventSubscriptionRatePreview Advisory rate-only admission model and bounded deadline evidence; completeness does not certify deadline safety.
   * @throws ApiError
   */
  public static previewEventSubscriptionRateLimit({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * Application slug for a read-only consumer pacing preview.
     */
    slug: string,
    /**
     * Owned application subscription UUID whose pacing is being reviewed.
     */
    subscriptionId: string,
    requestBody: EventSubscriptionRatePreviewRequest,
  }): CancelablePromise<EventSubscriptionRatePreview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/rate-limit/preview',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Proposed preview settings or subscription UUID are invalid.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Read-only pacing preview exceeded its request deadline; retry with a fresh observation.`,
      },
    });
  }
  /**
   * Configure rate bucket.
   * Configure rate bucket: Durable per-subscription pacing shared by admission workers. Mutations require deploy:write or admin and return an applied or rejected receipt; revision mismatch is a recorded rejection with no configuration change. Repeating an unexpired command UUID with identical inputs returns its original receipt. Reads require read scope. Existing pause, circuit, per-second drain caps, attempts and delivery deadlines remain in force. Capacity observations reserve nothing.
   * @returns EventSubscriptionRateCommandReceipt Configure rate bucket: Retained rate command evidence; inspect outcome for applied or rejected.
   * @throws ApiError
   */
  public static setEventSubscriptionRateLimit({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * Configure rate bucket: Application slug owning this subscription rate policy.
     */
    slug: string,
    /**
     * Configure rate bucket: Application subscription UUID for rate controls.
     */
    subscriptionId: string,
    requestBody: EventSubscriptionRateRequest,
  }): CancelablePromise<EventSubscriptionRateCommandReceipt> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/rate-limit',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Configure rate bucket: Invalid rate control input.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Configure rate bucket: Command UUID inputs changed or database clock moved behind the refill anchor.`,
        429: `Configure rate bucket: 128 unexpired receipts retained; known repeats remain available.`,
        504: `Configure rate bucket: Request deadline elapsed; after an uncertain mutation look up or replay the same command UUID.`,
      },
    });
  }
  /**
   * List rate history.
   * List rate history: Durable per-subscription pacing shared by admission workers. Mutations require deploy:write or admin and return an applied or rejected receipt; revision mismatch is a recorded rejection with no configuration change. Repeating an unexpired command UUID with identical inputs returns its original receipt. Reads require read scope. Existing pause, circuit, per-second drain caps, attempts and delivery deadlines remain in force. Capacity observations reserve nothing.
   * @returns EventSubscriptionRateCommands List rate history: Retained rate command evidence; inspect outcome for applied or rejected.
   * @throws ApiError
   */
  public static listEventSubscriptionRateCommands({
    slug,
    subscriptionId,
    after,
  }: {
    /**
     * List rate history: Application slug owning this subscription rate policy.
     */
    slug: string,
    /**
     * List rate history: Application subscription UUID for rate controls.
     */
    subscriptionId: string,
    /**
     * List rate history: Exclusive increasing history sequence; pages contain at most 50 retained receipts and do not freeze history.
     */
    after?: number,
  }): CancelablePromise<EventSubscriptionRateCommands> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/rate-limit/commands',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'after': after,
      },
      errors: {
        400: `List rate history: Invalid rate control input.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `List rate history: Request deadline elapsed; after an uncertain mutation look up or replay the same command UUID.`,
      },
    });
  }
  /**
   * Look up rate command.
   * Look up rate command: Durable per-subscription pacing shared by admission workers. Mutations require deploy:write or admin and return an applied or rejected receipt; revision mismatch is a recorded rejection with no configuration change. Repeating an unexpired command UUID with identical inputs returns its original receipt. Reads require read scope. Existing pause, circuit, per-second drain caps, attempts and delivery deadlines remain in force. Capacity observations reserve nothing.
   * @returns EventSubscriptionRateCommandReceipt Look up rate command: Retained rate command evidence; inspect outcome for applied or rejected.
   * @throws ApiError
   */
  public static getEventSubscriptionRateCommand({
    slug,
    subscriptionId,
    commandId,
  }: {
    /**
     * Look up rate command: Application slug owning this subscription rate policy.
     */
    slug: string,
    /**
     * Look up rate command: Application subscription UUID for rate controls.
     */
    subscriptionId: string,
    /**
     * Look up rate command: Nonzero UUID of a retained rate command.
     */
    commandId: string,
  }): CancelablePromise<EventSubscriptionRateCommandReceipt> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/rate-limit/commands/{commandID}',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'commandID': commandId,
      },
      errors: {
        400: `Look up rate command: Invalid rate control input.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Look up rate command: Request deadline elapsed; after an uncertain mutation look up or replay the same command UUID.`,
      },
    });
  }
  /**
   * Set a lower live-delivery ceiling for one event consumer.
   * Set a lower live-delivery ceiling for one event consumer. Requires `deploy:write` or `admin`. Controls survive deployment and subscription removal. Already admitted invocations continue. The explicit integer counts pending plus dispatching invocations. Zero restores the current plan limit; positive values cannot exceed it. Lowering does not evict existing work. Pause, rate and circuit state are preserved; plan downgrades clamp the effective ceiling. Routing and replay share this limit.
   * @returns EventSubscriptionDeliveryControl Set consumer delivery capacity: current control configuration and waiting recipient counts.
   * @throws ApiError
   */
  public static setEventSubscriptionCapacity({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * Application slug for this operation: set the live-delivery ceiling for one event consumer.
     */
    slug: string,
    /**
     * Application event subscription identifier for this operation: set the live-delivery ceiling for one event consumer.
     */
    subscriptionId: string,
    requestBody: EventSubscriptionCapacityRequest,
  }): CancelablePromise<EventSubscriptionDeliveryControl> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/capacity',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Set consumer delivery capacity: invalid subscription identifier, missing capacity or capacity outside the current plan ceiling.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Saved capacity or plan ceiling changed from expected values, or a retained command UUID was reused with different normalized input; no change is applied. Refresh preview before retrying. Value guards do not detect changes that return to the same values and do not reserve live capacity.`,
        429: `New command exceeds 128 retained receipts for this subscription. Known repeats remain available; expiry frees receipt slots. Controls without a command UUID remain available without idempotent evidence.`,
        504: `Set consumer delivery capacity: subscription control request timed out.`,
      },
    });
  }
  /**
   * List retained original capacity-command receipts.
   * Requires apps:read or admin and MFA. Read-only newest-first pagination by recorded_at and command UUID descending. Outcome and expiry filters apply before the page limit. Each page has a fresh ownership and receipt snapshot; cursors are bound to account, app, subscription and normalized outcome filter, but do not freeze history. New commands and expiry can affect later pages. Listing never applies commands, prunes evidence or extends retention. Empty history does not prove commands never ran. Recording order does not establish delivery or execution order. Responses use Cache-Control no-store.
   * @returns EventSubscriptionCapacityCommands Scoped page of original receipts and a continuation cursor when more matching retained receipts exist.
   * @throws ApiError
   */
  public static listEventSubscriptionCapacityCommands({
    slug,
    subscriptionId,
    outcome = 'any',
    limit = 25,
    after,
  }: {
    /**
     * Owned application slug.
     */
    slug: string,
    /**
     * Owned active subscription or retained delivery control identifier.
     */
    subscriptionId: string,
    /**
     * Original successful command outcome to include. Omission is equivalent to any.
     */
    outcome?: 'any' | 'applied' | 'unchanged',
    /**
     * Maximum receipts in this page. Omission uses 25. Explicit zero is invalid.
     */
    limit?: number,
    /**
     * Opaque next_after from the preceding page with the same owned scope and outcome filter. Page size may change. This cursor is a boundary rather than an authorization credential.
     */
    after?: string,
  }): CancelablePromise<EventSubscriptionCapacityCommands> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/capacity/commands',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'outcome': outcome,
        'limit': limit,
        'after': after,
      },
      errors: {
        400: `Invalid UUID, outcome filter, page limit or scoped cursor.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Read-only command history timed out.`,
      },
    });
  }
  /**
   * Read retained original capacity-command evidence.
   * Requires apps:read or admin and MFA. Returns the original successful command request values and outcome without applying a change, consuming receipt capacity, pruning records or extending retention. The fixed retention deadline is thirty days after success. A missing or expired receipt is unavailable evidence and does not prove the command never ran. The receipt does not describe current configuration and does not establish an ordering or execution guarantee.
   * @returns EventSubscriptionCapacityCommandReceipt Immutable original successful capacity-command receipt. Cache-Control is no-store.
   * @throws ApiError
   */
  public static getEventSubscriptionCapacityCommand({
    slug,
    subscriptionId,
    commandId,
  }: {
    /**
     * Owned application slug. Context: Read retained original capacity-command evidence.
     */
    slug: string,
    /**
     * Owned active subscription or retained delivery control identifier. Context: Read retained original capacity-command evidence.
     */
    subscriptionId: string,
    /**
     * Original nonzero hyphenated command UUID.
     */
    commandId: string,
  }): CancelablePromise<EventSubscriptionCapacityCommandReceipt> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/capacity/commands/{commandID}',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
        'commandID': commandId,
      },
      errors: {
        400: `Invalid subscription or command UUID.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `No owned target or unexpired command receipt is available. Receipt absence does not prove the command never ran.`,
        504: `Read-only capacity-command lookup timed out.`,
      },
    });
  }
  /**
   * Inspect app-wide recovery notification retry backlog.
   * Requires apps:read or admin and MFA. Reads original-generation retry outcomes across a bounded page of owned retained recovery jobs containing saved retry requests. Defaults to failed, pending, and inconclusive requests. Totals cover every request in scanned jobs before status filtering and are explicitly job-page scoped. Empty filtered pages may still have a next cursor. Each page is a fresh read-only snapshot; refresh from the beginning to see newer jobs or updated previously scanned jobs.
   * @returns EventRecoveryNotificationRetryBacklog Original-generation retry requests and explicitly page-scoped counts.
   * @throws ApiError
   */
  public static listEventRecoveryNotificationRetryBacklog({
    slug,
    status = 'failed,pending,inconclusive',
    pageSize = 5,
    cursor,
  }: {
    /**
     * Owned app slug whose retained recovery retry requests are inspected.
     */
    slug: string,
    /**
     * Distinct comma-separated request statuses selected as a union. Omission selects failed, pending, and inconclusive. Include succeeded explicitly to inspect successful requests.
     */
    status?: string,
    /**
     * Number of retained recovery jobs with saved retry requests inspected per page; this limits jobs rather than request rows.
     */
    pageSize?: number,
    /**
     * Opaque continuation bound to account, app, and canonical status selection. Continue even when a filtered page has no request rows.
     */
    cursor?: string,
  }): CancelablePromise<EventRecoveryNotificationRetryBacklog> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-recoveries/notification-retry-backlog',
      path: {
        'slug': slug,
      },
      query: {
        'status': status,
        'page_size': pageSize,
        'cursor': cursor,
      },
      errors: {
        400: `Invalid retry backlog query or cursor.`,
        401: `Authentication required for the app retry backlog.`,
        403: `Read scope or MFA required for the app retry backlog.`,
        404: `App retry backlog not found or not owned.`,
        429: `App retry backlog request rate exceeded.`,
        500: `App retry backlog could not be read.`,
        504: `App retry backlog evidence deadline expired.`,
      },
    });
  }
  /**
   * Inspect recovery admission and unresolved execution health.
   * Requires apps:read or admin and MFA. Current app-scoped admission observations and bounded unresolved execution health. Execution inspects the oldest 50 retained terminal-admission jobs missing exact saved replay results, samples up to three, and marks partial counts as lower bounds. Prolonged execution waits measure 15 minutes since admission completion; retention risk starts 24 hours before the nominal 30-day job retention boundary. Reads do not capture results or notifications. Stalled requires five minutes without admission progress and five minutes overdue for eligibility. Capacity/legacy claim retries and pacing defer eligibility. Paused jobs are reported separately and excluded from running-job alert counts. Expiry warnings cover pending items within one hour of expiry, including overdue expiry cleanup. No query parameters are accepted.
   * @returns EventRecoveryHealth Admission health and bounded unresolved execution observations.
   * @throws ApiError
   */
  public static getEventRecoveryHealth({
    slug,
  }: {
    /**
     * Application slug for this operation: inspect active recovery progress and expiry risk.
     */
    slug: string,
  }): CancelablePromise<EventRecoveryHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-recoveries/health',
      path: {
        'slug': slug,
      },
      errors: {
        400: `Invalid query parameters.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Recovery health request timed out.`,
      },
    });
  }
  /**
   * Preview failed application event recipients without requeueing.
   * Requires `apps:read` or `admin`.
   * @returns EventRecoveryPreview Recovery result.
   * @throws ApiError
   */
  public static previewEventRecovery({
    slug,
    requestBody,
  }: {
    /**
     * Application slug for this operation: preview failed application event recipients without requeueing.
     */
    slug: string,
    requestBody: EventRecoveryRequest,
  }): CancelablePromise<EventRecoveryPreview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-recoveries/preview',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid request or selection larger than 10000 recipients.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Recovery request timed out.`,
      },
    });
  }
  /**
   * Discover retained recovery jobs for an owned application.
   * Requires apps:read or admin and MFA. Newest creation time and ID first. Lists admission metadata only; execution outcomes are available through recovery status. Pages reflect live state and retention, not a frozen snapshot. Reuse the same app and filters with next_cursor.
   * @returns EventRecoveryJobs Retained recovery job metadata; execution summaries are omitted.
   * @throws ApiError
   */
  public static listEventRecoveries({
    slug,
    state,
    mode,
    subscriptionId,
    createdAfter,
    createdBefore,
    cursor,
    limit = 50,
  }: {
    /**
     * Application slug for this operation: discover retained recovery jobs for an owned application.
     */
    slug: string,
    /**
     * Current admission state.
     */
    state?: 'running' | 'paused' | 'completed' | 'cancelled',
    /**
     * Recovery mode; legacy omitted modes are routing.
     */
    mode?: 'routing' | 'execution' | 'workflow',
    /**
     * Matches the frozen selection filter or any captured item subscription.
     */
    subscriptionId?: string,
    /**
     * Exclusive creation lower bound.
     */
    createdAfter?: string,
    /**
     * Exclusive creation upper bound.
     */
    createdBefore?: string,
    /**
     * Opaque next_cursor from the previous page; bound to account, app, and filters.
     */
    cursor?: string,
    /**
     * Maximum jobs per page.
     */
    limit?: number,
  }): CancelablePromise<EventRecoveryJobs> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-recoveries',
      path: {
        'slug': slug,
      },
      query: {
        'state': state,
        'mode': mode,
        'subscription_id': subscriptionId,
        'created_after': createdAfter,
        'created_before': createdBefore,
        'cursor': cursor,
        'limit': limit,
      },
      errors: {
        400: `Invalid filters or cursor.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Recovery listing timed out.`,
      },
    });
  }
  /**
   * Create a durable recovery job for a frozen selection of routing failures.
   * Requires `deploy:write` or `admin`.
   * @returns EventRecoveryJob Create a durable recovery job for a frozen selection of routing failures: recovery result.
   * @throws ApiError
   */
  public static createEventRecovery({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Application slug for this operation: create a durable recovery job for a frozen selection of routing failures.
     */
    slug: string,
    requestBody: EventRecoveryRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<EventRecoveryJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-recoveries',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Create a durable recovery job for a frozen selection of routing failures: invalid request or selection larger than 10000 recipients.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Account already has three active recovery jobs.`,
        504: `Create a durable recovery job for a frozen selection of routing failures: recovery request timed out.`,
      },
    });
  }
  /**
   * Read bulk recovery progress.
   * Read bulk recovery progress. Requires `apps:read` or `admin`.
   * @returns EventRecoveryJob Read bulk recovery progress: recovery result.
   * @throws ApiError
   */
  public static getEventRecovery({
    jobId,
  }: {
    /**
     * Durable recovery job identifier for this operation: read bulk recovery progress.
     */
    jobId: string,
  }): CancelablePromise<EventRecoveryJob> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-recoveries/{jobID}',
      path: {
        'jobID': jobId,
      },
      errors: {
        400: `Read bulk recovery progress: invalid request or selection larger than 10000 recipients.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Read bulk recovery progress: recovery request timed out.`,
      },
    });
  }
  /**
   * Cancel retries that have not yet been queued.
   * Cancel retries that have not yet been queued. Requires `deploy:write` or `admin`.
   * @returns EventRecoveryJob Cancel retries that have not yet been queued: recovery result.
   * @throws ApiError
   */
  public static cancelEventRecovery({
    jobId,
    requestBody,
  }: {
    /**
     * Durable recovery job identifier for this operation: cancel retries that have not yet been queued.
     */
    jobId: string,
    /**
     * Optional operator reason; an empty body preserves existing clients.
     */
    requestBody?: EventRecoveryControlRequest,
  }): CancelablePromise<EventRecoveryJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/event-recoveries/{jobID}/cancel',
      path: {
        'jobID': jobId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Cancel retries that have not yet been queued: invalid request or selection larger than 10000 recipients.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Cancel retries that have not yet been queued: recovery request timed out.`,
      },
    });
  }
  /**
   * Pause further recovery admissions.
   * Requires `deploy:write` or `admin` and MFA. Controls preserve the frozen selection, spent window budget, existing waits, quota, and original expiry. Already queued deliveries continue. Completed, cancelled, or expired jobs return 409. Repeated desired-state controls on active jobs are idempotent.
   * @returns EventRecoveryJob Pause further recovery admissions: recovery result.
   * @throws ApiError
   */
  public static pauseEventRecovery({
    jobId,
    requestBody,
  }: {
    /**
     * Durable recovery job identifier for this operation: pause further recovery admissions.
     */
    jobId: string,
    /**
     * Request to pause further recovery admissions: optional operator reason; an empty body preserves existing clients.
     */
    requestBody?: EventRecoveryControlRequest,
  }): CancelablePromise<EventRecoveryJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/event-recoveries/{jobID}/pause',
      path: {
        'jobID': jobId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Pause further recovery admissions: invalid request or selection larger than 10000 recipients.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Recovery job is completed, cancelled, or expired.`,
        504: `Pause further recovery admissions: recovery request timed out.`,
      },
    });
  }
  /**
   * Resume the frozen recovery selection.
   * Resume the frozen recovery selection. Requires `deploy:write` or `admin` and MFA. Controls preserve the frozen selection, spent window budget, existing waits, quota, and original expiry. Already queued deliveries continue. Completed, cancelled, or expired jobs return 409. Repeated desired-state controls on active jobs are idempotent.
   * @returns EventRecoveryJob Resume the frozen recovery selection: recovery result.
   * @throws ApiError
   */
  public static resumeEventRecovery({
    jobId,
    requestBody,
  }: {
    /**
     * Durable recovery job identifier for this operation: resume the frozen recovery selection.
     */
    jobId: string,
    /**
     * Request to resume the frozen recovery selection: optional operator reason; an empty body preserves existing clients.
     */
    requestBody?: EventRecoveryControlRequest,
  }): CancelablePromise<EventRecoveryJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/event-recoveries/{jobID}/resume',
      path: {
        'jobID': jobId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Resume the frozen recovery selection: invalid request or selection larger than 10000 recipients.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Resume the frozen recovery selection: recovery job is completed, cancelled, or expired.`,
        504: `Resume the frozen recovery selection: recovery request timed out.`,
      },
    });
  }
  /**
   * Change the current recovery admission rate.
   * Change the current recovery admission rate. Requires `deploy:write` or `admin` and MFA. Controls preserve the frozen selection, spent window budget, existing waits, quota, and original expiry. Already queued deliveries continue. Completed, cancelled, or expired jobs return 409. Repeated desired-state controls on active jobs are idempotent.
   * @returns EventRecoveryJob Change the current recovery admission rate: recovery result.
   * @throws ApiError
   */
  public static setEventRecoveryRate({
    jobId,
    requestBody,
  }: {
    /**
     * Durable recovery job identifier for this operation: change the current recovery admission rate.
     */
    jobId: string,
    requestBody: EventRecoveryRateRequest,
  }): CancelablePromise<EventRecoveryJob> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/event-recoveries/{jobID}/rate',
      path: {
        'jobID': jobId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Change the current recovery admission rate: invalid request or selection larger than 10000 recipients.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Change the current recovery admission rate: recovery job is completed, cancelled, or expired.`,
        504: `Change the current recovery admission rate: recovery request timed out.`,
      },
    });
  }
  /**
   * Inspect the recovery policy
   * Returns the current app policy and scheduler evaluation status. An unconfigured app has a disabled version-zero policy.
   * @returns WorkflowRecoveryPolicyResponse Result of getWorkflowRecoveryPolicy.
   * @throws ApiError
   */
  public static getWorkflowRecoveryPolicy({
    slug,
  }: {
    /**
     * App slug for getWorkflowRecoveryPolicy.
     */
    slug: string,
  }): CancelablePromise<WorkflowRecoveryPolicyResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Configure a recovery policy
   * Updates an app policy with optimistic version checks. Preview prepares plans from retained native evidence. Opt-in automatic_retry authorizes bounded retries of saved failures; automatic_initial authorizes bounded generation-zero recovery with explicit workflow and failure-status filters. Enabling automatic_initial requires the workflow runtime. Requires deploy write permission and MFA; cancellation remains manual.
   * @returns WorkflowRecoveryPolicyResponse Result of setWorkflowRecoveryPolicy.
   * @throws ApiError
   */
  public static setWorkflowRecoveryPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App slug for setWorkflowRecoveryPolicy.
     */
    slug: string,
    /**
     * Recovery policy settings and explicit automatic retry limits with the last observed version.
     */
    requestBody: SetWorkflowRecoveryPolicyRequest,
  }): CancelablePromise<WorkflowRecoveryPolicyResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/recovery-policy',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Evaluate proposed recovery settings against retained historical candidates
   * Read-only consistent snapshot comparing current and proposed settings against up to 100 oldest retained reviews and 500 decisions in a maximum 30-day window. Requires the observed current policy version. Individual historical failure status is not captured; historical policy filters establish a possible status set and ambiguous narrower filters remain unknown. Preparation budgets, cooldown and deduplication are replayed conditionally from empty window-start state; unknown candidates are not charged. Actual recorded applications establish lower-bound execution pressure, never admission availability or counterfactual retry results. Application warmup uses the longer policy cooldown or one hour, with up to 10000 records and explicit truncation. Never reads current run state as historical proof or writes policy, reviews, jobs, audit events or notifications.
   * @returns WorkflowRecoveryPolicyEvaluation Evidence-limited conditional comparison with explicit coverage notes.
   * @throws ApiError
   */
  public static evaluateWorkflowRecoveryPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App whose retained review evidence is used for policy comparison.
     */
    slug: string,
    requestBody: WorkflowRecoveryPolicyEvaluationRequest,
  }): CancelablePromise<WorkflowRecoveryPolicyEvaluation> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/evaluate',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Current recovery policy version differs from expected_version.`,
      },
    });
  }
  /**
   * Preview a recovery policy replacement
   * Read-only impact of replacing the observed policy version. Every replacement suppresses old pending automatic intents and preserves unresolved applied children and spent charges. Inspects up to five newest matching recovery jobs and returns up to 25 candidate decisions. Retained preparation targets continue to prevent duplicate plans. Requires read access and MFA. No plans, controls, retries or notifications are written. Advisory evidence can change before a separately authorized version-fenced update.
   * @returns WorkflowRecoveryPolicyChangePreview Read-only proposed-policy snapshot with parent-job and bounded initial-failure candidates. No reviews, jobs or notifications are created.
   * @throws ApiError
   */
  public static previewWorkflowRecoveryPolicyChange({
    slug,
    requestBody,
  }: {
    /**
     * App slug for previewWorkflowRecoveryPolicyChange.
     */
    slug: string,
    /**
     * Proposed policy using the last observed configuration version; version zero means no existing policy.
     */
    requestBody: SetWorkflowRecoveryPolicyRequest,
  }): CancelablePromise<WorkflowRecoveryPolicyChangePreview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/change-preview',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        504: `Policy change preview inspection timed out.`,
      },
    });
  }
  /**
   * Pause or resume automatic recovery
   * Controls enabled automatic recovery without replacing its policy or discarding prepared retries. Requires deploy write and MFA. The observed version, update timestamp and pause timestamp fence concurrent controls and failure pauses. Resume clears the consecutive failure streak; committed children continue while paused.
   * @returns WorkflowRecoveryPolicyResponse Result of controlWorkflowRecoveryPolicy.
   * @throws ApiError
   */
  public static controlWorkflowRecoveryPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App slug for controlWorkflowRecoveryPolicy.
     */
    slug: string,
    /**
     * Observed recovery control state, operation and operator reason.
     */
    requestBody: ControlWorkflowRecoveryPolicyRequest,
  }): CancelablePromise<WorkflowRecoveryPolicyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/control',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Prepare a durable first-recovery review
   * Policy-version-fenced initial recovery. Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed policy authority block submission and pending admission. Submission is explicit; no automatic first recovery is enabled.
   * @returns WorkflowInitialRecoveryReview Initial recovery review
   * @throws ApiError
   */
  public static prepareWorkflowInitialRecoveryReview({
    slug,
    failureId,
    requestBody,
  }: {
    /**
     * Application slug.
     */
    slug: string,
    /**
     * Captured initial workflow failure identifier.
     */
    failureId: string,
    requestBody: WorkflowInitialRecoveryReviewRequest,
  }): CancelablePromise<WorkflowInitialRecoveryReview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflow-failures/{failureID}/recovery-reviews',
      path: {
        'slug': slug,
        'failureID': failureId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Diagnose pending retry parent evidence
   * Read-only diagnosis of the exact parent generation and item required by the frozen pending retry plan. Missing admission history and superseded runs cannot be repaired. Requires read scope and MFA.
   * @returns WorkflowRetryEvidenceDiagnosis Consistent retained evidence diagnosis.
   * @returns Problem Error.
   * @throws ApiError
   */
  public static diagnoseWorkflowRetryParentEvidence({
    slug,
    reviewId,
    actionId,
  }: {
    /**
     * Application slug used to diagnose pending retry parent evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier.
     */
    reviewId: string,
    /**
     * Frozen retry action identifier.
     */
    actionId: string,
  }): CancelablePromise<WorkflowRetryEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence/parent',
      path: {
        'slug': slug,
        'reviewID': reviewId,
        'actionID': actionId,
      },
    });
  }
  /**
   * Save verified pending retry parent terminal evidence
   * Requires deploy write and MFA. Re-inspects the exact parent generation, item and admission lineage under lock. Repair applies only to pending actions and does not authorize child submission. Parent and child repair receipts are separate. Saves a missing result once and records the operator reason. Never overwrites retained evidence or resumes a paused policy. Replaying the same request ID and reason returns its receipt; changes conflict. The worker observes repaired proof on its next eligible poll.
   * @returns WorkflowRetryEvidenceDiagnosis Retained repair receipt and evidence diagnosis.
   * @returns Problem Save verified pending retry parent terminal evidence failed.
   * @throws ApiError
   */
  public static repairWorkflowRetryParentEvidence({
    slug,
    reviewId,
    actionId,
    requestBody,
  }: {
    /**
     * Application slug used to save verified pending retry parent terminal evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to save verified pending retry parent terminal evidence.
     */
    reviewId: string,
    /**
     * Frozen retry action identifier used to save verified pending retry parent terminal evidence.
     */
    actionId: string,
    requestBody: WorkflowInitialEvidenceRepairRequest,
  }): CancelablePromise<WorkflowRetryEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence/parent/repair',
      path: {
        'slug': slug,
        'reviewID': reviewId,
        'actionID': actionId,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Diagnose retained automatic retry evidence
   * Read-only exact retry-generation diagnosis with frozen parent and child lineage checks. Missing admission history and superseded runs cannot be repaired. Requires read scope and MFA.
   * @returns WorkflowRetryEvidenceDiagnosis Diagnose retained automatic retry evidence. Consistent retained evidence diagnosis.
   * @returns Problem Diagnose retained automatic retry evidence failed.
   * @throws ApiError
   */
  public static diagnoseWorkflowRetryEvidence({
    slug,
    reviewId,
    actionId,
  }: {
    /**
     * Application slug used to diagnose retained automatic retry evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to diagnose retained automatic retry evidence.
     */
    reviewId: string,
    /**
     * Frozen retry action identifier used to diagnose retained automatic retry evidence.
     */
    actionId: string,
  }): CancelablePromise<WorkflowRetryEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence',
      path: {
        'slug': slug,
        'reviewID': reviewId,
        'actionID': actionId,
      },
    });
  }
  /**
   * Save verified automatic retry terminal evidence
   * Requires deploy write and MFA. Re-inspects the exact generation and admission lineage under lock. Saves a missing result once and records the operator reason. Never overwrites retained evidence or resumes a paused policy. Replaying the same request ID and reason returns its receipt; changes conflict. The worker observes repaired proof on its next eligible poll.
   * @returns WorkflowRetryEvidenceDiagnosis Save verified automatic retry terminal evidence. Retained repair receipt and evidence diagnosis.
   * @returns Problem Save verified automatic retry terminal evidence failed.
   * @throws ApiError
   */
  public static repairWorkflowRetryEvidence({
    slug,
    reviewId,
    actionId,
    requestBody,
  }: {
    /**
     * Application slug used to save verified automatic retry terminal evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to save verified automatic retry terminal evidence.
     */
    reviewId: string,
    /**
     * Frozen retry action identifier used to save verified automatic retry terminal evidence.
     */
    actionId: string,
    requestBody: WorkflowInitialEvidenceRepairRequest,
  }): CancelablePromise<WorkflowRetryEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/evidence/repair',
      path: {
        'slug': slug,
        'reviewID': reviewId,
        'actionID': actionId,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Diagnose retained initial recovery evidence
   * Read-only exact-generation diagnosis. Missing admission history and superseded runs cannot be repaired. Requires read scope and MFA.
   * @returns WorkflowInitialEvidenceDiagnosis Diagnose retained initial recovery evidence. Consistent retained evidence diagnosis.
   * @returns Problem Diagnose retained initial recovery evidence failed.
   * @throws ApiError
   */
  public static diagnoseWorkflowInitialEvidence({
    slug,
    reviewId,
  }: {
    /**
     * Application slug used to diagnose retained initial recovery evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to diagnose retained initial recovery evidence.
     */
    reviewId: string,
  }): CancelablePromise<WorkflowInitialEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/initial-recovery-reviews/{reviewID}/evidence',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
    });
  }
  /**
   * Save verified initial recovery terminal evidence
   * Save verified initial recovery terminal evidence. Requires deploy write and MFA. Re-inspects the exact generation and admission lineage under lock. Saves a missing result once and records the operator reason. Never overwrites retained evidence or resumes a paused policy. Replaying the same request ID and reason returns its receipt; changes conflict. The worker observes repaired proof on its next eligible poll.
   * @returns WorkflowInitialEvidenceDiagnosis Save verified initial recovery terminal evidence. Retained repair receipt and evidence diagnosis.
   * @returns Problem Save verified initial recovery terminal evidence failed.
   * @throws ApiError
   */
  public static repairWorkflowInitialEvidence({
    slug,
    reviewId,
    requestBody,
  }: {
    /**
     * Application slug used to save verified initial recovery terminal evidence.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to save verified initial recovery terminal evidence.
     */
    reviewId: string,
    requestBody: WorkflowInitialEvidenceRepairRequest,
  }): CancelablePromise<WorkflowInitialEvidenceDiagnosis | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/initial-recovery-reviews/{reviewID}/evidence/repair',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Inspect a durable first-recovery review
   * Inspect a durable first-recovery review. Policy-version-fenced initial recovery. Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed policy authority block submission and pending admission. Submission is explicit; no automatic first recovery is enabled.
   * @returns WorkflowInitialRecoveryReview Inspect a durable first-recovery review result.
   * @throws ApiError
   */
  public static getWorkflowInitialRecoveryReview({
    slug,
    reviewId,
  }: {
    /**
     * Application slug for inspect a durable first-recovery review.
     */
    slug: string,
    /**
     * Durable recovery review identifier for inspect a durable first-recovery review.
     */
    reviewId: string,
  }): CancelablePromise<WorkflowInitialRecoveryReview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/initial-recovery-reviews/{reviewID}',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Submit or cancel a durable first-recovery review
   * Submit or cancel a durable first-recovery review. Policy-version-fenced initial recovery. Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed policy authority block submission and pending admission. Submission is explicit; no automatic first recovery is enabled.
   * @returns WorkflowInitialRecoveryReview Submit or cancel a durable first-recovery review result.
   * @throws ApiError
   */
  public static decideWorkflowInitialRecoveryReview({
    slug,
    reviewId,
    requestBody,
  }: {
    /**
     * Application slug used to submit or cancel a durable first-recovery review.
     */
    slug: string,
    /**
     * Durable recovery review identifier used to submit or cancel a durable first-recovery review.
     */
    reviewId: string,
    requestBody: WorkflowInitialRecoveryReviewDecisionRequest,
  }): CancelablePromise<WorkflowInitialRecoveryReview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/initial-recovery-reviews/{reviewID}/decision',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * List retained first-recovery reviews
   * List retained first-recovery reviews. Policy-version-fenced initial recovery. Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed policy authority block submission and pending admission. Submission is explicit; no automatic first recovery is enabled.
   * @returns WorkflowInitialRecoveryReviews List retained first-recovery reviews result.
   * @throws ApiError
   */
  public static listWorkflowInitialRecoveryReviews({
    slug,
    after,
    limit = 100,
  }: {
    /**
     * Application slug used to list retained first-recovery reviews.
     */
    slug: string,
    /**
     * Opaque cursor from the previous page.
     */
    after?: string,
    /**
     * Maximum number of results to return.
     */
    limit?: number,
  }): CancelablePromise<WorkflowInitialRecoveryReviews> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/initial-recovery-reviews',
      path: {
        'slug': slug,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * List captured initial workflow failures
   * Read-only, policy-independent generation-zero failure inbox. Capture is prospective and partial, retained for 30 days with at most 10000 entries per app. Newest-first cursors bind the account, app, workflow filter and observation ceiling. Retention may remove entries between pages. Evidence alone does not prove that a run can safely resume. No run inputs, outputs or error text are returned.
   * @returns WorkflowInitialFailures Retained initial failures.
   * @throws ApiError
   */
  public static listWorkflowInitialFailures({
    slug,
    workflow,
    after,
    limit = 25,
  }: {
    /**
     * Application slug used to list captured initial workflow failures.
     */
    slug: string,
    /**
     * Workflow name to filter captured failures.
     */
    workflow?: string,
    /**
     * Opaque cursor from the previous page used to list captured initial workflow failures.
     */
    after?: string,
    /**
     * Maximum number of results to return used to list captured initial workflow failures.
     */
    limit?: number,
  }): CancelablePromise<WorkflowInitialFailures> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-failures',
      path: {
        'slug': slug,
      },
      query: {
        'workflow': workflow,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Review first recovery eligibility for a captured failure
   * Read-only consistent live inspection of retained generation-zero evidence, current generation, safe resume diagnostics and operation ownership. Returns a generation-fenced target only when eligible. Cached later eligibility and policy existence do not authorize recovery. This does not prepare a policy review or admit a run. Operators may submit the exact reviewed target through event recovery creation with a stable request_id; creation and worker admission recheck evidence retention and generation, and resume rechecks safety and capacity. No automatic first recovery is enabled.
   * @returns WorkflowInitialRecoveryPreview Live first recovery diagnostic.
   * @throws ApiError
   */
  public static previewWorkflowInitialRecovery({
    slug,
    failureId,
  }: {
    /**
     * Application slug for review first recovery eligibility for a captured failure.
     */
    slug: string,
    /**
     * Captured initial workflow failure identifier for review first recovery eligibility for a captured failure.
     */
    failureId: string,
  }): CancelablePromise<WorkflowInitialRecoveryPreview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflow-failures/{failureID}/recovery-preview',
      path: {
        'slug': slug,
        'failureID': failureId,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Inspect retained recovery automation history
   * Read-only timeline of retained review decisions, controls, retry events, durable policy changes, worker reason transitions and health incident events, alongside latest observations. Durable events are captured prospectively with their state mutations, retained for 30 days and capped at 10000 per app. Results are ordered newest first with stable identifier tie breaks. Cursors fix the time window and durable event sequence ceiling; later appends are excluded. Retention can remove events, and latest observations can change across pages. Workflow, run and policy filters omit records without those associations. No execution inputs or outputs are returned.
   * @returns WorkflowRecoveryHistory Retained history page.
   * @throws ApiError
   */
  public static getWorkflowRecoveryHistory({
    slug,
    workflow,
    runId,
    policyVersion,
    createdAfter,
    createdBefore,
    after,
    limit = 25,
  }: {
    /**
     * App slug.
     */
    slug: string,
    /**
     * Workflow name filter.
     */
    workflow?: string,
    /**
     * Workflow run filter.
     */
    runId?: string,
    /**
     * Policy version filter; zero includes all versions.
     */
    policyVersion?: number,
    /**
     * Inclusive start; defaults to seven days before the end.
     */
    createdAfter?: string,
    /**
     * Inclusive end; defaults to the first page observation time. Maximum window is 30 days.
     */
    createdBefore?: string,
    /**
     * Opaque next_after cursor; preserve the original filters.
     */
    after?: string,
    /**
     * Page size; zero uses the default.
     */
    limit?: number,
  }): CancelablePromise<WorkflowRecoveryHistory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/history',
      path: {
        'slug': slug,
      },
      query: {
        'workflow': workflow,
        'run_id': runId,
        'policy_version': policyVersion,
        'created_after': createdAfter,
        'created_before': createdBefore,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Report observed recovery automation effectiveness
   * Read-only consistent snapshot summarizing at most 10000 newest retained history entries. Deduplicates durable and retained retry records by action. Attempts and results are selected independently by event time; missing results do not prove children are running. Durations measure application to result recording including worker polling delay. Groups use workflow and policy version. Blockers count distinct actions per observed reason. Recording is prospective, without backfill, and durable history retains 30 days / 10000 events per app. Coverage notes and truncation always accompany counts; no success percentage or causal effectiveness claim is inferred.
   * @returns WorkflowRecoveryReport Observed effectiveness report.
   * @throws ApiError
   */
  public static getWorkflowRecoveryReport({
    slug,
    workflow,
    policyVersion,
    createdAfter,
    createdBefore,
  }: {
    /**
     * App whose observed recovery behavior is summarized.
     */
    slug: string,
    /**
     * Restrict report evidence to this workflow name.
     */
    workflow?: string,
    /**
     * Restrict report evidence to this policy version; zero includes every version.
     */
    policyVersion?: number,
    /**
     * Inclusive report start; omitted values use seven days before the report end.
     */
    createdAfter?: string,
    /**
     * Inclusive end; defaults to the snapshot observation time. Maximum window is 30 days.
     */
    createdBefore?: string,
  }): CancelablePromise<WorkflowRecoveryReport> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/report',
      path: {
        'slug': slug,
      },
      query: {
        'workflow': workflow,
        'policy_version': policyVersion,
        'created_after': createdAfter,
        'created_before': createdBefore,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Inspect recovery automation health
   * Read-only app-scoped recovery policy health from a consistent bounded queue snapshot. Expected cooldown and exhausted budgets are blocked rather than stalled. Stalled means worker evaluation is overdue, not that a running child exceeded its execution deadline. This is advisory and never releases slots or authorizes execution.
   * @returns WorkflowRecoveryPolicyHealth Observed recovery automation health.
   * @throws ApiError
   */
  public static getWorkflowRecoveryPolicyHealth({
    slug,
    stallAfterSeconds = 300,
  }: {
    /**
     * App slug for recovery policy health.
     */
    slug: string,
    /**
     * Worker evaluation age threshold; request-local and not persisted.
     */
    stallAfterSeconds?: number,
  }): CancelablePromise<WorkflowRecoveryPolicyHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/health',
      path: {
        'slug': slug,
      },
      query: {
        'stall_after_seconds': stallAfterSeconds,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Inspect queued automatic retries
   * Returns a consistent app-scoped snapshot of current pending intents, unresolved applied children across policy versions, and execution charges across all versions. Includes at most 100 entries in review order. Reasons reflect the last worker evaluation and are informational; unknown evidence never grants execution authority.
   * @returns WorkflowRecoveryAutomaticQueue Result of getWorkflowRecoveryAutomaticQueue.
   * @throws ApiError
   */
  public static getWorkflowRecoveryAutomaticQueue({
    slug,
  }: {
    /**
     * App slug for getWorkflowRecoveryAutomaticQueue.
     */
    slug: string,
  }): CancelablePromise<WorkflowRecoveryAutomaticQueue> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/queue',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * List retained recovery policy decisions
   * Lists immutable evaluations and prepared response plans in identifier order. Evidence is retained with the parent recovery job.
   * @returns WorkflowRecoveryPolicyReviews Result of listWorkflowRecoveryPolicyReviews.
   * @throws ApiError
   */
  public static listWorkflowRecoveryPolicyReviews({
    slug,
    after,
    limit = 100,
  }: {
    /**
     * App slug for listWorkflowRecoveryPolicyReviews.
     */
    slug: string,
    /**
     * Exclusive review identifier cursor from the previous policy review page.
     */
    after?: string,
    /**
     * Maximum retained policy reviews returned in this page.
     */
    limit?: number,
  }): CancelablePromise<WorkflowRecoveryPolicyReviews> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/reviews',
      path: {
        'slug': slug,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Inspect one retained policy review
   * Reads a frozen policy version and its target decisions. A prepared plan can be exported and explicitly applied using generation-fenced controls.
   * @returns WorkflowRecoveryPolicyReview Result of getWorkflowRecoveryPolicyReview.
   * @throws ApiError
   */
  public static getWorkflowRecoveryPolicyReview({
    slug,
    reviewId,
  }: {
    /**
     * App slug for getWorkflowRecoveryPolicyReview.
     */
    slug: string,
    /**
     * Retained recovery policy review to inspect.
     */
    reviewId: string,
  }): CancelablePromise<WorkflowRecoveryPolicyReview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Approve or dismiss a policy plan
   * Records one immutable actor-attributed decision. An identical retry returns the original decision; a different decision or reason conflicts. Approval never applies actions and can be recorded for stale plans. Only reviews with prepared plans can be decided.
   * @returns WorkflowRecoveryPolicyReview Review operation result.
   * @throws ApiError
   */
  public static decideWorkflowRecoveryPolicyReview({
    slug,
    reviewId,
    requestBody,
  }: {
    /**
     * App owning the policy review.
     */
    slug: string,
    /**
     * Retained policy review identifier.
     */
    reviewId: string,
    requestBody: WorkflowRecoveryReviewDecisionRequest,
  }): CancelablePromise<WorkflowRecoveryPolicyReview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/decision',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Suppress one pending automatic retry
   * Suppresses a retained pending intent under its observed policy version. Records a trusted operator and reason. Requires deploy write and MFA. Applied children cannot be suppressed; no child is cancelled and no charge is refunded. Identical reason retries return the retained audit receipt while the policy version remains current. Manual recovery remains available.
   * @returns WorkflowRecoveryAutomaticAction Retained result of suppressWorkflowRecoveryAutomaticAction.
   * @throws ApiError
   */
  public static suppressWorkflowRecoveryAutomaticAction({
    slug,
    reviewId,
    actionId,
    requestBody,
  }: {
    /**
     * App slug for suppressWorkflowRecoveryAutomaticAction.
     */
    slug: string,
    /**
     * Retained review containing the automatic retry to suppress.
     */
    reviewId: string,
    /**
     * Stable pending automatic action identifier for suppression.
     */
    actionId: string,
    /**
     * Observed automatic policy version and operator suppression reason.
     */
    requestBody: SuppressWorkflowRecoveryAutomaticActionRequest,
  }): CancelablePromise<WorkflowRecoveryAutomaticAction> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/actions/{actionID}/suppress',
      path: {
        'slug': slug,
        'reviewID': reviewId,
        'actionID': actionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Inspect live policy plan freshness
   * Checks retained action receipts before live generation eligibility. Reports ready, stale, unknown, applied, partially_applied or no_plan. Applied means a cancellation succeeded or a retry child was created; execution outcomes require the response-status command. Advisory checks never mutate or refresh plans.
   * @returns WorkflowRecoveryReviewStatus Current advisory freshness and durable application evidence.
   * @throws ApiError
   */
  public static getWorkflowRecoveryReviewStatus({
    slug,
    reviewId,
  }: {
    /**
     * App whose prepared response plan freshness is inspected.
     */
    slug: string,
    /**
     * Policy review containing the saved targets to inspect.
     */
    reviewId: string,
  }): CancelablePromise<WorkflowRecoveryReviewStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/recovery-policy/reviews/{reviewID}/status',
      path: {
        'slug': slug,
        'reviewID': reviewId,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read a retained workflow recovery action receipt
   * Reads an immutable cancellation decision in the authenticated account. Missing or expired action receipts return 404. This operation never submits cancellation.
   * @returns WorkflowRecoveryActionReceipt Retained cancellation action and its original outcome.
   * @throws ApiError
   */
  public static getWorkflowRecoveryActionReceipt({
    jobId,
    requestId,
  }: {
    /**
     * Owned recovery job whose cancellation receipt is being reconciled.
     */
    jobId: string,
    /**
     * Stable cancellation action identifier from the saved response plan.
     */
    requestId: string,
  }): CancelablePromise<WorkflowRecoveryActionReceipt> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-recoveries/{jobID}/workflow-action-receipts/{requestID}',
      path: {
        'jobID': jobId,
        'requestID': requestId,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Find a retained recovery job by its stable request identifier
   * Looks up the original recovery job in this app and account after an interrupted creation request. Missing or expired jobs return 404. This operation never creates work.
   * @returns EventRecoveryJob Original retained recovery job and its current progress.
   * @throws ApiError
   */
  public static getEventRecoveryByRequest({
    slug,
    requestId,
  }: {
    /**
     * App containing the original recovery creation request.
     */
    slug: string,
    /**
     * Original recovery creation request identifier to locate in this app.
     */
    requestId: string,
  }): CancelablePromise<EventRecoveryJob> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-recoveries/by-request/{requestID}',
      path: {
        'slug': slug,
        'requestID': requestId,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Preview cancellation of an admitted workflow generation
   * Cancellation preview for native workflow recovery only. Rechecks the frozen item and generation under the run lock. Changed or unproven generations are refused. Cancellation is cooperative and cannot undo external side effects. Successful cancellation and its actor and reason are recorded atomically in recovery history.
   * @returns WorkflowRecoveryCancellationResponse Cancellation preview outcome. Noneligible outcomes do not change execution or history.
   * @throws ApiError
   */
  public static previewWorkflowRecoveryCancellation({
    jobId,
    requestBody,
  }: {
    /**
     * Owned retained recovery job ID for workflow-cancellation-preview.
     */
    jobId: string,
    /**
     * Reviewed item and generation with the operator reason for workflow-cancellation-preview.
     */
    requestBody: WorkflowRecoveryCancellationRequest,
  }): CancelablePromise<WorkflowRecoveryCancellationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/event-recoveries/{jobID}/workflow-cancellation-preview',
      path: {
        'jobID': jobId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Cancel one reviewed admitted workflow generation
   * Cancellation commit for native workflow recovery only. Rechecks the frozen item and generation under the run lock. Changed or unproven generations are refused. Cancellation is cooperative and cannot undo external side effects. Successful cancellation and its actor and reason are recorded atomically in recovery history.
   * @returns WorkflowRecoveryCancellationResponse Cancellation commit outcome. Noneligible outcomes do not change execution or history.
   * @throws ApiError
   */
  public static cancelWorkflowRecoveryExecution({
    jobId,
    requestBody,
  }: {
    /**
     * Owned retained recovery job ID for workflow-cancel.
     */
    jobId: string,
    /**
     * Reviewed item and generation with the operator reason for workflow-cancel.
     */
    requestBody: WorkflowRecoveryCancellationRequest,
  }): CancelablePromise<WorkflowRecoveryCancellationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/event-recoveries/{jobID}/workflow-cancel',
      path: {
        'jobID': jobId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Assess a frozen recovery selection without admitting work.
   * Requires apps:read or admin and MFA. Read-only observations of all pending items, with up to 100 sample positions. Current per-item eligibility and capacity are not reservations. Conditions can change before admission. The optimistic rate-window estimate includes all pending admissions/skips and spent permits, assumes immediate resume, and excludes future contention, worker delays and handler execution. With retry=true, review every saved item of a terminal native workflow recovery using one consistent snapshot. Only saved failures matching the retained run generation can be retried. Capacity is temporary; changed, missing, unsafe, and resume-limit targets cannot be selected. This view never creates a job or resumes a run. Timing fields describe the parent job, not a prospective child.
   * @returns EventRecoveryPreflight Current eligibility observations and optimistic timing estimate.
   * @throws ApiError
   */
  public static getEventRecoveryPreflight({
    jobId,
    retry,
  }: {
    /**
     * Durable recovery job identifier for this operation: assess a frozen recovery selection without admitting work.
     */
    jobId: string,
    /**
     * Review saved native workflow outcomes for an explicit child retry instead of pending parent admissions. Only true is accepted; omit for ordinary preflight.
     */
    retry?: boolean,
  }): CancelablePromise<EventRecoveryPreflight> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-recoveries/{jobID}/preflight',
      path: {
        'jobID': jobId,
      },
      query: {
        'retry': retry,
      },
      errors: {
        400: `Invalid job ID or query parameters.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Retry view requires a terminal native workflow recovery parent.`,
        504: `Recovery preflight timed out.`,
      },
    });
  }
  /**
   * Inspect recovery notification capture and receiver acknowledgements.
   * Requires apps:read or admin and MFA. Read-only account-scoped report for a retained recovery job. Admission and execution notifications are separate. Preserved receiver snapshots and retained outbox rows can establish complete selection; retained deliveries alone cannot. Reports up to 100 receivers per notification and marks incomplete counts. Missing/pruned evidence is unknown, never proof of acknowledgement. Existing retry paths require their normal write scope and MFA. No query parameters are accepted.
   * @returns EventRecoveryNotifications Current metadata-only notification and delivery report.
   * @throws ApiError
   */
  public static getEventRecoveryNotifications({
    jobId,
  }: {
    /**
     * Retained recovery job ID.
     */
    jobId: string,
  }): CancelablePromise<EventRecoveryNotifications> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-recoveries/{jobID}/notifications',
      path: {
        'jobID': jobId,
      },
      errors: {
        400: `Invalid recovery notification job ID or unsupported query parameters.`,
        401: `Authentication is required to inspect recovery notification delivery.`,
        403: `Read scope or MFA required.`,
        404: `Recovery job not found or not owned.`,
        429: `Request rate limited.`,
        500: `Internal notification reporting failure or invalid capture metadata.`,
      },
    });
  }
  /**
   * Preview selective recovery notification retries.
   * Requires apps:read or admin and MFA. Read-only preview of admission and execution receivers. Only retained dead deliveries with an enabled owned receiver and an eligible current plan are eligible. Incomplete evidence remains explicit; preview reserves nothing and accepts no query parameters.
   * @returns EventRecoveryNotificationRetryPreview Notification retry-preview decision for the selected job.
   * @throws ApiError
   */
  public static previewEventRecoveryNotificationRetry({
    jobId,
  }: {
    /**
     * Retained recovery job identifier for retry-preview.
     */
    jobId: string,
  }): CancelablePromise<EventRecoveryNotificationRetryPreview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-recoveries/{jobID}/notifications/retry-preview',
      path: {
        'jobID': jobId,
      },
      errors: {
        400: `Invalid notification retry-preview input or query parameters.`,
        401: `Authentication required for notification retry-preview.`,
        403: `Required scope or MFA missing for notification retry-preview.`,
        404: `Retained job unavailable for notification retry-preview.`,
        429: `Notification retry-preview request rate limited.`,
        500: `Notification retry-preview storage failure.`,
        504: `Notification retry-preview request exceeded its time budget.`,
      },
    });
  }
  /**
   * Retry selected recovery notification deliveries.
   * Requires deploy:write or admin and MFA. Explicit targets require notification kind, webhook and delivery identity, and expected replay generation. Revalidates each receiver and records queued or skipped decisions atomically with resets. A stable request_id returns the saved decision regardless of later delivery state; reuse with different targets conflicts. At most 100 targets and 100 saved decisions per retained job. Decisions expire with job pruning. Delivery IDs and source event IDs remain stable; queued means pending delivery, not acknowledgement. No query parameters are accepted.
   * @returns EventRecoveryNotificationRetryResponse Notification retry decision for the selected job.
   * @throws ApiError
   */
  public static retryEventRecoveryNotifications({
    jobId,
    requestBody,
  }: {
    /**
     * Retained recovery job identifier for retry.
     */
    jobId: string,
    requestBody: EventRecoveryNotificationRetryRequest,
  }): CancelablePromise<EventRecoveryNotificationRetryResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/event-recoveries/{jobID}/notifications/retry',
      path: {
        'jobID': jobId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid notification retry input or query parameters.`,
        401: `Authentication required for notification retry.`,
        403: `Required scope or MFA missing for notification retry.`,
        404: `Retained job unavailable for notification retry.`,
        409: `Notification retry request ID conflicts or saved decision limit reached.`,
        429: `Notification retry request rate limited.`,
        500: `Notification retry storage failure.`,
        504: `Notification retry request exceeded its time budget.`,
      },
    });
  }
  /**
   * List saved recovery notification retry decisions.
   * Requires apps:read or admin and MFA. Lists at most 100 immutable request summaries in decision time order for the retained owned recovery job. The optional status filter selects a comma-separated union of original-generation request statuses. Totals cover all retained requests before filtering; matched_count counts returned rows. Other query parameters, repeated status parameters, duplicate statuses, empty selections, and unknown statuses are rejected. Decisions expire when the job is pruned.
   * @returns EventRecoveryNotificationRetryHistory Saved retry request summaries and counts.
   * @throws ApiError
   */
  public static listEventRecoveryNotificationRetryHistory({
    jobId,
    status,
  }: {
    /**
     * Retained recovery job identifier whose retry decisions are listed.
     */
    jobId: string,
    /**
     * Comma-separated distinct request statuses selected as a union; omitting returns all retained requests. Aggregate totals remain unfiltered.
     */
    status?: string,
  }): CancelablePromise<EventRecoveryNotificationRetryHistory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-recoveries/{jobID}/notification-retry-decisions',
      path: {
        'jobID': jobId,
      },
      query: {
        'status': status,
      },
      errors: {
        400: `Invalid job identifier or unsupported query parameters for retry history.`,
        401: `Authentication required to list retry decisions.`,
        403: `Read scope or MFA required to list retry decisions.`,
        404: `Recovery job not found or not owned for retry history.`,
        429: `Retry history request rate limited.`,
        500: `Saved retry history could not be read.`,
      },
    });
  }
  /**
   * Inspect a saved recovery notification retry decision.
   * Requires apps:read or admin and MFA. Returns the original queued or skipped decision for each target and current retained delivery status observed now. Missing or pruned deliveries are unavailable; original decisions remain unchanged. Query parameters are not accepted.
   * @returns EventRecoveryNotificationRetryDecisionDetail Frozen decision and latest retained delivery status.
   * @throws ApiError
   */
  public static getEventRecoveryNotificationRetryDecision({
    jobId,
    requestId,
  }: {
    /**
     * Retained recovery job that owns the saved retry decision.
     */
    jobId: string,
    /**
     * Canonical request identifier returned by retry history.
     */
    requestId: string,
  }): CancelablePromise<EventRecoveryNotificationRetryDecisionDetail> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-recoveries/{jobID}/notification-retry-decisions/{requestID}',
      path: {
        'jobID': jobId,
        'requestID': requestId,
      },
      errors: {
        400: `Invalid request or recovery identifier or unsupported query parameters.`,
        401: `Authentication required to inspect a saved retry decision.`,
        403: `Read scope or MFA required to inspect a saved retry decision.`,
        404: `Recovery job or saved request not found or not owned.`,
        429: `Retry decision detail request rate limited.`,
        500: `Saved retry decision could not be read.`,
      },
    });
  }
  /**
   * Inspect retained recovery control audit history.
   * Requires apps:read or admin and MFA. Entries are ordered by ID ascending. Actor identity is server-resolved. Failed requests and no-op controls add no entry. Expiry discovered by controls commits a system entry even when the request returns 409. History is retained and deleted with its job. Existing jobs have no fabricated historical entries.
   * @returns EventRecoveryHistory Retained audit history page.
   * @throws ApiError
   */
  public static listEventRecoveryHistory({
    jobId,
    after,
    limit = 100,
  }: {
    /**
     * Durable recovery job identifier for this operation: inspect retained recovery control audit history.
     */
    jobId: string,
    /**
     * Exclusive last history entry ID; IDs need not be contiguous.
     */
    after?: number,
    /**
     * Maximum page size for this operation: inspect retained recovery control audit history.
     */
    limit?: number,
  }): CancelablePromise<EventRecoveryHistory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-recoveries/{jobID}/history',
      path: {
        'jobID': jobId,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `Invalid pagination.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Recovery history request timed out.`,
      },
    });
  }
  /**
   * Read a stable page of selected recipients and recovery outcomes.
   * Read a stable page of selected recipients and recovery outcomes. Requires `apps:read` or `admin`.
   * @returns EventRecoveryItems Read a stable page of selected recipients and recovery outcomes: recovery result.
   * @throws ApiError
   */
  public static listEventRecoveryItems({
    jobId,
    after,
    limit = 100,
  }: {
    /**
     * Durable recovery job identifier for this operation: read a stable page of selected recipients and recovery outcomes.
     */
    jobId: string,
    /**
     * Last item position returned on the preceding page of this job.
     */
    after?: number,
    /**
     * Maximum page size for this operation: read a stable page of selected recipients and recovery outcomes.
     */
    limit?: number,
  }): CancelablePromise<EventRecoveryItems> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-recoveries/{jobID}/items',
      path: {
        'jobID': jobId,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `Read a stable page of selected recipients and recovery outcomes: invalid request or selection larger than 10000 recipients.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Read a stable page of selected recipients and recovery outcomes: recovery request timed out.`,
      },
    });
  }
  /**
   * Read durable event backfill progress.
   * Returns account-scoped scan and per-envelope routing counts without exposing payloads.
   * @returns EventReplayBackfillJobResponse Current replay job progress.
   * @throws ApiError
   */
  public static getEventReplayBackfill({
    jobId,
  }: {
    /**
     * Durable backfill job identifier returned at creation.
     */
    jobId: string,
  }): CancelablePromise<EventReplayBackfillJobResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-replays/{jobID}',
      path: {
        'jobID': jobId,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Job status read timed out.`,
      },
    });
  }
  /**
   * List per-envelope outcomes for a durable backfill.
   * Returns stable, acceptance-ordered metadata pages. Event identity and routing outcomes remain readable after the source envelope is pruned; event payloads are never returned.
   * @returns EventReplayBackfillItemsResponse One metadata-only page of backfill outcomes.
   * @throws ApiError
   */
  public static listEventReplayBackfillItems({
    jobId,
    state,
    after,
    limit = 50,
  }: {
    /**
     * Durable backfill job identifier.
     */
    jobId: string,
    /**
     * Filter items to one durable routing outcome.
     */
    state?: 'pending' | 'processing' | 'enqueued' | 'filtered' | 'failed' | 'skipped_captured' | 'skipped_unknown' | 'skipped_existing' | 'skipped_unsettled',
    /**
     * Opaque continuation cursor. Keep the job and state filter unchanged.
     */
    after?: string,
    /**
     * Maximum items returned in this page.
     */
    limit?: number,
  }): CancelablePromise<EventReplayBackfillItemsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-replays/{jobID}/items',
      path: {
        'jobID': jobId,
      },
      query: {
        'state': state,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Item read timed out.`,
      },
    });
  }
  /**
   * Retry a bounded batch of failed backfill deliveries.
   * Resets up to 100 failed routing recipients in this job with a fresh routing generation; handler retries and dead letters remain independent.
   * @returns EventReplayBackfillRetryResponse Failed routing deliveries requeued.
   * @throws ApiError
   */
  public static retryFailedEventReplayBackfill({
    jobId,
    requestBody,
  }: {
    /**
     * Backfill job whose eligible failed routing deliveries are being retried.
     */
    jobId: string,
    requestBody: EventReplayBackfillRetryRequest,
  }): CancelablePromise<EventReplayBackfillRetryResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/event-replays/{jobID}/retry-failed',
      path: {
        'jobID': jobId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Job has not completed with failures (event_replay_backfill_state).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Retry request timed out.`,
      },
    });
  }
  /**
   * Inspect event delivery lifecycle for an app.
   * Returns original event-triggered invocation metadata, operator replay
   * invocations carrying event identity headers, and terminal fanout
   * recipient failures, each newest first. The projections include the
   * published event identity, subscription, lifecycle state, attempts,
   * and last error without returning payloads. The two histories have
   * independent pagination cursors. event_id alone searches across event
   * sources; provide event_source with event_id to select one event identity.
   *
   * @returns EventDeliveryListResponse App-scoped event deliveries and terminal fanout failures, newest first.
   * @throws ApiError
   */
  public static listEventDeliveries({
    slug,
    eventSource,
    eventId,
    state,
    before,
    limit = 20,
    fanoutBefore,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Optional exact published event source; requires event_id.
     */
    eventSource?: string,
    /**
     * Published event ID; without event_source, matching IDs from all sources are included.
     */
    eventId?: string,
    /**
     * Exact delivery state to include; failed also includes pre-invocation recipient routing failures.
     */
    state?: 'pending' | 'dispatching' | 'completed' | 'failed' | 'dead_letter',
    /**
     * Opaque cursor from next_before, bound to this app and the event identity and state filters.
     */
    before?: string,
    /**
     * Maximum number of rows to return; capped at 200.
     */
    limit?: number,
    /**
     * Opaque cursor from next_fanout_before for this app and event source and ID filters.
     */
    fanoutBefore?: string,
  }): CancelablePromise<EventDeliveryListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-deliveries',
      path: {
        'slug': slug,
      },
      query: {
        'event_source': eventSource,
        'event_id': eventId,
        'state': state,
        'before': before,
        'limit': limit,
        'fanout_before': fanoutBefore,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
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
   * Inspect one event recipient's fanout attempt history.
   * Returns the routing outcomes and explicit operator replay requests
   * retained for one app-scoped event identity. This immutable history is
   * separate from the current recipient checkpoint and survives replay.
   * Event source and ID are required so reused IDs cannot mix histories.
   *
   * @returns EventFanoutAttemptHistoryResponse Recipient routing attempt history, newest first.
   * @throws ApiError
   */
  public static listEventFanoutAttemptHistory({
    slug,
    eventSource,
    eventId,
    subscriptionId,
    before,
    limit = 20,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Exact published event source.
     */
    eventSource: string,
    /**
     * Exact published event ID.
     */
    eventId: string,
    /**
     * Optional filter for one captured recipient.
     */
    subscriptionId?: string,
    /**
     * Opaque cursor from next_before, bound to this app, event identity, and recipient filter.
     */
    before?: string,
    /**
     * Maximum number of rows to return; capped at 200.
     */
    limit?: number,
  }): CancelablePromise<EventFanoutAttemptHistoryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-deliveries/attempts',
      path: {
        'slug': slug,
      },
      query: {
        'event_source': eventSource,
        'event_id': eventId,
        'subscription_id': subscriptionId,
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
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
   * Retry one terminal event recipient routing failure.
   * Requeues only the named failed recipient captured at acceptance or
   * added by a retained historical backfill. Backfill failures must be
   * retryable and have a running or completed-with-failures job; reopening
   * a completed job observes active-job limits. Other recipients and
   * their outcomes are left untouched. With independent recipient routing,
   * a terminal recipient can be replayed while siblings are active and gets
   * a fresh routing retry budget. Legacy receipts must settle first.
   *
   * @returns ReplayEventFanoutFailureResponse One recipient accepted for replay.
   * @throws ApiError
   */
  public static replayEventFanoutFailure({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: ReplayEventFanoutFailureRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ReplayEventFanoutFailureResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-deliveries:replay-fanout-failure',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Replay a bounded batch of retryable event recipient failures.
   * Requeues up to 100 terminal recipients for the app whose stable failure
   * classification marks them retryable. Optional event_source and event_id
   * narrow the action to one published event and must be supplied together.
   * Non-retryable recipients are left alone. Independent recipient routing
   * permits replay while siblings are active and renews each selected
   * recipient's routing retry budget. Legacy receipts with active whole-event
   * claims are skipped; repeat after they settle if has_more remains true.
   *
   * @returns ReplayRetryableEventFanoutFailuresResponse Bounded replay accepted.
   * @throws ApiError
   */
  public static replayRetryableEventFanoutFailures({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: ReplayRetryableEventFanoutFailuresRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ReplayRetryableEventFanoutFailuresResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-deliveries:replay-retryable-fanout-failures',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
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
   * Page through retained acknowledgment outcome groups.
   * Requires apps:read or admin. Orders immutable first occurrence and identity fields descending so renewals cannot move groups between pages. A fixed first-page cutoff excludes later-created groups. Counts and last_at remain live; deletion, retention and initially uncommitted inserts prevent a database snapshot guarantee. Filters select groups by first_at, not individual occurrences. Repeat all filters and the page size with the cursor. Cursors expire one hour after the cutoff and grant no authority. The existing acknowledgment-history operation keeps its latest-update ordering.
   * @returns EventAcknowledgmentHistoryPage Retained outcome page with stable ordering and an optional continuation cursor.
   * @throws ApiError
   */
  public static listEventAcknowledgmentHistoryPage({
    slug,
    subscriptionId,
    invocationId,
    kind,
    from,
    until,
    limit = 100,
    cursor,
  }: {
    /**
     * Application slug owning the paginated outcome view.
     */
    slug: string,
    /**
     * Consumer subscription used to bind page cursors.
     */
    subscriptionId: string,
    /**
     * Restrict all pages to the specified delivery UUID.
     */
    invocationId?: string,
    /**
     * Restrict pages to one deferred outcome kind.
     */
    kind?: 'accepted' | 'renewed' | 'renewal_rejected' | 'completed' | 'expired' | 'retry_requested' | 'dead_letter_requested',
    /**
     * Inclusive first occurrence lower bound, normalized to UTC microseconds.
     */
    from?: string,
    /**
     * Exclusive first occurrence upper bound, normalized to UTC microseconds.
     */
    until?: string,
    /**
     * Maximum groups per page, fixed for the cursor traversal.
     */
    limit?: number,
    /**
     * Opaque continuation bound to account, application, consumer, filters and page size.
     */
    cursor?: string,
  }): CancelablePromise<EventAcknowledgmentHistoryPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/acknowledgment-history/pages',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'invocation_id': invocationId,
        'kind': kind,
        'from': from,
        'until': until,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `Invalid, expired or mismatched history page cursor or filters.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Paginated acknowledgment read exceeded its request deadline.`,
      },
    });
  }
  /**
   * Inspect retained deferred acknowledgment outcomes.
   * Requires apps:read or admin. Returns the 100 most recently updated outcome groups for this consumer, optionally restricted to one delivery. Repeated operations coalesce per attempt and kind; occurrences saturates at 2147483647. Retention ends seven days after the attempt deadline, or earlier when the invocation is deleted. Invalid and replaced tokens produce no history. This is diagnostic coverage since rollout, not a complete audit log or a rate denominator.
   * @returns EventAcknowledgmentHistory Bounded coalesced acknowledgment history, with explicit truncation.
   * @throws ApiError
   */
  public static listEventAcknowledgmentHistory({
    slug,
    subscriptionId,
    invocationId,
  }: {
    /**
     * Application slug owning acknowledgment history.
     */
    slug: string,
    /**
     * Consumer subscription whose retained outcomes are inspected.
     */
    subscriptionId: string,
    /**
     * Optional delivery UUID restricting consumer outcome history.
     */
    invocationId?: string,
  }): CancelablePromise<EventAcknowledgmentHistory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/acknowledgment-history',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      query: {
        'invocation_id': invocationId,
      },
      errors: {
        400: `Malformed acknowledgment history scope or delivery filter.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Acknowledgment history read exceeded its time budget.`,
      },
    });
  }
  /**
   * Inspect future delivery acknowledgment policy.
   * Requires apps:read or admin. Tokens are never returned by inspection. Policies affect future acceptances and explicit backfills only. Zero preserves HTTP-success acknowledgment; 1–300 seconds enables a deferred lease after HTTP 202.
   * @returns EventAcknowledgmentPolicy Inspect future delivery acknowledgment policy result.
   * @throws ApiError
   */
  public static getEventAcknowledgmentPolicy({
    slug,
    subscriptionId,
  }: {
    /**
     * slug identifying the target for getEventAcknowledgmentPolicy.
     */
    slug: string,
    /**
     * subscriptionID identifying the target for getEventAcknowledgmentPolicy.
     */
    subscriptionId: string,
  }): CancelablePromise<EventAcknowledgmentPolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/acknowledgment-policy',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      errors: {
        400: `Invalid scope, token or policy for getEventAcknowledgmentPolicy.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Expired lease, replaced attempt or incompatible completion for getEventAcknowledgmentPolicy.`,
        504: `Acknowledgment request timed out for getEventAcknowledgmentPolicy.`,
      },
    });
  }
  /**
   * Configure future delivery acknowledgment policy.
   * Requires deploy:write or admin. Writes remain app-scoped; delivery callbacks additionally require the opaque token. Never log tokens. Policies affect future acceptances and explicit backfills only. Zero preserves HTTP-success acknowledgment; 1–300 seconds enables a deferred lease after HTTP 202.
   * @returns EventAcknowledgmentPolicy Configure future delivery acknowledgment policy result.
   * @throws ApiError
   */
  public static setEventAcknowledgmentPolicy({
    slug,
    subscriptionId,
    requestBody,
  }: {
    /**
     * slug identifying the target for setEventAcknowledgmentPolicy.
     */
    slug: string,
    /**
     * subscriptionID identifying the target for setEventAcknowledgmentPolicy.
     */
    subscriptionId: string,
    requestBody: EventAcknowledgmentPolicyRequest,
  }): CancelablePromise<EventAcknowledgmentPolicy> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/event-subscriptions/{subscriptionID}/acknowledgment-policy',
      path: {
        'slug': slug,
        'subscriptionID': subscriptionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid scope, token or policy for setEventAcknowledgmentPolicy.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Expired lease, replaced attempt or incompatible completion for setEventAcknowledgmentPolicy.`,
        504: `Acknowledgment request timed out for setEventAcknowledgmentPolicy.`,
      },
    });
  }
  /**
   * Inspect retained delivery processing state.
   * Requires apps:read or admin. Tokens are never returned by inspection. Processing state distinguishes waiting, delivering, processing, expired and terminal invocation states. It does not prove business effects outside the consumer transaction.
   * @returns EventAcknowledgmentStatus Inspect retained delivery processing state result.
   * @throws ApiError
   */
  public static getEventAcknowledgment({
    slug,
    invocationId,
  }: {
    /**
     * slug identifying the target for getEventAcknowledgment.
     */
    slug: string,
    /**
     * invocationID identifying the target for getEventAcknowledgment.
     */
    invocationId: string,
  }): CancelablePromise<EventAcknowledgmentStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-deliveries/{invocationID}/acknowledgment',
      path: {
        'slug': slug,
        'invocationID': invocationId,
      },
      errors: {
        400: `Invalid scope, token or policy for getEventAcknowledgment.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Expired lease, replaced attempt or incompatible completion for getEventAcknowledgment.`,
        504: `Acknowledgment request timed out for getEventAcknowledgment.`,
      },
    });
  }
  /**
   * Renew a deferred delivery processing lease.
   * Requires deploy:write or admin. Writes remain app-scoped; delivery callbacks additionally require the opaque token. Never log tokens. Renewal requires an unexpired awaiting lease and cannot exceed the one-hour attempt deadline. A 409 during the 202 handoff may be retried with the same token.
   * @returns EventAcknowledgmentStatus Renew a deferred delivery processing lease result.
   * @throws ApiError
   */
  public static renewEventAcknowledgment({
    slug,
    invocationId,
    requestBody,
  }: {
    /**
     * slug identifying the target for renewEventAcknowledgment.
     */
    slug: string,
    /**
     * invocationID identifying the target for renewEventAcknowledgment.
     */
    invocationId: string,
    requestBody: EventAcknowledgmentRequest,
  }): CancelablePromise<EventAcknowledgmentStatus> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-deliveries/{invocationID}/acknowledgment/renew',
      path: {
        'slug': slug,
        'invocationID': invocationId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid scope, token or policy for renewEventAcknowledgment.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Expired lease, replaced attempt or incompatible completion for renewEventAcknowledgment.`,
        504: `Acknowledgment request timed out for renewEventAcknowledgment.`,
      },
    });
  }
  /**
   * Complete a deferred delivery attempt.
   * Requires deploy:write or admin. Writes remain app-scoped; delivery callbacks additionally require the opaque token. Never log tokens. Complete only after durable business processing. Matching successful repeats return the saved completion state. Replaced attempts and changed results return 409. Completion can precede the scheduler observing 202.
   * @returns EventAcknowledgmentStatus Complete a deferred delivery attempt result.
   * @throws ApiError
   */
  public static completeEventAcknowledgment({
    slug,
    invocationId,
    requestBody,
  }: {
    /**
     * slug identifying the target for completeEventAcknowledgment.
     */
    slug: string,
    /**
     * invocationID identifying the target for completeEventAcknowledgment.
     */
    invocationId: string,
    requestBody: EventAcknowledgmentRequest,
  }): CancelablePromise<EventAcknowledgmentStatus> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-deliveries/{invocationID}/acknowledgment/complete',
      path: {
        'slug': slug,
        'invocationID': invocationId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid scope, token or policy for completeEventAcknowledgment.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Expired lease, replaced attempt or incompatible completion for completeEventAcknowledgment.`,
        504: `Acknowledgment request timed out for completeEventAcknowledgment.`,
      },
    });
  }
  /**
   * Retry a deferred delivery attempt.
   * Requires deploy:write or admin and the current delivery token. Atomically releases the claim and quota and schedules the captured backoff; an exhausted captured attempt budget dead-letters instead. Matching repeats return the saved disposition until the attempt or replay generation is replaced. Changed reason or action returns 409. May precede the scheduler observing 202. Reason is a bounded non-sensitive code. Does not undo consumer effects.
   * @returns EventAcknowledgmentStatus Retry a deferred delivery attempt result.
   * @throws ApiError
   */
  public static retryEventAcknowledgment({
    slug,
    invocationId,
    requestBody,
  }: {
    /**
     * slug identifying the target for retryEventAcknowledgment.
     */
    slug: string,
    /**
     * invocationID identifying the target for retryEventAcknowledgment.
     */
    invocationId: string,
    requestBody: EventAcknowledgmentFailureRequest,
  }): CancelablePromise<EventAcknowledgmentStatus> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-deliveries/{invocationID}/acknowledgment/retry',
      path: {
        'slug': slug,
        'invocationID': invocationId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid scope, token or policy for retryEventAcknowledgment.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Expired lease, replaced attempt or incompatible completion for retryEventAcknowledgment.`,
        504: `Acknowledgment request timed out for retryEventAcknowledgment.`,
      },
    });
  }
  /**
   * Dead-letter a deferred delivery attempt.
   * Requires deploy:write or admin and the current delivery token. Atomically releases the claim and quota and dead-letters this delivery independently of other consumers. Matching repeats return the saved disposition until replacement; changed reason or action returns 409. May precede the scheduler observing 202. Reason is a bounded non-sensitive code. Does not undo consumer effects.
   * @returns EventAcknowledgmentStatus Dead-letter a deferred delivery attempt result.
   * @throws ApiError
   */
  public static deadLetterEventAcknowledgment({
    slug,
    invocationId,
    requestBody,
  }: {
    /**
     * slug identifying the target for deadLetterEventAcknowledgment.
     */
    slug: string,
    /**
     * invocationID identifying the target for deadLetterEventAcknowledgment.
     */
    invocationId: string,
    requestBody: EventAcknowledgmentFailureRequest,
  }): CancelablePromise<EventAcknowledgmentStatus> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/event-deliveries/{invocationID}/acknowledgment/dead-letter',
      path: {
        'slug': slug,
        'invocationID': invocationId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid scope, token or policy for deadLetterEventAcknowledgment.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Expired lease, replaced attempt or incompatible completion for deadLetterEventAcknowledgment.`,
        504: `Acknowledgment request timed out for deadLetterEventAcknowledgment.`,
      },
    });
  }
}
