/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AsyncInvokeResponse } from '../models/AsyncInvokeResponse.js';
import type { QueueDeadLetterResponse } from '../models/QueueDeadLetterResponse.js';
import type { QueuePeekResponse } from '../models/QueuePeekResponse.js';
import type { QueueReceiveResponse } from '../models/QueueReceiveResponse.js';
import type { QueueSendRequest } from '../models/QueueSendRequest.js';
import type { QueueSendResponse } from '../models/QueueSendResponse.js';
import type { QueueStateResponse } from '../models/QueueStateResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class QueuesService {
  /**
   * Enqueue a row on the per-app FIFO queue.
   * Cap-checked against the plan's MaxQueueDepth (Hobby 5, Pro 25,
   * Scale 100). The drain re-checks at dispatch tick.
   *
   * @returns QueueSendResponse The enqueued row.
   * @throws ApiError
   */
  public static queueSend({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: QueueSendRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<QueueSendResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/queues/send',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        403: `code: plan_queue_depth — per-app queue at the plan's MaxQueueDepth.`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Long-poll for the next dispatched row.
   * Returns 200 with the dequeued row, or 204 (no body) if the
   * server-side 30s budget elapses with no event. The customer
   * retries on 204.
   *
   * @returns QueueReceiveResponse A dispatched row.
   * @throws ApiError
   */
  public static queueReceive({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<QueueReceiveResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/queues/receive',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Ack a queue row (idempotent).
   * @returns void
   * @throws ApiError
   */
  public static queueAck({
    slug,
    id,
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
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/queues/{id}/ack',
      path: {
        'slug': slug,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
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
   * Read queue depth, in-flight count, and oldest pending age.
   * Read-only depth / in-flight / oldest-pending stats. NO lease is
   * acquired and no row is mutated — the response can be polled at
   * any cadence without affecting drain behaviour. Free plans can
   * call this for diagnostics even though they cannot send.
   *
   * @returns QueueStateResponse Queue stats for the app.
   * @throws ApiError
   */
  public static queueState({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<QueueStateResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/queues/state',
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
   * List pending queue rows without acquiring a lease.
   * Read-only peek at pending rows, oldest first. Repeated calls
   * return the same rows in the same order — the underlying SQL has
   * no FOR UPDATE / FOR SHARE / advisory lock, so attempts is
   * never incremented and no row state changes. Cursor pagination
   * matches the existing `?before=<id>` convention. NOT equivalent
   * to `queues/receive` — peek never leases.
   *
   * @returns QueuePeekResponse A page of pending rows.
   * @throws ApiError
   */
  public static queuePeek({
    slug,
    limit = 20,
    before,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Maximum number of rows to return; capped at 200.
     */
    limit?: number,
    /**
     * Cursor — the last id from the previous page (omit for the first page).
     */
    before?: string,
  }): CancelablePromise<QueuePeekResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/queues/peek',
      path: {
        'slug': slug,
      },
      query: {
        'limit': limit,
        'before': before,
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
   * List queue rows that exhausted the plan's retry budget.
   * Read-only list of rows in `state='dead_letter'`, newest first.
   * The drain transitions a row here once it has failed
   * `MaxQueueAttempts` times for the app's plan (Hobby 3, Pro 10,
   * Scale 25). NO lease is acquired and no row is mutated. Replaying
   * a dead-letter row is out of scope for this endpoint — see
   * `POST /v1/apps/{slug}/queues/dead_letter/{id}/replay`.
   *
   * @returns QueueDeadLetterResponse A page of dead-letter rows.
   * @throws ApiError
   */
  public static queueDeadLetter({
    slug,
    limit = 20,
    before,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Maximum number of rows to return; capped at 200.
     */
    limit?: number,
    /**
     * Cursor — the last id from the previous page (omit for the first page).
     */
    before?: string,
  }): CancelablePromise<QueueDeadLetterResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/queues/dead_letter',
      path: {
        'slug': slug,
      },
      query: {
        'limit': limit,
        'before': before,
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
   * Reset a dead-letter queue row back to pending.
   * ADR-134 PR-C. Resets the row's `state` to `pending` with
   * `attempts=0`, `last_error=null`, `due_at=now()`,
   * `last_replayed_at=now()`. Distinct from
   * `POST /v1/invocations/{id}/replay`, which enqueues a NEW row
   * tagged Source=InvocationReplay. This endpoint mutates the
   * existing row in place so the dashboard's replay history view
   * tracks the chain on a single row id.
   *
   * Idempotent: a second POST after the first has succeeded
   * finds the row in 'pending' and returns 404. The
   * Idempotency-Key middleware (issued automatically by the SDK)
   * covers double-POST across network retries.
   *
   * @returns AsyncInvokeResponse Replay accepted; row is back to pending.
   * @throws ApiError
   */
  public static queueDeadLetterReplay({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * The invocation id of the dead-letter row to replay.
     */
    id: string,
  }): CancelablePromise<AsyncInvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/queues/dead_letter/{id}/replay',
      path: {
        'slug': slug,
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
