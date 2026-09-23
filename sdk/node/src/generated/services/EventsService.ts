/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventDeliveryListResponse } from '../models/EventDeliveryListResponse.js';
import type { EventSubscriptionListResponse } from '../models/EventSubscriptionListResponse.js';
import type { PublishEventRequest } from '../models/PublishEventRequest.js';
import type { PublishEventResponse } from '../models/PublishEventResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class EventsService {
  /**
   * Publish one tenant-scoped internal event.
   * Persists a canonical CloudEvents-shaped envelope for later content
   * matching and delivery. The authenticated account owns the event;
   * account_id is server-stamped and a supplied value must match it.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
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
   * Returns event-triggered invocation metadata, newest first. The
   * projection includes the published event identity, subscription,
   * lifecycle state, attempts, and last error without returning payloads.
   *
   * @returns EventDeliveryListResponse App-scoped event delivery page, newest first.
   * @throws ApiError
   */
  public static listEventDeliveries({
    slug,
    eventId,
    state,
    before,
    limit = 20,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Exact published event id to inspect.
     */
    eventId?: string,
    /**
     * Exact delivery state to include.
     */
    state?: 'pending' | 'dispatching' | 'completed' | 'failed' | 'dead_letter',
    /**
     * Cursor — the last id from the previous page (omit for the first page).
     */
    before?: string,
    /**
     * Maximum number of rows to return; capped at 200.
     */
    limit?: number,
  }): CancelablePromise<EventDeliveryListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/event-deliveries',
      path: {
        'slug': slug,
      },
      query: {
        'event_id': eventId,
        'state': state,
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
}
