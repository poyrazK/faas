/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventDeliveryListResponse } from '../models/EventDeliveryListResponse.js';
import type { EventSchema } from '../models/EventSchema.js';
import type { EventSubscriptionListResponse } from '../models/EventSubscriptionListResponse.js';
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
   * Matching and delivery are asynchronous follow-up work.
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
   * Inspect event delivery lifecycle for an app.
   * Returns event-triggered invocation metadata and terminal fanout
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
   * Retry one terminal event recipient routing failure.
   * Requeues only the named failed recipient from the immutable recipient
   * snapshot captured when the event was accepted. Other recipients and
   * their outcomes are left untouched. The event must have settled before
   * a failed recipient can be replayed.
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
   * Non-retryable recipients are left alone, and an event being processed
   * by the fanout worker is not modified. A replayed event can accept more
   * recipients while pending; if has_more remains true while the worker is
   * processing it, repeat the request after that event settles.
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
