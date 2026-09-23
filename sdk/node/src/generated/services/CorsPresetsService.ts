/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CorsPresetListResponse } from '../models/CorsPresetListResponse.js';
import type { CorsPresetResponse } from '../models/CorsPresetResponse.js';
import type { CreateCorsPresetRequest } from '../models/CreateCorsPresetRequest.js';
import type { UpdateCorsPresetRequest } from '../models/UpdateCorsPresetRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class CorsPresetsService {
  /**
   * List CORS presets visible to the calling account.
   * Lists every cors_presets row the account owns — both
   * account-wide (app_id IS NULL) and app-scoped (app_id
   * is set). The optional `app_id` query parameter filters
   * to a single app's scoped presets; absent = union of
   * account-wide + every app-scoped row. No pagination —
   * the per-account quota caps the row count (see the
   * plan_cors_preset_quota_reached error code).
   *
   * @returns CorsPresetListResponse The list of cors presets.
   * @throws ApiError
   */
  public static listCorsPresets({
    appId,
  }: {
    /**
     * Optional app filter. Absent = every preset on the
     * account. Set = only the app-scoped presets for that
     * app. Cross-tenant IDs collapse to 404.
     *
     */
    appId?: string,
  }): CancelablePromise<CorsPresetListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/cors-presets',
      query: {
        'app_id': appId,
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
   * Create a CORS preset.
   * Creates a new cors_presets row owned by the caller's
   * account. AppID is optional (null = account-wide; UUID =
   * app-scoped). The body validation mirrors the storage-
   * side CHECK constraints: name 1..64, max_age 0..86400,
   * at-least-one allow_origin + allow_method. The
   * *+credentials footgun (ADR-091 D12) returns 422
   * cors_wildcard_with_credentials when the create body
   * combines AllowCredentials: true with AllowOrigins:
   * ["*"].
   *
   * Pre-loadApp gates fire in this order: 402
   * plan_cors_preset_not_allowed on the Free-tier cap-0 →
   * 422 cors_preset_invalid on the body shape → 404
   * cors_preset_app_not_found on a cross-tenant app_id →
   * 403 plan_cors_preset_quota_reached on the per-account
   * / per-app cap → 409 cors_preset_name_conflict on a
   * duplicate (account_id, COALESCE(app_id, '00..00'),
   * name) tuple.
   *
   * @returns CorsPresetResponse The created cors preset.
   * @throws ApiError
   */
  public static createCorsPreset({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateCorsPresetRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<CorsPresetResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/cors-presets',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        402: `code: cors_preset_invalid | cors_wildcard_with_credentials |
        cors_preset_update_requires_field | cors_preset_name_conflict |
        plan_cors_preset_not_allowed | plan_cors_preset_quota_reached
        (issue #975 #4 PR-B / ADR-129). The body validates the
        same shape as the storage-side CHECK constraints; the
        wire-level codes are 422 for grammar violations and 402
        for plan gates.
        `,
        403: `code: cors_preset_invalid | cors_wildcard_with_credentials |
        cors_preset_update_requires_field | cors_preset_name_conflict |
        plan_cors_preset_not_allowed | plan_cors_preset_quota_reached
        (issue #975 #4 PR-B / ADR-129). The body validates the
        same shape as the storage-side CHECK constraints; the
        wire-level codes are 422 for grammar violations and 402
        for plan gates.
        `,
        404: `code: not_found`,
        409: `code: cors_preset_invalid | cors_wildcard_with_credentials |
        cors_preset_update_requires_field | cors_preset_name_conflict |
        plan_cors_preset_not_allowed | plan_cors_preset_quota_reached
        (issue #975 #4 PR-B / ADR-129). The body validates the
        same shape as the storage-side CHECK constraints; the
        wire-level codes are 422 for grammar violations and 402
        for plan gates.
        `,
        422: `code: cors_preset_invalid | cors_wildcard_with_credentials |
        cors_preset_update_requires_field | cors_preset_name_conflict |
        plan_cors_preset_not_allowed | plan_cors_preset_quota_reached
        (issue #975 #4 PR-B / ADR-129). The body validates the
        same shape as the storage-side CHECK constraints; the
        wire-level codes are 422 for grammar violations and 402
        for plan gates.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Get a single CORS preset by id.
   * Returns the canonical cors_presets row. Cross-tenant
   * IDs collapse to 404 (no slug-leak). The response is
   * the same shape as POST /v1/cors-presets.
   *
   * @returns CorsPresetResponse The cors preset.
   * @throws ApiError
   */
  public static getCorsPreset({
    id,
  }: {
    /**
     * Preset UUID.
     */
    id: string,
  }): CancelablePromise<CorsPresetResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/cors-presets/{id}',
      path: {
        'id': id,
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
   * Partial-update a CORS preset.
   * Applies the partial-update (nil-skip convention) to
   * the cors_presets row. The PATCH body must include at
   * least one field (an empty body returns 422
   * cors_preset_update_requires_field). The wire-level
   * Validate enforces the same partial grammar as Create
   * (CorsOriginPattern on allow_origins if provided,
   * non-empty allow_methods if provided, max_age bound
   * 0..86400). The handler additionally re-validates the
   * post-update merged shape against the *+credentials
   * footgun (a PATCH that flips AllowCredentials to true
   * while leaving AllowOrigins=["*"] is rejected).
   *
   * The pgstore trigger fires pg_notify
   * ('cors_preset_changed', account_id) AFTER the UPDATE
   * commits so the gatewayd-internal listener reloads
   * the affected account's preset overlay (ADR-129 D4).
   *
   * @returns CorsPresetResponse The updated cors preset.
   * @throws ApiError
   */
  public static patchCorsPreset({
    id,
    requestBody,
  }: {
    /**
     * Preset UUID.
     */
    id: string,
    requestBody: UpdateCorsPresetRequest,
  }): CancelablePromise<CorsPresetResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/cors-presets/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: cors_preset_invalid | cors_wildcard_with_credentials |
        cors_preset_update_requires_field | cors_preset_name_conflict |
        plan_cors_preset_not_allowed | plan_cors_preset_quota_reached
        (issue #975 #4 PR-B / ADR-129). The body validates the
        same shape as the storage-side CHECK constraints; the
        wire-level codes are 422 for grammar violations and 402
        for plan gates.
        `,
        422: `code: cors_preset_invalid | cors_wildcard_with_credentials |
        cors_preset_update_requires_field | cors_preset_name_conflict |
        plan_cors_preset_not_allowed | plan_cors_preset_quota_reached
        (issue #975 #4 PR-B / ADR-129). The body validates the
        same shape as the storage-side CHECK constraints; the
        wire-level codes are 422 for grammar violations and 402
        for plan gates.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Delete a CORS preset.
   * Removes the cors_presets row. The FK ON DELETE SET
   * NULL on edge_rules.cors_preset_id clears every
   * referencing rule's FK atomically with the preset's
   * deletion; the gatewayd-internal compile path fails
   * closed (MergeCorsPresetIntoRule returns ErrNotFound)
   * until the customer wires a new preset or inlines
   * fallback values. The pgstore trigger fires
   * pg_notify('cors_preset_changed', account_id) AFTER
   * the DELETE commits.
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteCorsPreset({
    id,
  }: {
    /**
     * Preset UUID.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/cors-presets/{id}',
      path: {
        'id': id,
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
}
