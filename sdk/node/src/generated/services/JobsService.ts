/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateJobRequest } from '../models/CreateJobRequest.js';
import type { CreateRunRequest } from '../models/CreateRunRequest.js';
import type { JobResponse } from '../models/JobResponse.js';
import type { JobRunResponse } from '../models/JobRunResponse.js';
import type { JobTaskResponse } from '../models/JobTaskResponse.js';
import type { ListJobsResponse } from '../models/ListJobsResponse.js';
import type { ListRunsResponse } from '../models/ListRunsResponse.js';
import type { ListRunTasksResponse } from '../models/ListRunTasksResponse.js';
import type { RetryTaskRequest } from '../models/RetryTaskRequest.js';
import type { UpdateJobRequest } from '../models/UpdateJobRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class JobsService {
  /**
   * List jobs on the account.
   * Page of jobs owned by the caller's account. The
   * `quota_max` and `count` fields let the dashboard render
   * a "3/25 jobs" progress bar without a second call. Cursor
   * pagination uses `?before=<id>` of the last row from the
   * previous page; omit for the most recent slice.
   *
   * @returns ListJobsResponse A page of jobs on the account.
   * @throws ApiError
   */
  public static listJobs({
    before,
    limit = 50,
  }: {
    /**
     * Cursor — return jobs older than this id. Omit for the most recent page.
     */
    before?: string,
    /**
     * Page size; 1-500, default 50.
     */
    limit?: number,
  }): CancelablePromise<ListJobsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs',
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Jobs are disabled for this account (FAAS_JOBS_ENABLED is off).`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Create a job.
   * Create the job template. Per-plan caps from
   * `pkg/api/limits.go::JobMax*` apply — Free plan rejects
   * with `code=job_quota_exceeded` (HTTP 403). The Name is
   * the customer-facing slug (k8s-style: lowercase + dashes,
   * max 63 chars). `image_ref` must be a `sha256:...` digest
   * or `ref:...` named pointer. `env_overrides` is plaintext
   * key=value pairs (NOT sealed — secrets are out of scope
   * for jobs per ADR-099 §Decision 10).
   *
   * @returns JobResponse The new job.
   * @throws ApiError
   */
  public static createJob({
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Job creation payload. See CreateJobRequest.
     */
    requestBody: CreateJobRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<JobResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/jobs',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `Jobs are disabled for this account (FAAS_JOBS_ENABLED is off).`,
        409: `A job with the same name already exists on the account.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Get one job.
   * Single-row read for one job. IDOR-safe: a cross-account
   * fetch returns 404 (NOT 403) so the existence of an
   * out-of-account job id is never disclosed.
   *
   * @returns JobResponse The job.
   * @throws ApiError
   */
  public static getJob({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<JobResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{id}',
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
   * Partial-update a job.
   * Every field is optional; absent = leave unchanged. The
   * PATCH semantics mirror the cron path (every field is a
   * pointer; nil vs zero is distinguishable on the wire).
   * `name` is intentionally NOT patchable — rename is a
   * destructive operation that needs its own endpoint (not
   * in PR-D scope).
   *
   * @returns JobResponse The updated job.
   * @throws ApiError
   */
  public static updateJob({
    id,
    requestBody,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Job patch — every field is optional. See UpdateJobRequest.
     */
    requestBody: UpdateJobRequest,
  }): CancelablePromise<JobResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/jobs/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Delete a job.
   * Removes the job template. In-flight runs continue to
   * completion (the schedd owns instance.kind='job' rows);
   * new POST /v1/jobs/{id}/runs against this id will return
   * 404.
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteJob({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/jobs/{id}',
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
   * List runs for a job.
   * Page of runs for one job, newest first. Each row reports
   * the `aggregate_status` (queued/running/succeeded/failed/cancelled),
   * task fan-out counters (succeeded/failed/cancelled/running), and
   * the run's `started_at`/`finished_at` timestamps.
   *
   * @returns ListRunsResponse A page of runs for this job.
   * @throws ApiError
   */
  public static listRuns({
    id,
    before,
    limit = 50,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Cursor — return runs older than this run id. Omit for the most recent page.
     */
    before?: string,
    /**
     * Page size; 1-500, default 50.
     */
    limit?: number,
  }): CancelablePromise<ListRunsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{id}/runs',
      path: {
        'id': id,
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
   * Create a new run for a job.
   * Enqueue a new run. Tasks is required (1 ≤ tasks ≤
   * JobMaxTasksPerRun for the plan); parallelism and
   * env_overrides default to the job's configured values.
   * The schedd dispatch tick picks up the run on the next
   * runJobsTick and fans out tasks to fresh task VMs (cold
   * boot only — ADR-005).
   *
   * @returns JobRunResponse The new run.
   * @throws ApiError
   */
  public static createRun({
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Run payload. See CreateRunRequest.
     */
    requestBody: CreateRunRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<JobRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/jobs/{id}/runs',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Get one run.
   * Single-row read for one run. IDOR-safe: cross-account
   * fetch returns 404.
   *
   * @returns JobRunResponse The run.
   * @throws ApiError
   */
  public static getRun({
    runId,
  }: {
    /**
     * The run id (UUID hex, no dashes).
     */
    runId: string,
  }): CancelablePromise<JobRunResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/runs/{run_id}',
      path: {
        'run_id': runId,
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
   * Cancel a run.
   * Marks the run `aggregate_status='cancelled'`. Tasks in
   * queued state are skipped by the next dispatch tick;
   * tasks already in flight (claimed/running) are killed
   * via the vmmd watchdog (SIGKILL after TaskTimeoutS +
   * 30s grace). Already-terminal tasks are NOT modified.
   * Idempotent: a second cancel against an already-terminal
   * run returns 409 `code=job_run_cancelled`.
   *
   * @returns JobRunResponse The cancelled run.
   * @throws ApiError
   */
  public static cancelRun({
    runId,
  }: {
    /**
     * The run id (UUID hex, no dashes).
     */
    runId: string,
  }): CancelablePromise<JobRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runs/{run_id}/cancel',
      path: {
        'run_id': runId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `Run already terminal (succeeded/failed/cancelled). code=job_run_cancelled.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List tasks for a run.
   * Per-task execution view. Cursor pagination uses
   * `?before=<task_index>`; tasks are ordered ASCENDING by
   * task_index (the dispatch order, oldest tasks first).
   * Each row reports the per-task status (queued/claimed/
   * running/succeeded/failed/timeout/oom/cancelled),
   * attempt counter, and the underlying instance_id (the
   * task VM that ran it; null until schedd claims).
   *
   * @returns ListRunTasksResponse A page of tasks for this run.
   * @throws ApiError
   */
  public static listRunTasks({
    runId,
    before,
    limit = 50,
  }: {
    /**
     * The run id (UUID hex, no dashes).
     */
    runId: string,
    /**
     * Cursor — return tasks with task_index > before. Omit for the first slice.
     */
    before?: number,
    /**
     * Page size; 1-500, default 50.
     */
    limit?: number,
  }): CancelablePromise<ListRunTasksResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/runs/{run_id}/tasks',
      path: {
        'run_id': runId,
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
   * Retry a failed task.
   * Manual retry of a task in failed/timeout/oom/cancelled
   * state. The dispatch tick picks up the new attempt on
   * the next runJobsTick. By default the per-task attempt
   * counter is reset to 0; pass `reset_attempt: false` to
   * increment the existing counter instead. Returns 404
   * `code=job_task_not_found` if the task is currently
   * queued/claimed/running/succeeded (retry is not valid
   * for those states).
   *
   * @returns JobTaskResponse The task after retry reset.
   * @throws ApiError
   */
  public static retryTask({
    runId,
    idx,
    requestBody,
  }: {
    /**
     * The run id (UUID hex, no dashes).
     */
    runId: string,
    /**
     * The task index (0-based).
     */
    idx: number,
    /**
     * Optional retry parameters. See RetryTaskRequest.
     */
    requestBody?: RetryTaskRequest,
  }): CancelablePromise<JobTaskResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/runs/{run_id}/tasks/{idx}/retry',
      path: {
        'run_id': runId,
        'idx': idx,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `Task not found, or task is in a non-retryable state. code=job_task_not_found.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
