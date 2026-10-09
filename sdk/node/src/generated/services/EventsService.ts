/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventBacklogResponse } from '../models/EventBacklogResponse.js';
import type { EventCircuitBreakerPolicy } from '../models/EventCircuitBreakerPolicy.js';
import type { EventCircuitBreakerResponse } from '../models/EventCircuitBreakerResponse.js';
import type { EventConsumerExecutionHealth } from '../models/EventConsumerExecutionHealth.js';
import type { EventConsumerHealth } from '../models/EventConsumerHealth.js';
import type { EventDeliveryListResponse } from '../models/EventDeliveryListResponse.js';
import type { EventFanoutAttemptHistoryResponse } from '../models/EventFanoutAttemptHistoryResponse.js';
import type { EventReceiptAttemptHistoryResponse } from '../models/EventReceiptAttemptHistoryResponse.js';
import type { EventReceiptReplayHistoryResponse } from '../models/EventReceiptReplayHistoryResponse.js';
import type { EventReceiptResponse } from '../models/EventReceiptResponse.js';
import type { EventRecoveryControlRequest } from '../models/EventRecoveryControlRequest.js';
import type { EventRecoveryHealth } from '../models/EventRecoveryHealth.js';
import type { EventRecoveryHistory } from '../models/EventRecoveryHistory.js';
import type { EventRecoveryItems } from '../models/EventRecoveryItems.js';
import type { EventRecoveryJob } from '../models/EventRecoveryJob.js';
import type { EventRecoveryJobs } from '../models/EventRecoveryJobs.js';
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
import type { EventSubscriptionDeliveryControl } from '../models/EventSubscriptionDeliveryControl.js';
import type { EventSubscriptionListResponse } from '../models/EventSubscriptionListResponse.js';
import type { EventSubscriptionResumeRequest } from '../models/EventSubscriptionResumeRequest.js';
import type { EventSubscriptionSchemaVersionsRequest } from '../models/EventSubscriptionSchemaVersionsRequest.js';
import type { EventSubscriptionSchemaVersionsResponse } from '../models/EventSubscriptionSchemaVersionsResponse.js';
import type { PlatformTenantPublishEventResponse } from '../models/PlatformTenantPublishEventResponse.js';
import type { PreviewEventRequest } from '../models/PreviewEventRequest.js';
import type { PreviewEventResponse } from '../models/PreviewEventResponse.js';
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
import type { WorkflowEventReplayBackfillRequest } from '../models/WorkflowEventReplayBackfillRequest.js';
import type { WorkflowEventReplayPreviewResponse } from '../models/WorkflowEventReplayPreviewResponse.js';
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
   * Inspect receipt pruning eligibility, backfill holds and account storage.
   * Read-only account snapshot under apps:read/admin scopes and MFA.
   * Receipt retention is 30 days after routing settles; unsettled receipts
   * have no pruning deadline. Reports current eligible, upcoming expiring
   * and held receipts using the pruning worker's shared hold predicate.
   * Running backfills pin their acceptance ranges; retained completed
   * backfills pin retryable failed items. Bulk recovery jobs do not pin
   * receipts. Eligible means the nominal deadline has passed without a
   * current backfill hold, not that pruning will occur immediately.
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
   * Inspect consumer execution health over a retained history window.
   * Requires `apps:read` or `admin`. Reports current retained execution states and windowed attempt outcomes for admitted deliveries and handler replays. Receipt and invocation retention bound coverage; history_complete is always false. Counts are not unique event totals. Completion latency starts at original event acceptance.
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
   * Inspect active recovery progress and expiry risk.
   * Requires apps:read or admin and MFA. Current app-scoped observations of active jobs only. Stalled requires five minutes without admission progress and five minutes overdue for eligibility. Capacity/legacy claim retries and pacing defer eligibility. Paused jobs are reported separately and excluded from running-job alert counts. Expiry warnings cover pending items within one hour of expiry, including overdue expiry cleanup. No query parameters are accepted.
   * @returns EventRecoveryHealth Current active recovery health; terminal jobs are omitted.
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
    mode?: 'routing' | 'execution',
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
   * Assess a frozen recovery selection without admitting work.
   * Requires apps:read or admin and MFA. Read-only observations of all pending items, with up to 100 sample positions. Current per-item eligibility and capacity are not reservations. Conditions can change before admission. The optimistic rate-window estimate includes all pending admissions/skips and spent permits, assumes immediate resume, and excludes future contention, worker delays and handler execution. No query parameters are accepted.
   * @returns EventRecoveryPreflight Current eligibility observations and optimistic timing estimate.
   * @throws ApiError
   */
  public static getEventRecoveryPreflight({
    jobId,
  }: {
    /**
     * Durable recovery job identifier for this operation: assess a frozen recovery selection without admitting work.
     */
    jobId: string,
  }): CancelablePromise<EventRecoveryPreflight> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/event-recoveries/{jobID}/preflight',
      path: {
        'jobID': jobId,
      },
      errors: {
        400: `Invalid job ID or query parameters.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        504: `Recovery preflight timed out.`,
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
}
