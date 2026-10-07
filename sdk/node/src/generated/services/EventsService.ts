/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventBacklogResponse } from '../models/EventBacklogResponse.js';
import type { EventDeliveryListResponse } from '../models/EventDeliveryListResponse.js';
import type { EventFanoutAttemptHistoryResponse } from '../models/EventFanoutAttemptHistoryResponse.js';
import type { EventReceiptAttemptHistoryResponse } from '../models/EventReceiptAttemptHistoryResponse.js';
import type { EventReceiptReplayHistoryResponse } from '../models/EventReceiptReplayHistoryResponse.js';
import type { EventReceiptResponse } from '../models/EventReceiptResponse.js';
import type { EventReplayBackfillItemsResponse } from '../models/EventReplayBackfillItemsResponse.js';
import type { EventReplayBackfillJobResponse } from '../models/EventReplayBackfillJobResponse.js';
import type { EventReplayBackfillRequest } from '../models/EventReplayBackfillRequest.js';
import type { EventReplayBackfillRetryRequest } from '../models/EventReplayBackfillRetryRequest.js';
import type { EventReplayBackfillRetryResponse } from '../models/EventReplayBackfillRetryResponse.js';
import type { EventReplayPreviewResponse } from '../models/EventReplayPreviewResponse.js';
import type { EventSchema } from '../models/EventSchema.js';
import type { EventStorageUsageResponse } from '../models/EventStorageUsageResponse.js';
import type { EventSubscriptionListResponse } from '../models/EventSubscriptionListResponse.js';
import type { PlatformTenantPublishEventResponse } from '../models/PlatformTenantPublishEventResponse.js';
import type { PreviewEventRequest } from '../models/PreviewEventRequest.js';
import type { PreviewEventResponse } from '../models/PreviewEventResponse.js';
import type { PublishEventRequest } from '../models/PublishEventRequest.js';
import type { PublishEventResponse } from '../models/PublishEventResponse.js';
import type { RegisterEventSchemaRequest } from '../models/RegisterEventSchemaRequest.js';
import type { RegisterEventSchemaResponse } from '../models/RegisterEventSchemaResponse.js';
import type { ReplayEventFanoutFailureRequest } from '../models/ReplayEventFanoutFailureRequest.js';
import type { ReplayEventFanoutFailureResponse } from '../models/ReplayEventFanoutFailureResponse.js';
import type { ReplayRetryableEventFanoutFailuresRequest } from '../models/ReplayRetryableEventFanoutFailuresRequest.js';
import type { ReplayRetryableEventFanoutFailuresResponse } from '../models/ReplayRetryableEventFanoutFailuresResponse.js';
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        500: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Discover waiting application event recipients and consumer counts.
   * Requires apps:read or admin. Reads only the authenticated account's
   * captured application recipients in pending or processing routing state,
   * in both whole-event and independent-recipient routing modes. Includes
   * capacity waits before an invocation exists; excludes settled routing and
   * handler execution queues. Returns metadata and receipt/history links,
   * never envelope data. Consumers count all matching recipients, independently
   * of either bounded page. Age is measured from durable event acceptance.
   * Recipient pages are oldest accepted first, then receipt and subscription
   * identity; consumer pages use app and subscription identity. Pass each
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
    state,
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
     * Captured application recipient identifier.
     */
    subscriptionId?: string,
    /**
     * Restrict current routing state.
     */
    state?: 'pending' | 'processing',
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
        'state': state,
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
   * and a bounded page of immutable acceptance-time recipients. Routing
   * counts cover the entire snapshot, including recipients without invocations.
   * Enqueued routing does not imply handler success: keyed cancellation can
   * produce a cancellation receipt instead. The original deterministic
   * invocation is shown when retained; trusted generic replay lineage adds
   * the latest replay and a paginated history URL without replacing the
   * original failure. Recovery actions target the latest retained replay
   * when present. Recovery actions require their existing
   * write scopes and are revalidated when called. Legacy receipts without
   * snapshots report snapshot_captured=false and cannot reconstruct recipients.
   * Pages follow acceptance order while outcomes may change between requests.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        500: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect retained handler replay history for one captured recipient.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        500: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect retained handler delivery attempts for one captured recipient.
   * Requires apps:read or admin. Includes the original async invocation and
   * trusted child replays, ordered by descending attempt history ID. Each
   * claim records its attempt and replay generation atomically with dispatch.
   * A claim does not prove handler execution. Recovered expired leases have
   * outcome unknown; external side effects still require idempotency.
   * Only attempts recorded after rollout and still retained are available.
   * Closed attempts expire after at most 30 days, earlier when result retention
   * expires or their invocation is deleted. Running attempts are not pruned.
   * Missing history does not establish that no delivery occurred. Unknown or
   * foreign receipts, captured recipients and current app owners return 404.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        500: `code: capacity — server-side error; retry with backoff.`,
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Retained-event read failed or exceeded the five-second deadline (event_replay_preview_read_timeout).`,
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Durable job creation timed out.`,
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Retry one terminal event recipient routing failure.
   * Requeues only the named failed recipient from the immutable recipient
   * snapshot captured when the event was accepted. Other recipients and
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
