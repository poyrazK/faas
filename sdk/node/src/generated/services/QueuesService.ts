/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AsyncInvokeResponse } from '../models/AsyncInvokeResponse.js';
import type { CreateQueueBindingRequest } from '../models/CreateQueueBindingRequest.js';
import type { DeadLetterEvent } from '../models/DeadLetterEvent.js';
import type { DeadLetterEventsResponse } from '../models/DeadLetterEventsResponse.js';
import type { DeadLetterPurgeResponse } from '../models/DeadLetterPurgeResponse.js';
import type { DeadLetterReplayAllResponse } from '../models/DeadLetterReplayAllResponse.js';
import type { QueueBindingResponse } from '../models/QueueBindingResponse.js';
import type { QueueDeadLetterResponse } from '../models/QueueDeadLetterResponse.js';
import type { QueuePeekResponse } from '../models/QueuePeekResponse.js';
import type { QueueReceiveResponse } from '../models/QueueReceiveResponse.js';
import type { QueueSendRequest } from '../models/QueueSendRequest.js';
import type { QueueSendResponse } from '../models/QueueSendResponse.js';
import type { QueueStateResponse } from '../models/QueueStateResponse.js';
import type { UpdateQueueBindingRequest } from '../models/UpdateQueueBindingRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class QueuesService {
  /**
   * Replay a failed event from the dashboard.
   * Resets the source event to pending and redirects to the Failed Events inbox.
   * @returns void
   * @throws ApiError
   */
  public static dashboardReplayFailedEvent({
    slug,
    id,
    formData,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Unified failed-event identifier to replay.
     */
    id: string,
    formData: {
      csrf_token: string;
    },
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/dashboard/failed-events/{slug}/{id}/replay',
      path: {
        'slug': slug,
        'id': id,
      },
      formData: formData,
      mediaType: 'application/x-www-form-urlencoded',
      errors: {
        303: `Redirect to the Failed Events inbox after replay.`,
        400: `Invalid dashboard CSRF token for replay.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Discard a failed event from the dashboard.
   * Removes the ledger projection and redirects to the Failed Events inbox.
   * @returns void
   * @throws ApiError
   */
  public static dashboardDiscardFailedEvent({
    slug,
    id,
    formData,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Unified failed-event identifier to discard.
     */
    id: string,
    formData: {
      csrf_token: string;
    },
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/dashboard/failed-events/{slug}/{id}/discard',
      path: {
        'slug': slug,
        'id': id,
      },
      formData: formData,
      mediaType: 'application/x-www-form-urlencoded',
      errors: {
        303: `Redirect to the Failed Events inbox after discard.`,
        400: `Invalid dashboard CSRF token for discard.`,
        404: `code: not_found`,
      },
    });
  }
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
   * List first-class queue bindings for an app.
   * Returns the durable mappings between logical queues and worker/job
   * workloads. Bindings are the configuration source for push consumers
   * and queue-depth autoscaling; queue messages remain under /queues*.
   *
   * @returns QueueBindingResponse Queue bindings.
   * @throws ApiError
   */
  public static listQueueBindings({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<QueueBindingResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/queue-bindings',
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
   * Create a first-class queue binding.
   * @returns QueueBindingResponse Created queue binding.
   * @throws ApiError
   */
  public static createQueueBinding({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateQueueBindingRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<QueueBindingResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/queue-bindings',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Get one queue binding.
   * @returns QueueBindingResponse Queue binding.
   * @throws ApiError
   */
  public static getQueueBinding({
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
  }): CancelablePromise<QueueBindingResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/queue-bindings/{id}',
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
  /**
   * Update one queue binding.
   * @returns QueueBindingResponse Updated queue binding.
   * @throws ApiError
   */
  public static updateQueueBinding({
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
    requestBody: UpdateQueueBindingRequest,
  }): CancelablePromise<QueueBindingResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/queue-bindings/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Delete one queue binding.
   * @returns void
   * @throws ApiError
   */
  public static deleteQueueBinding({
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
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/queue-bindings/{id}',
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
  /**
   * List dead-letter events for an app.
   * Returns queue invocation, broker trigger, and outbound webhook
   * delivery failures in one durable, newest-first ledger. The source row
   * is not leased or mutated.
   *
   * @returns DeadLetterEventsResponse A page of unified dead-letter events.
   * @throws ApiError
   */
  public static listDeadLetterEvents({
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
  }): CancelablePromise<DeadLetterEventsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/dlq',
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
   * Purge dead-letter events from the app ledger.
   * Removes up to `limit` ledger projections; source records remain dead-lettered.
   * @returns DeadLetterPurgeResponse Number of ledger events purged.
   * @throws ApiError
   */
  public static purgeDeadLetterEvents({
    slug,
    limit = 20,
    idempotencyKey,
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
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<DeadLetterPurgeResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/dlq',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
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
   * Replay pending dead-letter events for an app.
   * Atomically resets up to `limit` pending queue invocation, broker
   * trigger, and outbound webhook delivery records to pending and stamps
   * each ledger row with replayed_at. Concurrent operators claim disjoint
   * rows.
   *
   * @returns DeadLetterReplayAllResponse Number of events accepted for replay.
   * @throws ApiError
   */
  public static replayAllDeadLetterEvents({
    slug,
    limit = 20,
    idempotencyKey,
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
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<DeadLetterReplayAllResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/dlq:replay_all',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
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
   * Get one dead-letter event for an app.
   * @returns DeadLetterEvent The dead-letter event.
   * @throws ApiError
   */
  public static getDeadLetterEvent({
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
  }): CancelablePromise<DeadLetterEvent> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/dlq/{id}',
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
  /**
   * Purge one dead-letter event from the app ledger.
   * Removes only the ledger projection; the source remains dead-lettered.
   * @returns void
   * @throws ApiError
   */
  public static deleteDeadLetterEvent({
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
      method: 'DELETE',
      url: '/v1/apps/{slug}/dlq/{id}',
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
   * Replay one dead-letter event atomically.
   * Resets the source invocation, trigger record, or outbound webhook
   * delivery to pending, clears its retry error, and records replayed_at
   * on the unified ledger.
   *
   * @returns DeadLetterEvent Replay accepted.
   * @throws ApiError
   */
  public static replayDeadLetterEvent({
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
  }): CancelablePromise<DeadLetterEvent> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/dlq/{id}/replay',
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
}
