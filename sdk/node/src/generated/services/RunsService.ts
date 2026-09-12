/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
   * @returns ExecutionResponse Cancellation accepted or already applied.
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
}
