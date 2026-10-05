/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AsyncInvokeResponse } from '../models/AsyncInvokeResponse.js';
import type { CancelPendingWorkRequest } from '../models/CancelPendingWorkRequest.js';
import type { CancelPendingWorkResponse } from '../models/CancelPendingWorkResponse.js';
import type { Invocation } from '../models/Invocation.js';
import type { InvokeRequest } from '../models/InvokeRequest.js';
import type { InvokeResponse } from '../models/InvokeResponse.js';
import type { ListInvocationsResponse } from '../models/ListInvocationsResponse.js';
import type { UpsertWorkPolicyRequest } from '../models/UpsertWorkPolicyRequest.js';
import type { WorkPolicyListResponse } from '../models/WorkPolicyListResponse.js';
import type { WorkPolicyResponse } from '../models/WorkPolicyResponse.js';
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
    xGregaleRevision,
    xGregaleRelease,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: InvokeRequest,
    /**
     * Exact deployment pin. Mutually exclusive with X-Gregale-Release; checked again at delivery.
     */
    xGregaleRevision?: string,
    /**
     * Immutable project release set. Defaults to the active set for project apps and is checked again at delivery.
     */
    xGregaleRelease?: string,
  }): CancelablePromise<InvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/invoke',
      path: {
        'slug': slug,
      },
      headers: {
        'X-Gregale-Revision': xGregaleRevision,
        'X-Gregale-Release': xGregaleRelease,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
    xGregaleRevision,
    xGregaleRelease,
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
    /**
     * Exact deployment pin. Mutually exclusive with X-Gregale-Release; checked again at delivery.
     */
    xGregaleRevision?: string,
    /**
     * Immutable project release set. Defaults to the active set for project apps and is checked again at delivery.
     */
    xGregaleRelease?: string,
  }): CancelablePromise<AsyncInvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/invoke/async',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
        'X-Gregale-Revision': xGregaleRevision,
        'X-Gregale-Release': xGregaleRelease,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        413: `code: source_too_large — payload exceeds the plan's MaxSourceBytesPerInvocation.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List named work policies for an app.
   * Stage reads return the complete desired policy collection from immutable workload settings. An uninitialized stage collection returns 409 and never inherits production policies. Stage policy execution remains unavailable until work lanes and producers are isolated.
   * @returns WorkPolicyListResponse App work policies.
   * @throws ApiError
   */
  public static listAppWorkPolicies({
    slug,
    environment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Registered project environment. Omit for legacy production policies. An explicit production selection uses the legacy collection.
     */
    environment?: string,
  }): CancelablePromise<WorkPolicyListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/work-policies',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Create or update a named app work policy.
   * Legacy production changes affect new work only. Stage edits create immutable desired workload configuration and subsequent deployments pin it; existing deployments keep their policies. Stage policy execution and qualification remain unavailable until work lanes and producers are isolated.
   * @returns WorkPolicyResponse Saved policy.
   * @throws ApiError
   */
  public static upsertAppWorkPolicy({
    slug,
    name,
    requestBody,
    environment,
    ifWorkloadRevision,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Named app work policy.
     */
    name: string,
    requestBody: UpsertWorkPolicyRequest,
    /**
     * Edit the complete desired collection in a registered stage. Omit for legacy production policies. Protected environments reject direct edits.
     */
    environment?: string,
    /**
     * Expected complete desired workload revision for a stage edit. Zero means no revision exists; concurrent edits return 409.
     */
    ifWorkloadRevision?: number,
  }): CancelablePromise<WorkPolicyResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/work-policies/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      headers: {
        'If-Workload-Revision': ifWorkloadRevision,
      },
      query: {
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Delete a policy after removing event subscription bindings.
   * A stage deletion preserves an explicit empty collection and its collection revision clock after the last policy is removed. Production producer bindings retain their existing deletion checks.
   * @returns void
   * @throws ApiError
   */
  public static deleteAppWorkPolicy({
    slug,
    name,
    environment,
    ifWorkloadRevision,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Named app work policy.
     */
    name: string,
    /**
     * Edit the complete desired collection in a registered stage. Omit for legacy production policies. Protected environments reject direct edits.
     */
    environment?: string,
    /**
     * Expected complete desired workload revision for a stage edit.
     */
    ifWorkloadRevision?: number,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/work-policies/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      headers: {
        'If-Workload-Revision': ifWorkloadRevision,
      },
      query: {
        'environment': environment,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Cancel pending work for one policy and application key.
   * Running work continues. A repeated Idempotency-Key returns the original receipt and does not cancel newer work. A stage selection cancels only its isolated environment lane, including work admitted under a policy that has since been deleted from desired settings. Cancellation receipts are independent per environment. Omitting environment preserves the production API.
   * @returns CancelPendingWorkResponse Durable cancellation receipt.
   * @throws ApiError
   */
  public static cancelPendingAppWork({
    slug,
    name,
    requestBody,
    idempotencyKey,
    environment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Policy whose pending lane is being cancelled.
     */
    name: string,
    requestBody: CancelPendingWorkRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
    /**
     * Registered project environment whose pending work should be cancelled. Stage lanes and receipts are isolated by the environment's immutable identity.
     */
    environment?: string,
  }): CancelablePromise<CancelPendingWorkResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/work-policies/{name}/cancel-pending',
      path: {
        'slug': slug,
        'name': name,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Re-issue a failed or dead_letter invocation.
   * Accepts no request body. Requires deployment write scope and configured
   * MFA. Only failed or dead-lettered unbound unkeyed work is eligible.
   * The parent and its current app must belong to the caller. The child
   * preserves the original request, deployment scope, customer identity
   * and trusted replay lineage. Trace/version headers and execution/result
   * lifetimes are refreshed; retry policy uses the current app and plan.
   *
   * Each parent creates at most one durable recovery child. Concurrent and
   * repeated requests return that child regardless of Idempotency-Key,
   * including after completion. A subsequent recovery targets the failed
   * child. If its child has been pruned, the retained parent returns 409
   * `invocation_replay_unavailable` instead of creating another execution.
   * Existing acceptance is returned before checking expired deployment pins.
   * Application delivery and external side effects remain at least once.
   *
   * Other parent states return `invocation_not_replayable`. Keyed work
   * returns `keyed_replay_requires_policy`; use `/replay-keyed` for failed
   * keyed work. Queue-bound or named-queue work returns
   * `queue_replay_requires_binding` and requires its app queue dead-letter
   * replay endpoint.
   *
   * @returns AsyncInvokeResponse The recovery child was enqueued or already exists.
   * @throws ApiError
   */
  public static replayInvocation({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<AsyncInvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/invocations/{id}/replay',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `The parent is not replayable, requires its policy/binding recovery
        path, or its durable child is no longer retained
        (\`invocation_replay_unavailable\`).
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Recover failed keyed work in its captured policy lane
   * Accepts no request body. Requires deploy write scope and configured MFA.
   * Only failed keyed work without a queue binding is eligible. The child
   * retains the captured policy revision, key, fairness limits, deployment
   * scope, customer identity, payload, retry policy and trusted replay root.
   * It receives the next sequence in the same lane, after previously admitted
   * work. Replay does not supersede pending rows or restart debounce.
   *
   * The original pending expiry and start deadline remain effective. A new
   * replay after either elapsed deadline returns `keyed_replay_expired`.
   * Each parent creates at most one child. Repeating a request returns that
   * child, including after completion; a subsequent recovery must target
   * the failed child. If the child has been pruned while its parent remains,
   * the parent returns `keyed_replay_unavailable` instead of executing again.
   * The original failure remains visible in event receipts and retained
   * replay history. Delivery and application side effects remain at least once.
   *
   * @returns AsyncInvokeResponse The recovery child was enqueued or already exists.
   * @throws ApiError
   */
  public static replayKeyedInvocation({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<AsyncInvokeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/invocations/{id}/replay-keyed',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Recovery is ineligible, its pending deadline expired, or its child is no longer retained.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
