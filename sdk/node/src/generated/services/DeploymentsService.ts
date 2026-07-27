/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateDeploymentRequest } from '../models/CreateDeploymentRequest.js';
import type { DeploymentListResponse } from '../models/DeploymentListResponse.js';
import type { DeploymentResponse } from '../models/DeploymentResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DeploymentsService {
  /**
   * Create a deployment.
   * Two content-types are accepted:
   * - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
   * - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
   * Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
   *
   * @returns DeploymentResponse The deployment whose build has been accepted and queued.
   * @throws ApiError
   */
  public static createDeployment({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Either a prebuilt OCI image reference (`application/json`) or a source tarball upload (`multipart/form-data`). See the operation description for plan size caps.
     */
    requestBody: CreateDeploymentRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/deployments',
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
        403: `code: image_egress_denied — registry is in RFC1918 / IMDS / link-local, or blocked egress range.`,
        413: `code: source_too_large`,
        422: `code: deploy_failed | image_not_found | image_manifest_invalid | build_oom | build_timeout`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Roll back to the previous deployment.
   * @returns DeploymentResponse The deployment that was created by rolling back to the previous version.
   * @throws ApiError
   */
  public static rollbackApp({
    slug,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/rollback',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        409: `code: no_rollback_target — there is no superseded deployment to roll back to.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List deployments across all apps on the account.
   * Paged backwards (newest first). `next_before` is an opaque cursor
   * (RFC3339Nano of the `created_at`); pass it on the next request to
   * page backwards. Empty `next_before` means end of list.
   *
   * @returns DeploymentListResponse A paginated list of deployments.
   * @throws ApiError
   */
  public static listDeployments({
    limit = 50,
    before,
  }: {
    /**
     * Page size (1–200, default 50).
     */
    limit?: number,
    /**
     * RFC3339Nano cursor from a previous response's `next_before`.
     */
    before?: string,
  }): CancelablePromise<DeploymentListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments',
      query: {
        'limit': limit,
        'before': before,
      },
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
   * Fetch one deployment.
   * @returns DeploymentResponse The deployment.
   * @throws ApiError
   */
  public static getDeployment({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}',
      path: {
        'id': id,
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
   * Stream build logs (SSE).
   * Server-Sent Events stream of build logs. `follow=1` holds the
   * connection open until the build completes.
   *
   * @returns any A text/event-stream of build log entries, terminated by an empty SSE frame when the build finishes.
   * @throws ApiError
   */
  public static streamDeploymentLogs({
    id,
    beforeSeq,
    limit,
    follow = 0,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Stream cursor — return only log entries with seq strictly less than this value.
     */
    beforeSeq?: number,
    /**
     * Maximum number of log entries to return in the initial burst before streaming.
     */
    limit?: number,
    /**
     * If 1, hold the connection open and stream new build log entries as they arrive.
     */
    follow?: 0 | 1,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/logs',
      path: {
        'id': id,
      },
      query: {
        'before_seq': beforeSeq,
        'limit': limit,
        'follow': follow,
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
