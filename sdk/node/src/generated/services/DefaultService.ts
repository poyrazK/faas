/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FireCronRequestResponse } from '../models/FireCronRequestResponse.js';
import type { ManagedRealtimeChannelBatchRequest } from '../models/ManagedRealtimeChannelBatchRequest.js';
import type { ManagedRealtimeChannelBatchResponse } from '../models/ManagedRealtimeChannelBatchResponse.js';
import type { ManagedRealtimeChannelSnapshotRequest } from '../models/ManagedRealtimeChannelSnapshotRequest.js';
import type { ManagedRealtimeChannelSnapshotResponse } from '../models/ManagedRealtimeChannelSnapshotResponse.js';
import type { ManagedRealtimeEventSchemaRequest } from '../models/ManagedRealtimeEventSchemaRequest.js';
import type { ManagedRealtimeEventSchemaResponse } from '../models/ManagedRealtimeEventSchemaResponse.js';
import type { ManagedRealtimeNotificationControlResponse } from '../models/ManagedRealtimeNotificationControlResponse.js';
import type { ManagedRealtimeNotificationRescheduleRequest } from '../models/ManagedRealtimeNotificationRescheduleRequest.js';
import type { ManagedRealtimeNotificationTimelineEvent } from '../models/ManagedRealtimeNotificationTimelineEvent.js';
import type { ManagedRealtimeReducerRequest } from '../models/ManagedRealtimeReducerRequest.js';
import type { ManagedRealtimeReducerResponse } from '../models/ManagedRealtimeReducerResponse.js';
import type { ManagedRealtimeScheduleGroupRequest } from '../models/ManagedRealtimeScheduleGroupRequest.js';
import type { ManagedRealtimeScheduleHistoryResponse } from '../models/ManagedRealtimeScheduleHistoryResponse.js';
import type { ManagedRealtimeSchedulePauseRequest } from '../models/ManagedRealtimeSchedulePauseRequest.js';
import type { ManagedRealtimeScheduleRequest } from '../models/ManagedRealtimeScheduleRequest.js';
import type { ManagedRealtimeScheduleResponse } from '../models/ManagedRealtimeScheduleResponse.js';
import type { ManagedRealtimeScheduleRetryRequest } from '../models/ManagedRealtimeScheduleRetryRequest.js';
import type { ManagedRealtimeSchedulesResponse } from '../models/ManagedRealtimeSchedulesResponse.js';
import type { ManagedRealtimeScheduleTotals } from '../models/ManagedRealtimeScheduleTotals.js';
import type { ManagedRealtimeScheduleUpdate } from '../models/ManagedRealtimeScheduleUpdate.js';
import type { ManagedRealtimeSignalRequest } from '../models/ManagedRealtimeSignalRequest.js';
import type { ManagedRealtimeSignalResponse } from '../models/ManagedRealtimeSignalResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DefaultService {
  /**
   * Get fire-now request state
   * Polling surface for the row that `POST /v1/crons/{id}/run`
   * inserted (issue #791 PR-D / ADR-090 §Sub-decision 7).
   *
   * @returns FireCronRequestResponse Current state of the fire-now request.
   * @throws ApiError
   */
  public static getFireCronRequest({
    requestId,
  }: {
    /**
     * Fire-now request identifier returned by `POST /v1/crons/{id}/run`.
     */
    requestId: string,
  }): CancelablePromise<FireCronRequestResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/cron-fire-now-requests/{request_id}',
      path: {
        'request_id': requestId,
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
   * Send a backend signal to a private activity scope
   * Publishes to the canonical isolated activity channel for this parent and scope. Scope labels are at most 64 UTF-8 bytes and the encoded channel must fit 256 bytes. Subscribers require a separate read_activity authorization callback grant. Best-effort live delivery with no retained history, sequence, replay, or idempotency receipt. Sender member_id is backend. JSON data is limited to 2048 encoded bytes and the endpoint payload limit; request body is limited to 4096 bytes. Rate limit is 20 requests per second per endpoint/channel per API process, in addition to normal auth limits. Named signals default to a 5000 ms expiry; ttl_ms 0 clears a named signal. Deploy-write scopes and MFA apply. Acceptance is not subscriber acknowledgement; failures may follow partial delivery.
   * @returns ManagedRealtimeSignalResponse Best-effort fanout accepted, including when no subscribers exist
   * @throws ApiError
   */
  public static publishManagedRealtimeScopedSignal({
    slug,
    id,
    channel,
    activityScope,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Private activity scope
     */
    activityScope: string,
    requestBody: ManagedRealtimeSignalRequest,
  }): CancelablePromise<ManagedRealtimeSignalResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/activity-scopes/{activity_scope}/signals',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'activity_scope': activityScope,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Send an ephemeral backend signal to connected v2 channel subscribers
   * Best-effort live delivery with no retained history, sequence, replay, or idempotency receipt. Sender member_id is backend. JSON data is limited to 2048 encoded bytes and the endpoint payload limit; request body is limited to 4096 bytes. Rate limit is 20 requests per second per endpoint/channel per API process, in addition to normal auth limits. Named signals default to a 5000 ms expiry; ttl_ms 0 clears a named signal. Deploy-write scopes and MFA apply. Acceptance is not subscriber acknowledgement; failures may follow partial delivery.
   * @returns ManagedRealtimeSignalResponse Channel signal fanout accepted even when no subscribers exist.
   * @throws ApiError
   */
  public static publishManagedRealtimeSignal({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    requestBody: ManagedRealtimeSignalRequest,
  }): CancelablePromise<ManagedRealtimeSignalResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/signals',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * List pending and recent terminal retained-event schedules
   * @returns ManagedRealtimeSchedulesResponse Schedules ordered by delivery time and ID
   * @throws ApiError
   */
  public static listManagedRealtimeSchedules({
    slug,
    id,
    channel,
    group,
    status,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Schedule group filter
     */
    group?: string,
    /**
     * Schedule status filter
     */
    status?: 'pending' | 'paused' | 'published' | 'failed' | 'skipped' | 'canceled',
  }): CancelablePromise<ManagedRealtimeSchedulesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      query: {
        'group': group,
        'status': status,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Atomically pause, resume, or cancel a channel schedule group
   * Requires exactly every pending/paused member's version. Membership/version changes or incompatible transitions reject the entire action. Terminal members are excluded. Already-target-state members remain unchanged. Supports one-time and recurring schedules; deploy-write scopes, MFA and preview gating apply.
   * @returns any Pending/paused members included in the action, with updated states and totals
   * @throws ApiError
   */
  public static applyManagedRealtimeScheduleGroup({
    slug,
    id,
    channel,
    group,
    groupAction,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Schedule group
     */
    group: string,
    /**
     * Bulk schedule action
     */
    groupAction: 'pause' | 'resume' | 'cancel',
    requestBody: ManagedRealtimeScheduleGroupRequest,
  }): CancelablePromise<{
    schedules: Array<ManagedRealtimeScheduleResponse>;
    totals: ManagedRealtimeScheduleTotals;
  }> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/groups/{group}/{group_action}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
        'group_action': groupAction,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Schedule a retained channel event using an idempotent schedule ID
   * Future delivery within 30 days; timestamps normalize to microseconds.
   * Up to 256 pending and recent terminal schedules per endpoint. Terminal
   * receipts remain available for 24 hours. Identical ID/content retries
   * return the existing record; different content conflicts. Schema and reducer
   * validation run at delivery time. Preview gate and deploy-write scopes apply.
   *
   * @returns ManagedRealtimeScheduleResponse Pending schedule or identical existing record
   * @throws ApiError
   */
  public static putManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Bounded schedule identifier
     */
    scheduleId: string,
    requestBody: ManagedRealtimeScheduleRequest,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        409: `code: conflict`,
        429: `Endpoint schedule capacity reached`,
      },
    });
  }
  /**
   * Change a pending schedule deadline with a version check
   * @returns ManagedRealtimeScheduleResponse Updated pending schedule
   * @throws ApiError
   */
  public static rescheduleManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Bounded schedule identifier
     */
    scheduleId: string,
    requestBody: ManagedRealtimeScheduleUpdate,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Cancel a pending schedule with a version check
   * @returns ManagedRealtimeScheduleResponse Canceled schedule; cancellation retries are idempotent
   * @throws ApiError
   */
  public static cancelManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    expectedVersion,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Bounded schedule identifier
     */
    scheduleId: string,
    /**
     * Required current schedule version
     */
    expectedVersion: number,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      query: {
        'expected_version': expectedVersion,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Pause a recurring retained-event schedule
   * @returns ManagedRealtimeScheduleResponse Schedule updated
   * @throws ApiError
   */
  public static pauseManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Schedule ID
     */
    scheduleId: string,
    requestBody: ManagedRealtimeSchedulePauseRequest,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/pause',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Resume a recurring retained-event schedule
   * @returns ManagedRealtimeScheduleResponse Schedule resumed with its updated version.
   * @throws ApiError
   */
  public static resumeManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Schedule ID
     */
    scheduleId: string,
    requestBody: ManagedRealtimeSchedulePauseRequest,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/resume',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Inspect schedule changes and recorded delivery attempts
   * Events commit with schedule changes. Up to 128 entries are retained per
   * schedule, ordered by version. history_truncated marks missing earlier
   * history, including migrated baseline records. Terminal history expires
   * with its schedule receipt. Preview, ownership, MFA, and read scopes apply.
   *
   * @returns ManagedRealtimeScheduleHistoryResponse Consistent schedule timeline snapshot
   * @throws ApiError
   */
  public static getManagedRealtimeScheduleHistory({
    slug,
    id,
    channel,
    scheduleId,
    afterVersion,
    limit = 50,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Bounded schedule identifier
     */
    scheduleId: string,
    /**
     * Exclusive event version cursor
     */
    afterVersion?: number,
    /**
     * Maximum history records
     */
    limit?: number,
  }): CancelablePromise<ManagedRealtimeScheduleHistoryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/history',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      query: {
        'after_version': afterVersion,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Retry a failed schedule using its existing ID and payload
   * Requires the current version. Resets the cycle attempt budget, preserves lifetime attempts, and advances the version. Published or canceled schedules cannot retry.
   * @returns ManagedRealtimeScheduleResponse Retry pending
   * @throws ApiError
   */
  public static retryManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Bounded schedule identifier
     */
    scheduleId: string,
    requestBody: ManagedRealtimeScheduleRetryRequest,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/retry',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Enable a channel reducer from a state baseline matching the current sequence
   * Retained JSON events support set, merge, delete, increment, append, and remove operations.
   * Increment payloads use op=increment, key, field, and a required integer delta;
   * optional min/max bounds reject results outside the allowed range.
   * Values, deltas, bounds, and results must be safe integers in
   * [-9007199254740991, 9007199254740991]. Missing entities or fields start at zero.
   * Increment supports expected_version and preserves expires_at unless replaced
   * or cleared with null. Array operations use key, field, and items (1..32 JSON values).
   * Append accepts unique=true for structural JSON deduplication; remove deletes
   * all matching items. Optional max_length (0..256) rejects longer results.
   * Missing fields start as empty arrays; existing targets must be arrays of at
   * most 256 items. Array operations support expected_version and the same
   * expires_at behavior as increment. Every operation accepts optional conditions
   * (1..16 predicates), each with a literal field and exactly one of equals (any
   * JSON value) or absent:true. All predicates must hold before the update.
   * Equality is structural and compares numbers exactly. Failed conditions return
   * 409 realtime_condition_conflict with entity_key, condition_field,
   * condition_index, message_index, current_version, entity_exists, and field_exists.
   * Invalid operations roll back the whole batch.
   *
   * @returns ManagedRealtimeReducerResponse Enabled reducer or identical current state
   * @throws ApiError
   */
  public static putManagedRealtimeReducer({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    requestBody: ManagedRealtimeReducerRequest,
  }): CancelablePromise<ManagedRealtimeReducerResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid state or size`,
        409: `Stale baseline or attempt to reseed an active reducer`,
      },
    });
  }
  /**
   * Read channel entities and their exact sequence
   * @returns ManagedRealtimeReducerResponse Current reduced state
   * @throws ApiError
   */
  public static getManagedRealtimeReducer({
    slug,
    id,
    channel,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
  }): CancelablePromise<ManagedRealtimeReducerResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      errors: {
        404: `Reducer not enabled`,
      },
    });
  }
  /**
   * Disable the reducer and remove its state and generated snapshot
   * @returns any Reducer disabled; retained history remains
   * @throws ApiError
   */
  public static deleteManagedRealtimeReducer({
    slug,
    id,
    channel,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
    });
  }
  /**
   * Register an immutable event schema version and enable channel enforcement
   * @returns ManagedRealtimeEventSchemaResponse Registered version or identical existing version
   * @throws ApiError
   */
  public static putManagedRealtimeEventSchema({
    slug,
    id,
    channel,
    eventType,
    version,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Retained event type
     */
    eventType: string,
    /**
     * Current schema version
     */
    version: number,
    requestBody: ManagedRealtimeEventSchemaRequest,
  }): CancelablePromise<ManagedRealtimeEventSchemaResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schemas/{event_type}/{version}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'event_type': eventType,
        'version': version,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid schema or identity`,
        409: `Existing version has a different schema`,
        429: `Endpoint has 64 registered versions`,
      },
    });
  }
  /**
   * Get a registered event schema version
   * @returns ManagedRealtimeEventSchemaResponse Immutable schema
   * @throws ApiError
   */
  public static getManagedRealtimeEventSchema({
    slug,
    id,
    channel,
    eventType,
    version,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Retained event type
     */
    eventType: string,
    /**
     * Current schema version
     */
    version: number,
  }): CancelablePromise<ManagedRealtimeEventSchemaResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schemas/{event_type}/{version}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'event_type': eventType,
        'version': version,
      },
      errors: {
        404: `Version not registered`,
      },
    });
  }
  /**
   * Atomically retain a bounded batch, then attempt live delivery
   * @returns ManagedRealtimeChannelBatchResponse Durable batch, including per-message best-effort fanout outcomes
   * @throws ApiError
   */
  public static publishManagedRealtimeChannelBatch({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    requestBody: ManagedRealtimeChannelBatchRequest,
  }): CancelablePromise<ManagedRealtimeChannelBatchResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish-batch',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid batch count, size or ID`,
        409: `Sequence conflict includes current_sequence and expected_sequence; also batch ID conflict or channel capacity reached.`,
        429: `Endpoint has 256 retained batch IDs`,
      },
    });
  }
  /**
   * Get channel state and its replay baseline
   * @returns ManagedRealtimeChannelSnapshotResponse Snapshot with a replayable tail
   * @throws ApiError
   */
  public static getManagedRealtimeChannelSnapshot({
    slug,
    id,
    channel,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
  }): CancelablePromise<ManagedRealtimeChannelSnapshotResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/snapshot',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      errors: {
        404: `Snapshot or channel not found`,
        410: `Snapshot expired or tail no longer replayable`,
      },
    });
  }
  /**
   * Save state representing an explicit committed channel sequence
   * @returns ManagedRealtimeChannelSnapshotResponse Saved snapshot, expires after 24 hours
   * @throws ApiError
   */
  public static putManagedRealtimeChannelSnapshot({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    requestBody: ManagedRealtimeChannelSnapshotRequest,
  }): CancelablePromise<ManagedRealtimeChannelSnapshotResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/snapshot',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid sequence, size, or older snapshot overwrite`,
        404: `Channel has no retained history`,
        410: `Sequence no longer replayable`,
      },
    });
  }
  /**
   * Remove a snapshot without deleting channel history
   * @returns any Snapshot removed
   * @throws ApiError
   */
  public static deleteManagedRealtimeChannelSnapshot({
    slug,
    id,
    channel,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/snapshot',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
    });
  }
  /**
   * List notification state changes across devices, newest first
   * @returns ManagedRealtimeNotificationTimelineEvent Up to 100 retained events, newest first; empty when none remain.
   * @throws ApiError
   */
  public static listManagedRealtimeNotificationTimeline({
    slug,
    id,
    messageId,
    principal,
    before,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Notification message ID
     */
    messageId: string,
    /**
     * Verified principal ID
     */
    principal: string,
    /**
     * Exclusive event ID cursor. Omit or zero for the newest page.
     */
    before?: number,
  }): CancelablePromise<Array<ManagedRealtimeNotificationTimelineEvent>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}/timeline',
      path: {
        'slug': slug,
        'id': id,
        'message_id': messageId,
      },
      query: {
        'principal': principal,
        'before': before,
      },
      errors: {
        400: `Invalid principal, message ID or cursor`,
        404: `Endpoint or preview unavailable`,
      },
    });
  }
  /**
   * Cancel pending notification fallback and device deliveries without deleting inbox messages
   * @returns ManagedRealtimeNotificationControlResponse Counts of affected pending fallback timers and device deliveries; zero counts are an idempotent no-op.
   * @throws ApiError
   */
  public static cancelManagedRealtimeNotification({
    slug,
    id,
    messageId,
    principal,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Notification message ID
     */
    messageId: string,
    /**
     * Verified principal ID
     */
    principal: string,
  }): CancelablePromise<ManagedRealtimeNotificationControlResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}',
      path: {
        'slug': slug,
        'id': id,
        'message_id': messageId,
      },
      query: {
        'principal': principal,
      },
      errors: {
        400: `Invalid principal or message ID`,
        404: `Notification cancellation endpoint or preview unavailable.`,
      },
    });
  }
  /**
   * Reschedule active notifications across devices without reviving completed deliveries
   * @returns ManagedRealtimeNotificationControlResponse Counts of affected active timers and device deliveries.
   * @throws ApiError
   */
  public static rescheduleManagedRealtimeNotification({
    slug,
    id,
    messageId,
    principal,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Notification message ID
     */
    messageId: string,
    /**
     * Verified principal ID
     */
    principal: string,
    requestBody: ManagedRealtimeNotificationRescheduleRequest,
  }): CancelablePromise<ManagedRealtimeNotificationControlResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}',
      path: {
        'slug': slug,
        'id': id,
        'message_id': messageId,
      },
      query: {
        'principal': principal,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid schedule or schedule beyond expiration`,
        404: `Notification rescheduling endpoint or preview unavailable.`,
      },
    });
  }
}
