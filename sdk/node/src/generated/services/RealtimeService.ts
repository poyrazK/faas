/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateManagedRealtimeEndpointRequest } from '../models/CreateManagedRealtimeEndpointRequest.js';
import type { FinalizeManagedRealtimeAuthResponse } from '../models/FinalizeManagedRealtimeAuthResponse.js';
import type { ManagedRealtimeCloseRequest } from '../models/ManagedRealtimeCloseRequest.js';
import type { ManagedRealtimeConnectionListResponse } from '../models/ManagedRealtimeConnectionListResponse.js';
import type { ManagedRealtimeDrainRequest } from '../models/ManagedRealtimeDrainRequest.js';
import type { ManagedRealtimeDrainResponse } from '../models/ManagedRealtimeDrainResponse.js';
import type { ManagedRealtimeEndpointResponse } from '../models/ManagedRealtimeEndpointResponse.js';
import type { ManagedRealtimeHistoryUsageResponse } from '../models/ManagedRealtimeHistoryUsageResponse.js';
import type { ManagedRealtimeMessageRequest } from '../models/ManagedRealtimeMessageRequest.js';
import type { ManagedRealtimePublishResponse } from '../models/ManagedRealtimePublishResponse.js';
import type { ManagedRealtimePushDelivery } from '../models/ManagedRealtimePushDelivery.js';
import type { ManagedRealtimePushDevice } from '../models/ManagedRealtimePushDevice.js';
import type { ManagedRealtimePushProvider } from '../models/ManagedRealtimePushProvider.js';
import type { ManagedRealtimePushProviderRequest } from '../models/ManagedRealtimePushProviderRequest.js';
import type { ManagedRealtimePushRegistration } from '../models/ManagedRealtimePushRegistration.js';
import type { ManagedRealtimeRetainedHistoryResponse } from '../models/ManagedRealtimeRetainedHistoryResponse.js';
import type { ManagedRealtimeRetainedMessageRequest } from '../models/ManagedRealtimeRetainedMessageRequest.js';
import type { ManagedRealtimeRetainedMessageResponse } from '../models/ManagedRealtimeRetainedMessageResponse.js';
import type { Problem } from '../models/Problem.js';
import type { RealtimeNotificationPreferences } from '../models/RealtimeNotificationPreferences.js';
import type { RotateManagedRealtimeAuthRequest } from '../models/RotateManagedRealtimeAuthRequest.js';
import type { RotateManagedRealtimeAuthResponse } from '../models/RotateManagedRealtimeAuthResponse.js';
import type { UpdateManagedRealtimeEndpointRequest } from '../models/UpdateManagedRealtimeEndpointRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class RealtimeService {
  /**
   * Read the account's retained realtime history snapshot
   * Preview only; requires usage read scope and MFA for interactive sessions.
   * Counts current retained message rows and payload bytes for this account,
   * including expired rows awaiting cleanup. Replayable counts apply the
   * channel's contiguous retention floor. These values are informational
   * snapshots, not billed usage or physical database allocation.
   *
   * @returns ManagedRealtimeHistoryUsageResponse Account-scoped retained history snapshot; Cache-Control no-store
   * @returns Problem Authentication or history usage error
   * @throws ApiError
   */
  public static getManagedRealtimeHistoryUsage(): CancelablePromise<ManagedRealtimeHistoryUsageResponse | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/realtime-history-usage',
    });
  }
  /**
   * List managed realtime endpoints for this app.
   * @returns ManagedRealtimeEndpointResponse The configured managed realtime endpoints.
   * @throws ApiError
   */
  public static listManagedRealtimeEndpoints({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<ManagedRealtimeEndpointResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Create a managed realtime endpoint.
   * Registers the callback contract used by realtimed for connect,
   * message, and disconnect events. Callback and optional client
   * credentials are sealed at rest and never returned in plaintext.
   *
   * @returns ManagedRealtimeEndpointResponse Managed realtime endpoint created.
   * @throws ApiError
   */
  public static createManagedRealtimeEndpoint({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateManagedRealtimeEndpointRequest,
  }): CancelablePromise<ManagedRealtimeEndpointResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        403: `code: plan_realtime_quota — per-app or per-account managed realtime endpoint limit reached.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Fetch one managed realtime endpoint.
   * @returns ManagedRealtimeEndpointResponse The managed realtime endpoint.
   * @throws ApiError
   */
  public static getManagedRealtimeEndpoint({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<ManagedRealtimeEndpointResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Partially update a managed realtime endpoint.
   * @returns ManagedRealtimeEndpointResponse The updated managed realtime endpoint.
   * @throws ApiError
   */
  public static updateManagedRealtimeEndpoint({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: UpdateManagedRealtimeEndpointRequest,
  }): CancelablePromise<ManagedRealtimeEndpointResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Delete a managed realtime endpoint.
   * @returns void
   * @throws ApiError
   */
  public static deleteManagedRealtimeEndpoint({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List live connections for a managed realtime endpoint.
   * Returns a bounded point-in-time inventory, optionally filtered by
   * channel or authenticated principal. Results are ordered by connection
   * ID; use the opaque `next_cursor` value to continue a truncated page.
   * `partial` is true when one or more active realtime nodes could not be
   * queried; healthy node results remain in the response.
   *
   * @returns ManagedRealtimeConnectionListResponse The live connection inventory.
   * @throws ApiError
   */
  public static listManagedRealtimeConnections({
    slug,
    id,
    limit = 100,
    channel,
    principal,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Maximum number of live connections to return.
     */
    limit?: number,
    /**
     * Return only connections subscribed to this channel.
     */
    channel?: string,
    /**
     * Return only connections authenticated as this principal.
     */
    principal?: string,
    /**
     * Opaque next_cursor returned by a previous inventory response.
     */
    cursor?: string,
  }): CancelablePromise<ManagedRealtimeConnectionListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'limit': limit,
        'channel': channel,
        'principal': principal,
        'cursor': cursor,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
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
   * Close a bounded or explicitly all-matching set of live managed realtime connections.
   * Selects connections from a point-in-time fleet inventory by channel,
   * principal, or explicit connection IDs. `dry_run` returns the selected
   * connections without closing them. A non-dry-run request fails with
   * `409 conflict` when the inventory is partial unless `allow_partial`
   * is true; this prevents an unavailable node from making a drain look
   * complete. Set `all` to select every matching connection, up to the
   * server safety cap of 10,000; `all` cannot be combined with
   * `connection_ids`; any supplied `limit` is ignored in all mode, and
   * the request returns `409 conflict` when the cap would be exceeded.
   *
   * @returns ManagedRealtimeDrainResponse The durable drain operation was accepted for background execution.
   * @throws ApiError
   */
  public static drainManagedRealtimeConnections({
    slug,
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: ManagedRealtimeDrainRequest,
    /**
     * Replays the accepted operation for 24 hours when the request is retried.
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedRealtimeDrainResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/drain',
      path: {
        'slug': slug,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Get the durable result of a realtime connection drain.
   * Returns a previously recorded drain result scoped to the application and endpoint.
   * @returns ManagedRealtimeDrainResponse The persisted drain result.
   * @throws ApiError
   */
  public static getManagedRealtimeDrainOperation({
    slug,
    id,
    drainId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Durable identifier returned by the drain request.
     */
    drainId: string,
  }): CancelablePromise<ManagedRealtimeDrainResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/drain/{drain_id}',
      path: {
        'slug': slug,
        'id': id,
        'drain_id': drainId,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
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
   * Rotate a static bearer credential with a bounded overlap window.
   * Promotes the supplied credential to current and accepts the previous
   * credential until previous_token_expires_at. Credentials are sealed at
   * rest and never returned in plaintext.
   *
   * @returns RotateManagedRealtimeAuthResponse Static bearer credential rotation started.
   * @throws ApiError
   */
  public static rotateManagedRealtimeAuth({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: RotateManagedRealtimeAuthRequest,
  }): CancelablePromise<RotateManagedRealtimeAuthResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/auth/rotate',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Revoke the predecessor static bearer credential immediately.
   * @returns FinalizeManagedRealtimeAuthResponse Static bearer credential rotation finalized.
   * @throws ApiError
   */
  public static finalizeManagedRealtimeAuth({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<FinalizeManagedRealtimeAuthResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/auth/rotate/finalize',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Send a message to one live managed realtime connection.
   * @returns any Message accepted by the connection owner.
   * @throws ApiError
   */
  public static sendManagedRealtimeConnection({
    slug,
    id,
    connectionId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Live connection identifier returned by the callback event.
     */
    connectionId: string,
    requestBody: ManagedRealtimeMessageRequest,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/send',
      path: {
        'slug': slug,
        'id': id,
        'connection_id': connectionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
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
   * Close one live managed realtime connection.
   * @returns void
   * @throws ApiError
   */
  public static closeManagedRealtimeConnection({
    slug,
    id,
    connectionId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Live socket identifier to close on its owning realtime node.
     */
    connectionId: string,
    requestBody?: ManagedRealtimeCloseRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/close',
      path: {
        'slug': slug,
        'id': id,
        'connection_id': connectionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Subscribe one live connection to a channel.
   * @returns void
   * @throws ApiError
   */
  public static subscribeManagedRealtimeConnection({
    slug,
    id,
    connectionId,
    channel,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Live connection identifier returned in realtime callback events.
     */
    connectionId: string,
    /**
     * Channel to add or remove for the live connection.
     */
    channel: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/subscriptions/{channel}',
      path: {
        'slug': slug,
        'id': id,
        'connection_id': connectionId,
        'channel': channel,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Remove one live connection from a channel.
   * @returns void
   * @throws ApiError
   */
  public static unsubscribeManagedRealtimeConnection({
    slug,
    id,
    connectionId,
    channel,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Live connection identifier returned in realtime callback events.
     */
    connectionId: string,
    /**
     * Channel to add or remove for the live connection.
     */
    channel: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/subscriptions/{channel}',
      path: {
        'slug': slug,
        'id': id,
        'connection_id': connectionId,
        'channel': channel,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Publish a message to live or resumable channel subscribers.
   * delivery=live (the default) fans out to live raw-frame subscribers. delivery=retained is preview-only and requires FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1 on apid; it accepts at most 4096 decoded bytes and requires an Idempotency-Key. Retained delivery commits the message to the ordered channel log before fan-out and returns its sequence. V2 subscribers read the committed log in sequence; a best-effort wake reduces latency while bounded polling recovers missed wakes. The retained log remains authoritative if live fan-out is incomplete. The resume protocol separately requires FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1 on realtimed.
   * A supplied Idempotency-Key binds the publish to delivery mode, decoded payload and binary flag for 24 hours. Replays return the original response and do not retry recipients that missed a partial publish. Reusing a key with a different mode or payload returns 409. An in-flight or uncertain reservation also returns 409 and is not run again while the key is active. Queue admission does not confirm client receipt.
   * @returns ManagedRealtimePublishResponse Per-recipient queue outcomes; retained publishes also include the committed channel sequence.
   * @throws ApiError
   */
  public static publishManagedRealtimeChannel({
    slug,
    id,
    channel,
    requestBody,
    delivery = 'live',
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Channel that receives the published message.
     */
    channel: string,
    requestBody: ManagedRealtimeMessageRequest,
    /**
     * Use retained to commit a sequenced message before live fan-out; this is preview-only and requires an Idempotency-Key.
     */
    delivery?: 'live' | 'retained',
    /**
     * Stable printable-ASCII key for retrying this publish; reuse it only with the same delivery mode, payload, and binary flag.
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedRealtimePublishResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'delivery': delivery,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Append one ordered message to retained channel history.
   * Disabled by default; an operator must set FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1 on apid. Retained writes reach only opt-in v2 WebSocket subscribers when the separate realtimed resume preview is enabled. The sequence is committed before the response; idempotency applies while the message remains retained. At most 32 channels and 1024 messages per channel are retained per endpoint, for up to 24 hours.
   * @returns ManagedRealtimeRetainedMessageResponse Message committed to the channel log.
   * @throws ApiError
   */
  public static appendManagedRealtimeRetainedMessage({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Endpoint-scoped channel.
     */
    channel: string,
    requestBody: ManagedRealtimeRetainedMessageRequest,
  }): CancelablePromise<ManagedRealtimeRetainedMessageResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Read a page of retained channel history after a sequence.
   * Disabled by default; an operator must set FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1 on apid. A cursor older than retained history returns 410 with code history_unavailable. This management API is not a WebSocket resume protocol.
   * @returns ManagedRealtimeRetainedHistoryResponse A consistent page of retained messages.
   * @throws ApiError
   */
  public static readManagedRealtimeRetainedMessages({
    slug,
    id,
    channel,
    after,
    limit = 100,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Endpoint-scoped channel.
     */
    channel: string,
    /**
     * Last processed sequence; zero starts at the first retained message.
     */
    after: number,
    /**
     * Maximum number of retained messages to return.
     */
    limit?: number,
  }): CancelablePromise<ManagedRealtimeRetainedHistoryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
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
   * Send to a verified principal, optionally retaining a notification with push fallback.
   * Retained sends require the retained-history preview gate and a stable message ID. Notification category is part of deduplication and defaults to notifications. Push preferences are evaluated before provider delivery; ACKs cancel queued fallback. Live sends can request receipts instead of retention.
   * @returns any Accepted; durable reports storage acceptance and queued reports live connection queueing.
   * @throws ApiError
   */
  public static sendManagedRealtimePrincipal({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: {
      principal: string;
      /**
       * Standard base64; at most 4096 decoded bytes.
       */
      data_base64: string;
      binary?: boolean;
      delivery?: 'live' | 'retained';
      message_id?: string;
      request_receipt?: boolean;
      /**
       * Requires retained delivery; zero disables fallback.
       */
      fallback_after_seconds?: number;
      /**
       * Stable conversation, job or project key; requires a fallback.
       */
      notification_group_key?: string;
      /**
       * Earliest built-in push delivery time; RFC3339 with timezone, at most 48 hours ahead. Requires fallback; explicit TTL must extend past this instant.
       */
      notification_not_before?: string;
      /**
       * Requires fallback. Newer queued alerts supersede older alerts for the same recipient, device, category, priority and collapse key.
       */
      notification_collapse_key?: string;
      /**
       * Push lifetime in seconds from publication. Positive values require fallback and must exceed its deadline. Zero or omission keeps default expiration.
       */
      notification_ttl_seconds?: number;
      /**
       * Defaults to normal when omitted; requires retained fallback. Urgent bypass requires user opt-in.
       */
      notification_priority?: 'low' | 'normal' | 'urgent';
      /**
       * Display name for summaries; requires a group key.
       */
      notification_group_label?: string;
      /**
       * Requires a nonzero fallback deadline; omitted values use notifications for push preferences.
       */
      notification_category?: string;
    },
  }): CancelablePromise<{
    message_id?: string;
    sequence?: number;
    durable?: boolean;
    fallback_deadline?: string;
    recipients?: number;
    queued?: number;
    unsupported?: number;
    queue_full?: number;
    failed?: number;
    nodes_queried?: number;
    nodes_unavailable?: number;
    partial?: boolean;
    receipt_requested?: boolean;
    receipt_status?: string;
    acknowledged?: number;
    pending?: number;
    timed_out?: number;
  }> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals:send',
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
   * Read a user notification preference document.
   * Requires the retained-history preview gate. Defaults enable all categories and devices. Preferences apply to built-in push; inbox retention and ACKs remain independent. Quiet-hour deferrals do not consume attempts. At most 256 preference documents per endpoint.
   * @returns RealtimeNotificationPreferences Current preference document.
   * @throws ApiError
   */
  public static getManagedRealtimeNotificationPreferences({
    slug,
    id,
    principal,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    principal: string,
  }): CancelablePromise<RealtimeNotificationPreferences> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/preferences',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
      },
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
   * Replace notification preferences across user devices.
   * Requires the retained-history preview gate. Defaults enable all categories and devices. Preferences apply to built-in push; inbox retention and ACKs remain independent. Quiet-hour deferrals do not consume attempts. At most 256 preference documents per endpoint.
   * @returns RealtimeNotificationPreferences Current preference document.
   * @throws ApiError
   */
  public static putManagedRealtimeNotificationPreferences({
    slug,
    id,
    principal,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    principal: string,
    requestBody: RealtimeNotificationPreferences,
  }): CancelablePromise<RealtimeNotificationPreferences> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/preferences',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
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
   * List realtime push providers.
   * Requires the retained-history preview gate. Credentials and tokens are never returned. Delivery history includes the latest 100 records for the principal; sent means provider acceptance, not user delivery.
   * @returns ManagedRealtimePushProvider Push metadata.
   * @throws ApiError
   */
  public static listManagedRealtimePushProviders({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<Array<ManagedRealtimePushProvider>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/providers',
      path: {
        'slug': slug,
        'id': id,
      },
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
   * List realtime push devices.
   * Requires the retained-history preview gate. Credentials and tokens are never returned. Delivery history includes the latest 100 records for the principal; sent means provider acceptance, not user delivery.
   * @returns ManagedRealtimePushDevice Push metadata.
   * @throws ApiError
   */
  public static listManagedRealtimePushDevices({
    slug,
    id,
    principal,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    principal: string,
  }): CancelablePromise<Array<ManagedRealtimePushDevice>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/devices',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
      },
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
   * List realtime push deliveries.
   * Requires the retained-history preview gate. Credentials and tokens are never returned. Delivery history includes the latest 100 records for the principal; sent means provider acceptance, not user delivery.
   * @returns ManagedRealtimePushDelivery Push metadata.
   * @throws ApiError
   */
  public static listManagedRealtimePushDeliveries({
    slug,
    id,
    principal,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    principal: string,
  }): CancelablePromise<Array<ManagedRealtimePushDelivery>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/deliveries',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
      },
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
   * Configure or disable a push provider.
   * Provider credentials are sealed at rest. Disabling cancels queued deliveries; re-enabling does not replay them. The retained-history preview gate is required.
   * @returns any Provider configured; credentials are omitted.
   * @throws ApiError
   */
  public static configureManagedRealtimePushProvider({
    slug,
    id,
    provider,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    provider: 'fcm' | 'apns' | 'webpush',
    requestBody: ManagedRealtimePushProviderRequest,
  }): CancelablePromise<{
    provider: 'fcm' | 'apns' | 'webpush';
    enabled: boolean;
  }> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/providers/{provider}',
      path: {
        'slug': slug,
        'id': id,
        'provider': provider,
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
   * Register or rotate a device push token.
   * Requires the retained-history preview gate. Registration is limited to 16 devices per principal and 1024 per endpoint. A provider must be enabled before registration. Identical registrations preserve pending deliveries; changed tokens cancel old work.
   * @returns any Registration updated; token is omitted.
   * @throws ApiError
   */
  public static registerManagedRealtimePushDevice({
    slug,
    id,
    principal,
    device,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    principal: string,
    device: string,
    requestBody: ManagedRealtimePushRegistration,
  }): CancelablePromise<{
    ok: boolean;
  }> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/devices/{device}',
      path: {
        'slug': slug,
        'id': id,
        'device': device,
      },
      query: {
        'principal': principal,
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
   * Remove a push registration and cancel its queued work.
   * Requires the retained-history preview gate. Registration is limited to 16 devices per principal and 1024 per endpoint. A provider must be enabled before registration. Identical registrations preserve pending deliveries; changed tokens cancel old work.
   * @returns any Registration updated; token is omitted.
   * @throws ApiError
   */
  public static unregisterManagedRealtimePushDevice({
    slug,
    id,
    principal,
    device,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    principal: string,
    device: string,
  }): CancelablePromise<{
    ok: boolean;
  }> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/devices/{device}',
      path: {
        'slug': slug,
        'id': id,
        'device': device,
      },
      query: {
        'principal': principal,
      },
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
}
