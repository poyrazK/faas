/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AdminStatusEventCreateRequest } from '../models/AdminStatusEventCreateRequest.js';
import type { AdminStatusEventEditRequest } from '../models/AdminStatusEventEditRequest.js';
import type { AdminStatusEventUpdateRequest } from '../models/AdminStatusEventUpdateRequest.js';
import type { AdminStatusUpdateEditRequest } from '../models/AdminStatusUpdateEditRequest.js';
import type { PreflightReport } from '../models/PreflightReport.js';
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
   * Check whether a public GitHub repository would run on Gregale.
   * Unauthenticated static analysis of a public repository. Nothing is
   * built, deployed, or executed: the source is inspected for a detectable
   * framework and for hard disqualifiers from the container compatibility
   * contract.
   *
   * The verdict is deliberately conservative. `green` means the source
   * already satisfies the contract, `amber` means it runs once a declared
   * change is supplied, and `red` means a disqualifier applies and the app
   * cannot run as written.
   *
   * Only public github.com repositories are accepted. A private or missing
   * repository returns the same `preflight_repo_not_found` response so the
   * endpoint cannot be used to probe for private repositories.
   *
   * @returns PreflightReport The preflight verdict for the resolved commit.
   * @throws ApiError
   */
  public static getMigrationPreflight({
    source,
    ref,
  }: {
    /**
     * A github.com repository URL, or an `owner/repo` pair.
     */
    source: string,
    /**
     * Branch, tag, or commit SHA. A full commit SHA pins the verdict and
     * makes the result permanently reproducible.
     *
     */
    ref?: string,
  }): CancelablePromise<PreflightReport> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/preflight',
      query: {
        'source': source,
        'ref': ref,
      },
      errors: {
        404: `Repository not found, or private.`,
        422: `Not a public github.com repository, or the source is too large.`,
        429: `Too many checks from this client.`,
        503: `The upstream repository host is rate limiting Gregale.`,
      },
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Correct a published event title without changing its timeline order.
   * Requires an operator session. The event remains published and exposes edited_at; deletion is unsupported.
   * @returns PublicStatusEvent Corrected event with stable updated_at ordering and visible edited_at.
   * @throws ApiError
   */
  public static editAdminStatusEvent({
    publicId,
    requestBody,
    faasSid,
  }: {
    /**
     * Stable public UUID used by status permalinks.
     */
    publicId: string,
    requestBody: AdminStatusEventEditRequest,
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
      method: 'PATCH',
      url: '/v1/admin/status/incidents/{public_id}',
      path: {
        'public_id': publicId,
      },
      cookies: {
        'faas_sid': faasSid,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Correct a published timeline message without changing its posted time.
   * Requires an operator session. The entry remains in place and exposes edited_at; deletion is unsupported.
   * @returns PublicStatusEvent Corrected timeline with stable posted_at and visible edited_at.
   * @throws ApiError
   */
  public static editAdminStatusUpdate({
    publicId,
    updateId,
    requestBody,
    faasSid,
  }: {
    /**
     * Stable public UUID used by status permalinks.
     */
    publicId: string,
    /**
     * Public UUID of the timeline entry being corrected.
     */
    updateId: string,
    requestBody: AdminStatusUpdateEditRequest,
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
      method: 'PATCH',
      url: '/v1/admin/status/incidents/{public_id}/updates/{update_id}',
      path: {
        'public_id': publicId,
        'update_id': updateId,
      },
      cookies: {
        'faas_sid': faasSid,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
