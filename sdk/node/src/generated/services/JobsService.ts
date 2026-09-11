/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateJobRequest } from '../models/CreateJobRequest.js';
import type { CreateJobRunRequest } from '../models/CreateJobRunRequest.js';
import type { JobResponse } from '../models/JobResponse.js';
import type { JobRunCancelledResponse } from '../models/JobRunCancelledResponse.js';
import type { JobRunResponse } from '../models/JobRunResponse.js';
import type { JobTaskLogResponse } from '../models/JobTaskLogResponse.js';
import type { ListJobRunsResponse } from '../models/ListJobRunsResponse.js';
import type { ListJobsResponse } from '../models/ListJobsResponse.js';
import type { ListJobTasksResponse } from '../models/ListJobTasksResponse.js';
import type { UpdateJobRequest } from '../models/UpdateJobRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class JobsService {
  /**
   * List jobs on the account.
   * Page-based pagination via ?limit / ?offset. NextOffset=-1
   * signals the last page. Free accounts return an empty
   * list (read gate is not in the plan).
   *
   * @returns ListJobsResponse A page of jobs.
   * @throws ApiError
   */
  public static listJobs({
    limit = 50,
    offset,
  }: {
    /**
     * Maximum number of jobs to return in this page (1–200, default 50).
     */
    limit?: number,
    /**
     * Number of jobs to skip before returning results. NextOffset=-1 in the body signals the last page.
     */
    offset?: number,
  }): CancelablePromise<ListJobsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs',
      query: {
        'limit': limit,
        'offset': offset,
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
   * Create a job template.
   * Plan-tier gate (Free → 402 jobs_not_allowed) precedes
   * per-plan cap clamping (RAM, task_timeout, parallelism,
   * retry_max). The per-account JobMaxPerAccount quota is
   * enforced atomically (PR-A → PR-B style follow-up).
   *
   * @returns JobResponse The new job.
   * @throws ApiError
   */
  public static createJob({
    requestBody,
    idempotencyKey,
  }: {
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
        402: `Plan tier does not include job support (Free plan).`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        409: `code: conflict`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Get one job.
   * @returns JobResponse The job.
   * @throws ApiError
   */
  public static getJob({
    name,
  }: {
    /**
     * The job slug (3-40 chars, lowercase letters/digits/hyphens).
     */
    name: string,
  }): CancelablePromise<JobResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}',
      path: {
        'name': name,
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
   * @returns JobResponse The updated job.
   * @throws ApiError
   */
  public static updateJob({
    name,
    requestBody,
  }: {
    /**
     * The job slug (3-40 chars, lowercase letters/digits/hyphens).
     */
    name: string,
    requestBody: UpdateJobRequest,
  }): CancelablePromise<JobResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/jobs/{name}',
      path: {
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
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
   * Soft-delete a job.
   * Returns 409 job_has_live_instances when at least one
   * kind='job_task' AND status NOT IN ('parked','destroyed')
   * instance still references the job. Cancel pending runs
   * first or wait for them to drain.
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteJob({
    name,
  }: {
    /**
     * The job slug (3-40 chars, lowercase letters/digits/hyphens).
     */
    name: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/jobs/{name}',
      path: {
        'name': name,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `Job has live instances.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List runs of a job.
   * @returns ListJobRunsResponse A page of runs for this job.
   * @throws ApiError
   */
  public static listJobRuns({
    name,
    limit = 50,
    offset,
  }: {
    /**
     * Unique job name. DNS-label safe (`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`). Anchors path `/v1/jobs/{name}/runs`.
     */
    name: string,
    /**
     * Maximum number of runs to return in this page (1–200, default 50).
     */
    limit?: number,
    /**
     * Number of runs to skip before returning results. NextOffset=-1 in the body signals the last page.
     */
    offset?: number,
  }): CancelablePromise<ListJobRunsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}/runs',
      path: {
        'name': name,
      },
      query: {
        'limit': limit,
        'offset': offset,
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
   * Fan out N tasks of a job.
   * Atomic fan-out via `generate_series` CTE in pgstore.
   * `tasks` clamped against Plan.JobMaxTasksPerRun
   * (Hobby=100, Pro=1000, Scale=5000). Per-account
   * JobConcurrentPerAccount gate refuses if too many
   * live job_task instances exist.
   *
   * @returns JobRunResponse The new run + fan-out.
   * @throws ApiError
   */
  public static createJobRun({
    name,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Unique job name. DNS-label safe (`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`). Anchors path `/v1/jobs/{name}/runs`.
     */
    name: string,
    requestBody: CreateJobRunRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<JobRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/jobs/{name}/runs',
      path: {
        'name': name,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `Job is paused.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Get one run of a job.
   * @returns JobRunResponse The run aggregate.
   * @throws ApiError
   */
  public static getJobRun({
    name,
    id,
  }: {
    /**
     * Job name (path). DNS-label safe. Body creates a run against this template. Anchors path `/v1/jobs/{name}/runs/{id}`.
     */
    name: string,
    /**
     * The job_run id (UUID).
     */
    id: string,
  }): CancelablePromise<JobRunResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}/runs/{id}',
      path: {
        'name': name,
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
   * Cancel a run.
   * Transitions every non-terminal task of the run to
   * status='cancelled'. For claimed (running) tasks,
   * schedd SIGTERMs the guest; the supervisor catches
   * SIGTERM, writes job_exit{error_class='cancelled',
   * exit_code=143}, then poweroff.
   *
   * @returns JobRunCancelledResponse The cancelled run aggregate.
   * @throws ApiError
   */
  public static cancelJobRun({
    name,
    id,
  }: {
    /**
     * Unique job name. DNS-label safe (`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`). Anchors path `/v1/jobs/{name}/runs/{id}/cancel`.
     */
    name: string,
    /**
     * Job-run identifier (UUIDv4, path). Returns the run + aggregated counters. Anchors path `/v1/jobs/{name}/runs/{id}/cancel`.
     */
    id: string,
  }): CancelablePromise<JobRunCancelledResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/jobs/{name}/runs/{id}/cancel',
      path: {
        'name': name,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List tasks of a run.
   * @returns ListJobTasksResponse A page of tasks for this run.
   * @throws ApiError
   */
  public static listJobRunTasks({
    name,
    id,
    limit = 50,
    offset,
  }: {
    /**
     * Unique job name. DNS-label safe (`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`). Anchors path `/v1/jobs/{name}/runs/{id}/tasks`.
     */
    name: string,
    /**
     * Job-run identifier (UUIDv4, path). Body cancels this run. Anchors path `/v1/jobs/{name}/runs/{id}/tasks`.
     */
    id: string,
    /**
     * Maximum number of tasks to return in this page (1–200, default 50).
     */
    limit?: number,
    /**
     * Number of tasks to skip before returning results. NextOffset=-1 in the body signals the last page.
     */
    offset?: number,
  }): CancelablePromise<ListJobTasksResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}/runs/{id}/tasks',
      path: {
        'name': name,
        'id': id,
      },
      query: {
        'limit': limit,
        'offset': offset,
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
   * Get tail logs of a task.
   * Proxied from vmmd's tail endpoint on the compute node
   * that owns the instance. Empty LogContent with
   * Truncated=false means the task never produced output
   * (process exited before writing anything — common for
   * OOM-killed tasks).
   *
   * @returns JobTaskLogResponse The tail log + truncated flag.
   * @throws ApiError
   */
  public static getJobTaskLogs({
    name,
    id,
    idx,
    maxBytes = 65536,
  }: {
    /**
     * Unique job name. DNS-label safe (`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`). Anchors path `/v1/jobs/{name}/runs/{id}/tasks/{idx}/logs`.
     */
    name: string,
    /**
     * Job-run identifier (UUIDv4, path). Returns a page of tasks. Anchors path `/v1/jobs/{name}/runs/{id}/tasks/{idx}/logs`.
     */
    id: string,
    /**
     * The task index within the run (zero-indexed).
     */
    idx: number,
    /**
     * Maximum log payload size to return. Default 64 KiB; capped at 1 MiB.
     */
    maxBytes?: number,
  }): CancelablePromise<JobTaskLogResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}/runs/{id}/tasks/{idx}/logs',
      path: {
        'name': name,
        'id': id,
        'idx': idx,
      },
      query: {
        'max_bytes': maxBytes,
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
