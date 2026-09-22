/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DelayedTaskRequest } from '../models/DelayedTaskRequest.js';
import type { DelayedTaskResponse } from '../models/DelayedTaskResponse.js';
import type { ListDelayedTasksResponse } from '../models/ListDelayedTasksResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DelayedTasksService {
  /**
   * List delayed tasks for an app.
   * Newest-first, cursor-paginated delayed-task history.
   * @returns ListDelayedTasksResponse One page of delayed tasks.
   * @throws ApiError
   */
  public static listDelayedTasks({
    slug,
    before,
    limit = 20,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Invocation id returned as next_before by a prior page.
     */
    before?: string,
    /**
     * Page size from 1 to 200; defaults to 20.
     */
    limit?: number,
  }): CancelablePromise<ListDelayedTasksResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/delayed-tasks',
      path: {
        'slug': slug,
      },
      query: {
        'before': before,
        'limit': limit,
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
   * Schedule a delayed task to fire at a future time.
   * Supply exactly one of `scheduled_at` or `delay_seconds`. Scheduling is
   * bounded to one year. Cap-checked against the plan's
   * MaxDelayedTasksPerApp (Hobby 5, Pro 50, Scale 1_000_000). The drain
   * re-checks at dispatch. Delivery is at least once; handlers should use
   * the invocation id to make side effects idempotent.
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
        400: `code: invalid_scheduled_at — the schedule is missing, ambiguous, in the past, or beyond the one-year horizon.`,
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
   * Atomically cancels a pending row. A re-cancel is idempotent.
   * Dispatching or terminal rows are left unchanged and their actual
   * state is returned, so clients never claim completed work was stopped.
   *
   * @returns DelayedTaskResponse The task with its authoritative resulting state.
   * @throws ApiError
   */
  public static delayedTaskCancel({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<DelayedTaskResponse> {
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
