/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateImageRunRequest } from '../models/CreateImageRunRequest.js';
import type { CreateJobRequest } from '../models/CreateJobRequest.js';
import type { CreateJobRunRequest } from '../models/CreateJobRunRequest.js';
import type { ExclusiveJobOperationRequest } from '../models/ExclusiveJobOperationRequest.js';
import type { ExclusiveOperationAccepted } from '../models/ExclusiveOperationAccepted.js';
import type { ImageRunResponse } from '../models/ImageRunResponse.js';
import type { JobArtifactDownloadResponse } from '../models/JobArtifactDownloadResponse.js';
import type { JobRegistryCredentialListResponse } from '../models/JobRegistryCredentialListResponse.js';
import type { JobRegistryCredentialResponse } from '../models/JobRegistryCredentialResponse.js';
import type { JobResponse } from '../models/JobResponse.js';
import type { JobRunCancelledResponse } from '../models/JobRunCancelledResponse.js';
import type { JobRunResponse } from '../models/JobRunResponse.js';
import type { JobTaskLogResponse } from '../models/JobTaskLogResponse.js';
import type { JobTaskRetryResponse } from '../models/JobTaskRetryResponse.js';
import type { ListJobRunsResponse } from '../models/ListJobRunsResponse.js';
import type { ListJobsResponse } from '../models/ListJobsResponse.js';
import type { ListJobTaskAttemptsResponse } from '../models/ListJobTaskAttemptsResponse.js';
import type { ListJobTasksResponse } from '../models/ListJobTasksResponse.js';
import type { ListScheduleOccurrencesResponse } from '../models/ListScheduleOccurrencesResponse.js';
import type { PutJobRegistryCredentialRequest } from '../models/PutJobRegistryCredentialRequest.js';
import type { UpdateJobRequest } from '../models/UpdateJobRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class JobsService {
  /**
   * Run one command from an OCI image (ADR-1161).
   * `docker run`-shaped entry point. apid keeps one platform-managed
   * runner job (kind `image_run`) per account and normalized image, so
   * the image is pulled once and later runs start from the cached
   * artifact. While the image is being pulled the call returns 202
   * `pulling`; call again once `GET /v1/jobs/{job}` reports
   * `image_materialization_status: ready` to create a one-task run
   * (201 `queued`). The command follows Docker's rules: `entrypoint`
   * replaces the image ENTRYPOINT (and drops its CMD unless `args` is
   * set) and `args` replaces the image CMD. Runs, logs and cancellation
   * use `/v1/jobs/{job}/runs`. Free accounts get 402
   * `jobs_not_allowed`; the route returns 404 until the operator sets
   * `FAAS_IMAGE_RUNS=1`. With `interactive: true` (ADR-1330) the 201
   * response carries a one-time `attach` credential for
   * `GET /v1/jobs/{job}/runs/{run_id}/tasks/0/attach` and is never stored
   * for Idempotency-Key replay.
   *
   * @returns ImageRunResponse A one-task run was queued.
   * @throws ApiError
   */
  public static createImageRun({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateImageRunRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ImageRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/image-runs',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Image runs need a plan with Jobs (not Free).`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `Interactive image runs are not enabled on this control-plane host.`,
      },
    });
  }
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List sealed private-registry credentials on a job.
   * Returns registry, username, and timestamps only. Plaintext
   * passwords are never returned. Quota metadata is included for CLI
   * and dashboard rendering.
   *
   * @returns JobRegistryCredentialListResponse Job registry credential metadata.
   * @throws ApiError
   */
  public static listJobRegistryCredentials({
    name,
  }: {
    /**
     * The job slug.
     */
    name: string,
  }): CancelablePromise<JobRegistryCredentialListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}/registry-credentials',
      path: {
        'name': name,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Set or replace a sealed private-registry credential for a job.
   * Seals the plaintext password under namespace `registry_creds` and
   * stores it against the job and registry host. Replacements do not
   * consume another per-job quota slot.
   *
   * @returns JobRegistryCredentialResponse The stored credential metadata.
   * @throws ApiError
   */
  public static setJobRegistryCredential({
    name,
    requestBody,
  }: {
    /**
     * The job slug.
     */
    name: string,
    requestBody: PutJobRegistryCredentialRequest,
  }): CancelablePromise<JobRegistryCredentialResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/jobs/{name}/registry-credentials',
      path: {
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: invalid_registry_host — host missing scheme/path, uppercase, port out of range, or field-length cap exceeded.`,
        401: `code: unauthorized`,
        403: `code: plan_job_registry_credentials_not_allowed — Free plan cannot store job private-registry credentials.`,
        404: `code: not_found`,
        413: `code: plan_job_registry_credential_quota — per-job registry credential cap reached.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Delete a sealed private-registry credential from a job.
   * @returns void
   * @throws ApiError
   */
  public static deleteJobRegistryCredential({
    name,
    registry,
  }: {
    /**
     * The job slug.
     */
    name: string,
    /**
     * Registry host to delete, URL-encoded.
     */
    registry: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/jobs/{name}/registry-credentials',
      path: {
        'name': name,
      },
      query: {
        'registry': registry,
      },
      errors: {
        400: `Invalid registry host or no credential exists for the host.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
   * live job_task instances exist. External input manifests are read from
   * account-authorized obj:// storage; missing objects return 404 and
   * unavailable storage returns 503.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List durable scheduled occurrence decisions for a job.
   * Shows whether each nominal run started, was skipped, missed its start deadline, or was coalesced.
   * @returns ListScheduleOccurrencesResponse A newest-first page of job occurrence outcomes.
   * @throws ApiError
   */
  public static listJobScheduleOccurrences({
    name,
    limit = 50,
    before,
  }: {
    /**
     * Customer-visible name of the recurring job.
     */
    name: string,
    /**
     * Maximum number of job occurrence records to return.
     */
    limit?: number,
    /**
     * Job occurrence id that starts the next older page.
     */
    before?: string,
  }): CancelablePromise<ListScheduleOccurrencesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}/occurrences',
      path: {
        'name': name,
      },
      query: {
        'limit': limit,
        'before': before,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Submit a Job run through managed exclusive ownership.
   * The account and Job identity come from the authenticated route. The key is namespaced by the selected policy and is not an authorization boundary.
   * @returns ExclusiveOperationAccepted Job run accepted for managed ownership or joined to an equivalent operation.
   * @throws ApiError
   */
  public static submitExclusiveJobOperation({
    name,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Account-owned Job name.
     */
    name: string,
    requestBody: ExclusiveJobOperationRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ExclusiveOperationAccepted> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/jobs/{name}/operations',
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
        409: `The policy rejected the request, the Job is paused, or the policy does not include this Job.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Create a linked run from failed inputs of a terminal run.
   * The ready job image must still resolve to the source image digest. New task indexes are dense; source_task_index preserves the old index.
   * @returns JobRunResponse The linked run.
   * @throws ApiError
   */
  public static replayFailedJobRun({
    name,
    id,
  }: {
    /**
     * Job name owned by the authenticated account.
     */
    name: string,
    /**
     * Terminal source run identifier.
     */
    id: string,
  }): CancelablePromise<JobRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/jobs/{name}/runs/{id}/replay-failed',
      path: {
        'name': name,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: conflict`,
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Get tail logs of a task.
   * Returns the durable combined stdout/stderr tail captured before
   * the terminal task microVM is destroyed. Empty LogContent with
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Stream a task's output live (SSE).
   * ADR-1163. Served by the compute gateway, not apid. Waits while the
   * task is queued, then relays the task VM's console output as it is
   * written, including each retry attempt in turn. Frames match the app
   * log stream: `event: log` (seq, instance, stream, line, written_at),
   * `event: gap` when the VM already dropped older lines, heartbeat
   * comments, and one final `event: end` whose `reason` is
   * `task_finished` (all output relayed), `stored_output` (the task ended
   * before its last VM's output could be streamed; read `GET .../logs`),
   * `timeout`, `node_unreachable` or `not_found`. Read the task for its
   * outcome; the stored output from `GET .../logs` remains available.
   *
   * @returns any A text/event-stream of the task's output lines, ending with one `end` frame.
   * @throws ApiError
   */
  public static streamJobTaskLogs({
    name,
    id,
    idx,
    sinceSeq,
  }: {
    /**
     * Job whose task output is streamed.
     */
    name: string,
    /**
     * Job-run identifier (UUIDv4) owning the task.
     */
    id: string,
    /**
     * Zero-based index of the task within the run.
     */
    idx: number,
    /**
     * Resume the current attempt after this line sequence; 0 replays the attempt from its first line.
     */
    sinceSeq?: number,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}/runs/{id}/tasks/{idx}/logs/stream',
      path: {
        'name': name,
        'id': id,
        'idx': idx,
      },
      query: {
        'since_seq': sinceSeq,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Live task output needs a plan with Jobs (not Free).`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Attach to an interactive image run's task (WebSocket).
   * ADR-1330. Served by the compute gateway, not apid. The session uses
   * the interactive app-task wire contract (ADR-1037): the account
   * credential, deploy-write scope, the `X-Gregale-Attach-Token` from the
   * image run's create response, and the `gregale-app-task-attach-v1`
   * WebSocket subprotocol. Optional `rows`/`cols` set the initial terminal
   * size. The run's VM holds the command until the first successful
   * attach, which consumes the token.
   *
   * @returns void
   * @throws ApiError
   */
  public static attachJobTask({
    name,
    id,
    idx,
    rows,
    cols,
  }: {
    /**
     * Runner job of the interactive image run.
     */
    name: string,
    /**
     * Job-run identifier (UUIDv4) of the interactive image run.
     */
    id: string,
    /**
     * Zero-based index of the task; an interactive run has only task 0.
     */
    idx: number,
    /**
     * Initial height of the run's terminal when tty was requested; defaults to 24.
     */
    rows?: number,
    /**
     * Initial width of the run's terminal when tty was requested; defaults to 80.
     */
    cols?: number,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}/runs/{id}/tasks/{idx}/attach',
      path: {
        'name': name,
        'id': id,
        'idx': idx,
      },
      query: {
        'rows': rows,
        'cols': cols,
      },
      errors: {
        401: `code: unauthorized`,
        402: `Interactive image runs need a plan with Jobs (not Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The task VM is still queued or starting; retry after the Retry-After interval.`,
        410: `The task finished, already has a client, or its attach window closed.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List immutable terminal attempts of a task.
   * @returns ListJobTaskAttemptsResponse Attempts ordered by attempt number.
   * @throws ApiError
   */
  public static listJobTaskAttempts({
    name,
    id,
    idx,
    limit = 50,
    offset,
  }: {
    /**
     * Job whose task attempt history is requested.
     */
    name: string,
    /**
     * Run containing the task attempt history.
     */
    id: string,
    /**
     * Zero-based index of the task whose attempts are listed.
     */
    idx: number,
    /**
     * Maximum number of attempts to return.
     */
    limit?: number,
    /**
     * Number of attempts to skip.
     */
    offset?: number,
  }): CancelablePromise<ListJobTaskAttemptsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}/runs/{id}/tasks/{idx}/attempts',
      path: {
        'name': name,
        'id': id,
        'idx': idx,
      },
      query: {
        'limit': limit,
        'offset': offset,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Verify a Gregale managed result and obtain a download URL.
   * Reads the current obj:// object, checks its size and SHA-256 against the task output manifest, then returns a 5-minute signed GET URL. Missing objects return 404; changed bytes return 422.
   * @returns JobArtifactDownloadResponse Verified artifact and download capability.
   * @throws ApiError
   */
  public static downloadJobArtifact({
    name,
    id,
    idx,
    artifact,
  }: {
    /**
     * Job whose output artifact is requested.
     */
    name: string,
    /**
     * Run containing the successful task.
     */
    id: string,
    /**
     * Zero-based index of the successful task.
     */
    idx: number,
    /**
     * Artifact name from the successful task's output manifest.
     */
    artifact: string,
  }): CancelablePromise<JobArtifactDownloadResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/jobs/{name}/runs/{id}/tasks/{idx}/artifacts/{artifact}/download',
      path: {
        'name': name,
        'id': id,
        'idx': idx,
        'artifact': artifact,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Retry one failed job task.
   * Re-queues a failed, timeout, OOM, or cancelled task while its
   * configured retry budget remains. The server applies the same capped
   * exponential backoff as automatic retries and reopens a dead-letter run.
   *
   * @returns JobTaskRetryResponse The task has been queued for another attempt.
   * @throws ApiError
   */
  public static retryJobTask({
    name,
    id,
    idx,
    idempotencyKey,
  }: {
    /**
     * Unique job name. DNS-label safe. Anchors path `/v1/jobs/{name}/runs/{id}/tasks/{idx}/retry`.
     */
    name: string,
    /**
     * The job-run id (UUIDv4).
     */
    id: string,
    /**
     * The zero-based task index within the run.
     */
    idx: number,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<JobTaskRetryResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/jobs/{name}/runs/{id}/tasks/{idx}/retry',
      path: {
        'name': name,
        'id': id,
        'idx': idx,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `The task is not retriable or its retry budget is exhausted.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
}
