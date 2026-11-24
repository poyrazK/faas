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
import type { ManagedRealtimeMessageRequest } from '../models/ManagedRealtimeMessageRequest.js';
import type { ManagedRealtimePrincipalMessageRequest } from '../models/ManagedRealtimePrincipalMessageRequest.js';
import type { ManagedRealtimePrincipalReceiptResponse } from '../models/ManagedRealtimePrincipalReceiptResponse.js';
import type { ManagedRealtimePrincipalSendResponse } from '../models/ManagedRealtimePrincipalSendResponse.js';
import type { ManagedRealtimePublishResponse } from '../models/ManagedRealtimePublishResponse.js';
import type { RotateManagedRealtimeAuthRequest } from '../models/RotateManagedRealtimeAuthRequest.js';
import type { RotateManagedRealtimeAuthResponse } from '../models/RotateManagedRealtimeAuthResponse.js';
import type { UpdateManagedRealtimeEndpointRequest } from '../models/UpdateManagedRealtimeEndpointRequest.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class RealtimeService {
  protected static readonly clientConfig?: typeof OpenAPI;
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Create a managed realtime endpoint.
   * Registers the callback contract used by the realtime service for connect,
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session.`,
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session.`,
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session.`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Publish a message to live or resumable channel subscribers.
   * delivery=live (the default) fans out to live raw-frame subscribers. delivery=retained is part of the realtime preview, released per account; it accepts at most 4096 decoded bytes and requires an Idempotency-Key. Retained delivery commits the message to the ordered channel log before fan-out and returns its sequence. Resumable subscribers read the committed log in sequence; a best-effort wake reduces latency while bounded polling recovers missed wakes. The retained log remains authoritative if live fan-out is incomplete.
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
    delivery?: 'live',
    /**
     * Stable printable-ASCII key for retrying this publish; reuse it only with the same delivery mode, payload, and binary flag.
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedRealtimePublishResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
   * @returns ManagedRealtimePrincipalSendResponse Accepted; durable reports storage acceptance and queued reports live connection queueing.
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
    requestBody: ManagedRealtimePrincipalMessageRequest,
  }): CancelablePromise<ManagedRealtimePrincipalSendResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Get principal message receipt state
   * @returns ManagedRealtimePrincipalReceiptResponse Get principal message receipt state result.
   * @throws ApiError
   */
  public static getManagedRealtimePrincipalReceipt({
    slug,
    id,
    messageId,
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
  }): CancelablePromise<ManagedRealtimePrincipalReceiptResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals/messages/{message_id}/receipt',
      path: {
        'slug': slug,
        'id': id,
        'message_id': messageId,
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
      },
    });
  }
}
