/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DelayedTaskRequest } from '../models/DelayedTaskRequest.js';
import type { DelayedTaskResponse } from '../models/DelayedTaskResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DelayedTasksService {
  /**
   * Schedule a delayed task to fire at a future time.
   * Cap-checked against the plan's MaxDelayedTasksPerApp (Hobby 5,
   * Pro 50, Scale 1_000_000). The drain re-checks at dispatch.
   *
   * @returns DelayedTaskResponse The newly-scheduled task.
   * @throws ApiError
   */
  public static delayedTaskCreate({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: DelayedTaskRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<DelayedTaskResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/delayed-tasks',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: invalid_scheduled_at — delayed-task scheduled_at is in the past.`,
        403: `code: plan_delayed_tasks_cap — per-app delayed-task count at MaxDelayedTasksPerApp.`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Read a single delayed task by id.
   * @returns DelayedTaskResponse The task.
   * @throws ApiError
   */
  public static delayedTaskGet({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<DelayedTaskResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/delayed-tasks/{id}',
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
   * Cancel a pending delayed task.
   * Idempotent: a re-cancel (or a cancel of an already-fired row)
   * is a 204. The drain ignores cancelled rows at dispatch.
   *
   * @returns void
   * @throws ApiError
   */
  public static delayedTaskCancel({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/delayed-tasks/{id}',
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
