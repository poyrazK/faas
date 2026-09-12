/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AdminStatusEventCreateRequest } from '../models/AdminStatusEventCreateRequest.js';
import type { AdminStatusEventUpdateRequest } from '../models/AdminStatusEventUpdateRequest.js';
import type { PublicStatusEvent } from '../models/PublicStatusEvent.js';
import type { PublicStatusOverview } from '../models/PublicStatusOverview.js';
import type { StatusPage } from '../models/StatusPage.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class StatusService {
  /**
   * Read the current public platform status.
   * Unauthenticated single-region snapshot. Includes five public
   * capabilities, exactly 30 UTC daily observations per capability,
   * current error-budget indicators, active events, maintenance in the
   * next 30 days, and up to 20 resolved incidents from the last 90 days.
   * Data older than 90 seconds is marked stale; missing telemetry is never
   * represented as operational.
   *
   * @returns PublicStatusOverview Current status snapshot, including degraded-source responses.
   * @throws ApiError
   */
  public static getPublicStatus(): CancelablePromise<PublicStatusOverview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/status',
    });
  }
  /**
   * Read one public incident or maintenance timeline.
   * @returns PublicStatusEvent Public event and chronological update timeline.
   * @throws ApiError
   */
  public static getPublicStatusIncident({
    publicId,
  }: {
    /**
     * Stable public UUID used by status permalinks.
     */
    publicId: string,
  }): CancelablePromise<PublicStatusEvent> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/status/incidents/{public_id}',
      path: {
        'public_id': publicId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read the backwards-compatible status indicator projection.
   * Always returns valid JSON, including when the evaluator source is degraded.
   * @returns StatusPage Legacy status projection.
   * @throws ApiError
   */
  public static getLegacyStatusSlo(): CancelablePromise<StatusPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/status/slo.json',
    });
  }
  /**
   * List public status events (admin-only).
   * Requires admin scope, operator allowlist membership, and MFA.
   * @returns PublicStatusEvent Status events, newest update first.
   * @throws ApiError
   */
  public static listAdminStatusEvents({
    faasSid,
    kind,
    active = false,
  }: {
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
    /**
     * Restrict results to incidents or maintenance.
     */
    kind?: 'incident' | 'maintenance',
    /**
     * Return only events that have not reached a terminal lifecycle state.
     */
    active?: boolean,
  }): CancelablePromise<Array<PublicStatusEvent>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/status/incidents',
      cookies: {
        'faas_sid': faasSid,
      },
      query: {
        'kind': kind,
        'active': active,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Publish a public incident or maintenance event (admin-only).
   * Requires admin scope, operator allowlist membership, MFA, recent step-up authentication, and an Idempotency-Key.
   * @returns PublicStatusEvent Published event. Replays return the same public UUID.
   * @throws ApiError
   */
  public static createAdminStatusEvent({
    idempotencyKey,
    requestBody,
    faasSid,
  }: {
    /**
     * Stable caller-generated key used to replay this create safely.
     */
    idempotencyKey: string,
    requestBody: AdminStatusEventCreateRequest,
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<PublicStatusEvent> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/status/incidents',
      cookies: {
        'faas_sid': faasSid,
      },
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Append a public timeline update and lifecycle transition (admin-only).
   * Updates are append-only. Terminal incidents and maintenance cannot reopen.
   * @returns PublicStatusEvent Updated event with its complete chronological timeline.
   * @throws ApiError
   */
  public static updateAdminStatusEvent({
    publicId,
    idempotencyKey,
    requestBody,
    faasSid,
  }: {
    /**
     * Stable public UUID used by status permalinks.
     */
    publicId: string,
    /**
     * Stable caller-generated key used to replay this timeline update safely.
     */
    idempotencyKey: string,
    requestBody: AdminStatusEventUpdateRequest,
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<PublicStatusEvent> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/status/incidents/{public_id}/updates',
      path: {
        'public_id': publicId,
      },
      cookies: {
        'faas_sid': faasSid,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
