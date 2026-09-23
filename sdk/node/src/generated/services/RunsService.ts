/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppTaskListResponse } from '../models/AppTaskListResponse.js';
import type { AppTaskResponse } from '../models/AppTaskResponse.js';
import type { CreateAppTaskRequest } from '../models/CreateAppTaskRequest.js';
import type { CreateExecutionRequest } from '../models/CreateExecutionRequest.js';
import type { ExecutionListResponse } from '../models/ExecutionListResponse.js';
import type { ExecutionResponse } from '../models/ExecutionResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class RunsService {
  /**
   * List disposable executions.
   * Returns the caller's newest disposable execution receipts. Results are
   * account-scoped and ordered by creation time descending. Use `status`
   * to narrow the page before applying offset pagination; source and input
   * are never returned.
   *
   * @returns ExecutionListResponse Account-scoped execution page.
   * @throws ApiError
   */
  public static listExecutions({
    limit = 50,
    offset,
    status,
  }: {
    /**
     * Maximum number of execution receipts to return.
     */
    limit?: number,
    /**
     * Number of matching receipts to skip.
     */
    offset?: number,
    /**
     * Return only executions in this lifecycle state.
     */
    status?: 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'out_of_memory' | 'cancelled',
  }): CancelablePromise<ExecutionListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/executions',
      query: {
        'limit': limit,
        'offset': offset,
        'status': status,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Execute source in an isolated disposable microVM.
   * Queues one bounded Node.js or Python source execution. Source and
   * input are sealed by the control plane before dispatch; the guest has
   * loopback-only networking and an internal ephemeral scratch filesystem.
   * The VM is always destroyed before a terminal result is persisted.
   * This endpoint is an explicit opt-in on the control plane and may return
   * 501 while the host isolation gate is disabled.
   *
   * @returns ExecutionResponse Execution admitted and queued.
   * @throws ApiError
   */
  public static createExecution({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateExecutionRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ExecutionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/executions',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Get one disposable execution.
   * @returns ExecutionResponse Current or terminal execution receipt.
   * @throws ApiError
   */
  public static getExecution({
    id,
  }: {
    /**
     * Canonical UUID for a disposable execution.
     */
    id: string,
  }): CancelablePromise<ExecutionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/executions/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Cancel one disposable execution.
   * Cancellation is idempotent and destroys a claimed VM during teardown.
   * @returns ExecutionResponse Execution cancellation requested, or its existing terminal receipt returned.
   * @throws ApiError
   */
  public static cancelExecution({
    id,
    idempotencyKey,
  }: {
    /**
     * Canonical UUID for a disposable execution.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ExecutionResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/executions/{id}',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Stream disposable execution events.
   * Opens a resumable Server-Sent Events stream for one execution. Event
   * ids are monotonically increasing control-plane cursors; reconnect with
   * `after` or `Last-Event-ID`. The stream emits bounded status, stdout,
   * stderr, and terminal events and closes after the terminal event.
   * Source, input, host paths, and VM internals are never included.
   *
   * @returns string Resumable execution event stream.
   * @throws ApiError
   */
  public static streamExecutionEvents({
    id,
    after,
    limit = 100,
    lastEventId,
  }: {
    /**
     * Canonical UUID for a disposable execution.
     */
    id: string,
    /**
     * Resume after this event id (exclusive).
     */
    after?: number,
    /**
     * Maximum number of events returned per replay batch.
     */
    limit?: number,
    /**
     * Standard SSE reconnect cursor; `after` takes precedence.
     */
    lastEventId?: number,
  }): CancelablePromise<string> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/executions/{id}/events',
      path: {
        'id': id,
      },
      headers: {
        'Last-Event-ID': lastEventId,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * List deployment-attached tasks for an app.
   * Returns newest-first one-off command receipts for the app. Task rows
   * remain pinned to the immutable deployment selected at admission.
   * Scheduler leases and artifact storage identifiers are never returned.
   *
   * @returns AppTaskListResponse App-scoped task page.
   * @throws ApiError
   */
  public static listAppTasks({
    slug,
    limit = 50,
    offset,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Maximum number of task receipts to return.
     */
    limit?: number,
    /**
     * Number of task receipts to skip.
     */
    offset?: number,
  }): CancelablePromise<AppTaskListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/tasks',
      path: {
        'slug': slug,
      },
      query: {
        'limit': limit,
        'offset': offset,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Run a one-off command against an app deployment.
   * Queues one manual command in a fresh task VM using the app's current
   * live deployment, scoped configuration, bindings, and network policy.
   * The selected deployment is pinned at admission and the VM is destroyed
   * before terminal completion. Release tasks cannot be created here.
   *
   * @returns AppTaskResponse Manual app task admitted and queued.
   * @throws ApiError
   */
  public static createAppTask({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateAppTaskRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppTaskResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/tasks',
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
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `code: conflict`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Get one deployment-attached app task.
   * @returns AppTaskResponse Current or terminal app-task receipt.
   * @throws ApiError
   */
  public static getAppTask({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Canonical UUID for a deployment-attached app task.
     */
    id: string,
  }): CancelablePromise<AppTaskResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/tasks/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Cancel one deployment-attached app task.
   * Cancellation is idempotent; claimed work is torn down before terminal acknowledgement.
   * @returns AppTaskResponse App-task cancellation recorded, or its existing terminal receipt returned.
   * @throws ApiError
   */
  public static cancelAppTask({
    slug,
    id,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Canonical UUID for a deployment-attached app task.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppTaskResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/tasks/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
}
