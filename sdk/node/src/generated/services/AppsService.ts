/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppResponse } from '../models/AppResponse.js';
import type { CreateAppRequest } from '../models/CreateAppRequest.js';
import type { RenameAppRequest } from '../models/RenameAppRequest.js';
import type { UpdateAppRequest } from '../models/UpdateAppRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class AppsService {
  /**
   * List apps on the account.
   * @returns AppResponse Apps on the account.
   * @throws ApiError
   */
  public static listApps(): CancelablePromise<Array<AppResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps',
      errors: {
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Create an app.
   * @returns AppResponse The new app.
   * @throws ApiError
   */
  public static createApp({
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App creation payload (slug, type, runtime, RAM, …). See CreateAppRequest schema.
     */
    requestBody: CreateAppRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | app_layer_too_large | image_egress_denied`,
        409: `code: conflict`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Fetch one app.
   * @returns AppResponse The app.
   * @throws ApiError
   */
  public static getApp({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Partial-update an app.
   * @returns AppResponse The updated app.
   * @throws ApiError
   */
  public static updateApp({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Patch payload — every field is optional; omitted fields are unchanged. See UpdateAppRequest.
     */
    requestBody: UpdateAppRequest,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        422: `code: invalid_min_instances — must be in [0, plan max_concurrency].`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Delete an app.
   * @returns void
   * @throws ApiError
   */
  public static deleteApp({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Manually park all running instances.
   * @returns void
   * @throws ApiError
   */
  public static parkApp({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/park',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Manually wake an instance.
   * @returns void
   * @throws ApiError
   */
  public static wakeApp({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/wake',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `code: plan_limit_concurrency`,
        503: `code: capacity_unavailable — no host headroom (alerting; should be near-impossible).`,
      },
    });
  }
  /**
   * Rename an app.
   * @returns AppResponse The renamed app.
   * @throws ApiError
   */
  public static renameApp({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Rename payload — the new slug. See RenameAppRequest.
     */
    requestBody: RenameAppRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/rename',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        409: `code: app_rename_failed — slug taken by another live app, or DB unique violation.`,
      },
    });
  }
  /**
   * Stream app logs (SSE).
   * Server-Sent Events stream of instance logs. NOTE: this endpoint is
   * currently mounted behind `s.authLimited` and is documented here for
   * reference; the dashboard and CLI also consume it directly.
   *
   * @returns any A text/event-stream of structured log lines, terminated by an empty SSE frame when the connection closes.
   * @throws ApiError
   */
  public static streamAppLogs({
    slug,
    follow = 0,
    grep,
    since,
    level,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * If 1, hold the connection open and stream new entries.
     */
    follow?: 0 | 1,
    /**
     * Substring filter (case-sensitive). Matches the rendered log line.
     * Empty (default) means no grep filter. Issue #309.
     *
     */
    grep?: string,
    /**
     * RFC 3339 lower bound on the log timestamp. Lines whose
     * `written_at` is earlier are dropped. Empty (default) means
     * no lower bound.
     *
     */
    since?: string,
    /**
     * Minimum level (case-insensitive). Lines below this level
     * are dropped. Empty (default) means no level filter. Issue #309.
     *
     */
    level?: 'info' | 'warn' | 'error',
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/logs',
      path: {
        'slug': slug,
      },
      query: {
        'follow': follow,
        'grep': grep,
        'since': since,
        'level': level,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
