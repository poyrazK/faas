/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AsyncInvokeResponse } from '../models/AsyncInvokeResponse.js';
import type { Invocation } from '../models/Invocation.js';
import type { InvokeRequest } from '../models/InvokeRequest.js';
import type { InvokeResponse } from '../models/InvokeResponse.js';
import type { ListInvocationsResponse } from '../models/ListInvocationsResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class InvocationsService {
  /**
   * Sync-invoke an app; long-poll for the result.
   * Enqueues an invocation row and waits for the drain to drive it
   * to a terminal state. Server-side cap is 30s on paid plans, 5s
   * on Free. Returns 504 (long_poll_timeout) when the cap elapses;
   * the customer can immediately re-call /v1/invocations/{id}
   * to pick up the eventual result.
   *
   * @returns InvokeResponse The completed invocation.
   * @throws ApiError
   */
  public static invokeApp({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: InvokeRequest,
  }): CancelablePromise<InvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/invoke',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
        504: `code: long_poll_timeout — server-side long-poll budget elapsed without a terminal row.`,
      },
    });
  }
  /**
   * Async-invoke an app; returns id + status URL.
   * Enqueues an invocation row and returns immediately with the
   * id. The customer polls /v1/invocations/{id} (or uses the
   * dashboard SSE) for the eventual row state.
   *
   * @returns AsyncInvokeResponse The enqueued invocation.
   * @throws ApiError
   */
  public static invokeAppAsync({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: InvokeRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AsyncInvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/invoke/async',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List recent invocations on the account.
   * Paginated by `?before=<id>` (the LAST id of the returned slice).
   * Defaults to 20 per page; capped at 200.
   *
   * @returns ListInvocationsResponse The page.
   * @throws ApiError
   */
  public static listInvocations({
    before,
    limit = 20,
  }: {
    /**
     * Cursor — return rows whose id is strictly less than this. Omit for the most recent page.
     */
    before?: string,
    /**
     * Page size; 1-200, default 20.
     */
    limit?: number,
  }): CancelablePromise<ListInvocationsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/invocations',
      query: {
        'before': before,
        'limit': limit,
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
   * Read a single invocation by id (account-scoped).
   * @returns Invocation The row.
   * @throws ApiError
   */
  public static getInvocation({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<Invocation> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/invocations/{id}',
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
}
