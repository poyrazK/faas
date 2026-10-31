/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CompleteWorkflowCallbackResponse } from '../models/CompleteWorkflowCallbackResponse.js';
import type { CreateWorkflowCallbackWebhookBindingRequest } from '../models/CreateWorkflowCallbackWebhookBindingRequest.js';
import type { InjectWorkflowEventRequest } from '../models/InjectWorkflowEventRequest.js';
import type { InjectWorkflowEventResponse } from '../models/InjectWorkflowEventResponse.js';
import type { ListWorkflowCallbacksResponse } from '../models/ListWorkflowCallbacksResponse.js';
import type { ListWorkflowResumesResponse } from '../models/ListWorkflowResumesResponse.js';
import type { ListWorkflowRunsResponse } from '../models/ListWorkflowRunsResponse.js';
import type { ListWorkflowScheduleOccurrencesResponse } from '../models/ListWorkflowScheduleOccurrencesResponse.js';
import type { ListWorkflowSchedulesResponse } from '../models/ListWorkflowSchedulesResponse.js';
import type { ListWorkflowStepAttemptsResponse } from '../models/ListWorkflowStepAttemptsResponse.js';
import type { ListWorkflowStepsResponse } from '../models/ListWorkflowStepsResponse.js';
import type { ResumeWorkflowRunRequest } from '../models/ResumeWorkflowRunRequest.js';
import type { WorkflowCallbackWebhookBindingResponse } from '../models/WorkflowCallbackWebhookBindingResponse.js';
import type { WorkflowQueuedRunCancelRequest } from '../models/WorkflowQueuedRunCancelRequest.js';
import type { WorkflowQueuedRunCancelResponse } from '../models/WorkflowQueuedRunCancelResponse.js';
import type { WorkflowRunDiagnosticsResponse } from '../models/WorkflowRunDiagnosticsResponse.js';
import type { WorkflowRunResponse } from '../models/WorkflowRunResponse.js';
import type { WorkflowSchedulePreviewResponse } from '../models/WorkflowSchedulePreviewResponse.js';
import type { WorkflowScheduleReplayRequest } from '../models/WorkflowScheduleReplayRequest.js';
import type { WorkflowScheduleReplayResponse } from '../models/WorkflowScheduleReplayResponse.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class WorkflowsService {
  /**
   * Inspect scheduled workflow admission history
   * Started and skipped due minutes retained for 30 days. Requires app read access. History includes all linked tenants; optionally filter by tenant. No missed-minute catch-up is inferred.
   * @returns ListWorkflowScheduleOccurrencesResponse Newest nominal minutes first
   * @throws ApiError
   */
  public static listWorkflowScheduleOccurrences({
    slug,
    platformTenantId,
    cursor,
    limit = 100,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Only occurrences for this platform tenant within the app.
     */
    platformTenantId?: string,
    /**
     * next_cursor from the previous page. An expired cursor returns an empty page.
     */
    cursor?: string,
    /**
     * Maximum occurrences returned per page.
     */
    limit?: number,
  }): CancelablePromise<ListWorkflowScheduleOccurrencesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflows/schedules/occurrences',
      path: {
        'slug': slug,
      },
      query: {
        'platform_tenant_id': platformTenantId,
        'cursor': cursor,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Preview selected skipped schedule occurrence replays
   * Read-only advisory check of up to 20 retained skipped occurrences against the current live deployment, workflow definition, tenant schedule settings, overlap state, and app quota. Replay rechecks every condition.
   * @returns WorkflowScheduleReplayResponse Preview outcomes in chronological order.
   * @throws ApiError
   */
  public static previewWorkflowScheduleReplays({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: WorkflowScheduleReplayRequest,
  }): CancelablePromise<WorkflowScheduleReplayResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflows/schedules/occurrences:replay-preview',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Replay selected skipped schedule occurrences
   * Starts at most 20 selected skipped occurrences in chronological order, using the same live deployment and matching workflow definition, and the ordinary app quota and overlap checks. Each occurrence can create at most one replay run; blocked items are returned with their outcome.
   * @returns WorkflowScheduleReplayResponse Replay outcomes in chronological order.
   * @throws ApiError
   */
  public static replayWorkflowScheduleOccurrences({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: WorkflowScheduleReplayRequest,
  }): CancelablePromise<WorkflowScheduleReplayResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflows/schedules/occurrences:replay',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect deployed workflow schedules and their latest admission outcome.
   * Returns schedules from the default-scope live deployment, their next
   * nominal fire time and the most recent durable admission outcome.
   * When scheduling is unavailable, unavailable_reason explains why and
   * next_fire_at is omitted. Runtime availability reflects the API configuration;
   * operators must enable the same workflow gate on the scheduler.
   * Disabling or redeploying a schedule does not cancel existing runs.
   * No live default deployment returns an empty schedules array.
   *
   * @returns ListWorkflowSchedulesResponse Deployed schedules and runtime availability.
   * @throws ApiError
   */
  public static listWorkflowSchedules({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListWorkflowSchedulesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflows/schedules',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `Caller lacks the required read scope.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Preview a deployed workflow schedule.
   * Read-only simulation of upcoming local fire times and the next catch-up decision using the durable cursor. Fire times follow Gregale daylight-saving rules. No run is admitted or cursor changed.
   * @returns WorkflowSchedulePreviewResponse Preview evaluated for this app's active deployment.
   * @throws ApiError
   */
  public static getWorkflowSchedulePreview({
    slug,
    name,
    at,
    since,
    count = 5,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Schedule workflow name from the effective live deployment.
     */
    name: string,
    /**
     * RFC3339 time for evaluating future fire times and catch-up; defaults to the current time and is limited to five years from now.
     */
    at?: string,
    /**
     * Simulated prior evaluation time, useful for reviewing missed-fire catch-up. Defaults to the durable cursor.
     */
    since?: string,
    /**
     * Maximum number of future local fire times to include.
     */
    count?: number,
  }): CancelablePromise<WorkflowSchedulePreviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflows/schedules/{name}/preview',
      path: {
        'slug': slug,
        'name': name,
      },
      query: {
        'at': at,
        'since': since,
        'count': count,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: automation_version_conflict | automation_ownership_conflict — reload a stale revision or explicitly confirm transfer of YAML ownership.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Start a durable workflow run.
   * Snapshots the named effective workflow definition from the app's live
   * default deployment and dashboard publications and creates a pending run. The optional request body is
   * retained as the workflow input and may be any valid JSON value. An
   * optional Idempotency-Key binds this request to its original run for as
   * long as that run is retained. A matching retry returns the original
   * run, including its original definition snapshot; reusing the key with
   * different input returns 409.
   *
   * @returns WorkflowRunResponse The new pending workflow run.
   * @throws ApiError
   */
  public static createWorkflowRun({
    slug,
    name,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Workflow name from the app's effective published definitions.
     */
    name: string,
    /**
     * Stable caller key for retrying this run creation. Reuse only with the same workflow input.
     */
    idempotencyKey?: string,
    requestBody?: any,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflows/{name}/runs',
      path: {
        'slug': slug,
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
        402: `code: plan_workflows_not_allowed — this plan does not include durable workflows.`,
        403: `code: plan_workflows_quota | forbidden — the concurrent-run cap is exhausted or the caller lacks scope.`,
        404: `code: workflow_definition_not_found | app_not_found — the app or named live workflow definition does not exist.`,
        409: `Tenant-required apps need a tenant-scoped run route, or the Idempotency-Key was already used with different workflow input.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * List durable workflow runs for an app.
   * @returns ListWorkflowRunsResponse A page of workflow runs.
   * @throws ApiError
   */
  public static listWorkflowRuns({
    slug,
    status,
    workflowName,
    createdAfter,
    createdBefore,
    limit = 50,
    offset,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Optional exact status filter.
     */
    status?: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead',
    /**
     * Optional exact workflow name filter.
     */
    workflowName?: string,
    /**
     * Include runs created at or after this RFC3339 timestamp.
     */
    createdAfter?: string,
    /**
     * Include runs created at or before this RFC3339 timestamp.
     */
    createdBefore?: string,
    /**
     * Maximum runs to return in this page.
     */
    limit?: number,
    /**
     * Number of runs to skip before returning results.
     */
    offset?: number,
  }): CancelablePromise<ListWorkflowRunsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/workflows/runs',
      path: {
        'slug': slug,
      },
      query: {
        'status': status,
        'workflow_name': workflowName,
        'created_after': createdAfter,
        'created_before': createdBefore,
        'limit': limit,
        'offset': offset,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Preview cancellation eligibility for selected queued workflow runs.
   * Classifies up to 20 selected runs without changing them. A run is
   * eligible only while it is pending and has never started. This is an
   * advisory snapshot; the cancellation action rechecks eligibility while
   * holding the same run lock used by dispatch claims. Runs outside the app
   * are reported as not_found.
   *
   * @returns WorkflowQueuedRunCancelResponse One preview classification per selected run, in request order.
   * @throws ApiError
   */
  public static previewUnstartedWorkflowRunCancellations({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: WorkflowQueuedRunCancelRequest,
  }): CancelablePromise<WorkflowQueuedRunCancelResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflows/runs:cancel-preview',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Cancel selected queued workflow runs that have never started.
   * Atomically rechecks and cancels the selected runs that remain pending
   * and have no started_at timestamp. A run claimed after preview is
   * reported as already_started or not_queued and is left alone. Started
   * runs and retries are never cancelled by this bulk action. Eligible
   * runs transition to failed with cancelled_at set. The bounded batch is
   * committed as one transaction.
   *
   * @returns WorkflowQueuedRunCancelResponse One final outcome per selected run, in request order.
   * @throws ApiError
   */
  public static cancelUnstartedWorkflowRuns({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: WorkflowQueuedRunCancelRequest,
  }): CancelablePromise<WorkflowQueuedRunCancelResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflows/runs:cancel-queued',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Get a durable workflow run.
   * @returns WorkflowRunResponse The workflow run.
   * @throws ApiError
   */
  public static getWorkflowRun({
    id,
  }: {
    /**
     * Workflow-run identifier to retrieve.
     */
    id: string,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/workflows/runs/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * List the step attempts for a workflow run.
   * @returns ListWorkflowStepsResponse Ordered workflow step attempts.
   * @throws ApiError
   */
  public static listWorkflowSteps({
    id,
  }: {
    /**
     * Workflow-run identifier whose step attempts are returned.
     */
    id: string,
  }): CancelablePromise<ListWorkflowStepsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/workflows/runs/{id}/steps',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * List executor attempts for one workflow step.
   * @returns ListWorkflowStepAttemptsResponse Ordered executor attempts, including retry and condition-check outcomes.
   * @throws ApiError
   */
  public static listWorkflowStepAttempts({
    id,
    step,
  }: {
    /**
     * Parent run for the requested executor-attempt history.
     */
    id: string,
    /**
     * Workflow step name whose executor attempts are returned.
     */
    step: string,
  }): CancelablePromise<ListWorkflowStepAttemptsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/workflows/runs/{id}/steps/{step}/attempts',
      path: {
        'id': id,
        'step': step,
      },
      errors: {
        401: `code: unauthorized`,
        404: `The workflow run is absent or not owned by the caller, or code: workflow_step_not_found — the requested step is absent.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Retry one failed or dead HTTP step in its existing workflow run.
   * Requeues the same run and preserves the failed step's persisted input
   * and attempt history. Only terminal failed or dead HTTP steps are eligible. The
   * request conflicts if another step is active, failed, or dead, a downstream
   * step already succeeded, or the run was cancelled. Skipped dependent
   * steps are reopened so ordinary DAG evaluation can continue. Each manual
   * retry grants one new dispatch and does not reset the manifest's
   * automatic retry budget.
   *
   * @returns WorkflowRunResponse The workflow run has been requeued at the requested step.
   * @throws ApiError
   */
  public static retryWorkflowStep({
    id,
    step,
  }: {
    /**
     * Workflow run whose failed step should be retried.
     */
    id: string,
    /**
     * Failed HTTP step to resume in the existing run.
     */
    step: string,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/workflows/runs/{id}/steps/{step}/retry',
      path: {
        'id': id,
        'step': step,
      },
      errors: {
        401: `code: unauthorized`,
        402: `The account plan does not include workflows.`,
        403: `The account has reached its concurrent workflow run limit.`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        409: `The run or step is not in a state that can be safely retried.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * List callback handles for a workflow run.
   * Callback IDs identify waits but are not bearer credentials; completion requires authorization for the run owner.
   * @returns ListWorkflowCallbacksResponse Callback handles from the run's snapshotted definition.
   * @throws ApiError
   */
  public static listWorkflowCallbacks({
    id,
  }: {
    /**
     * Workflow-run identifier.
     */
    id: string,
  }): CancelablePromise<ListWorkflowCallbacksResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/workflows/runs/{id}/callbacks',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Complete one callback wait.
   * Requires the run owner's workflow-write authorization. An identical
   * retry returns duplicate=true, including after the run finishes; a
   * different payload conflicts. Completion may arrive before the step
   * parks and will be consumed when the step becomes runnable.
   *
   * @returns CompleteWorkflowCallbackResponse The callback was durably received or was an identical retry.
   * @throws ApiError
   */
  public static completeWorkflowCallback({
    id,
    callbackId,
    requestBody,
  }: {
    /**
     * Workflow run whose callback will be completed.
     */
    id: string,
    /**
     * Stable callback handle returned by the callback list endpoint.
     */
    callbackId: string,
    requestBody?: any,
  }): CancelablePromise<CompleteWorkflowCallbackResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/workflows/runs/{id}/callbacks/{callback_id}',
      path: {
        'id': id,
        'callback_id': callbackId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: workflow_run_not_found | workflow_step_not_found — the caller does not own the run or its callback handle is not declared.`,
        409: `Callback is closed or the ID was completed with different JSON.`,
        410: `Callback wait has expired.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Bind one verified Stripe object event to a workflow callback.
   * Requires account workflow-write authorization and a Stripe inbound
   * webhook endpoint owned by the same app. Repeating the identical
   * binding is safe; another callback cannot claim the same endpoint,
   * event type, and object ID. The endpoint's existing URL and signing
   * secret are reused.
   *
   * @returns WorkflowCallbackWebhookBindingResponse The durable binding, whether newly created or already present.
   * @throws ApiError
   */
  public static putWorkflowCallbackWebhookBinding({
    id,
    callbackId,
    requestBody,
  }: {
    /**
     * Workflow run that owns the callback binding.
     */
    id: string,
    /**
     * Stable callback handle to bind to one verified provider event.
     */
    callbackId: string,
    requestBody: CreateWorkflowCallbackWebhookBindingRequest,
  }): CancelablePromise<WorkflowCallbackWebhookBindingResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/workflows/runs/{id}/callbacks/{callback_id}/webhook-binding',
      path: {
        'id': id,
        'callback_id': callbackId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        409: `The callback is closed, or the callback/provider event already has a different binding.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Read the callback's verified webhook binding.
   * @returns WorkflowCallbackWebhookBindingResponse The bound provider event. No endpoint URL or secret is returned.
   * @throws ApiError
   */
  public static getWorkflowCallbackWebhookBinding({
    id,
    callbackId,
  }: {
    /**
     * Workflow run that owns the callback binding.
     */
    id: string,
    /**
     * Stable callback handle to bind to one verified provider event.
     */
    callbackId: string,
  }): CancelablePromise<WorkflowCallbackWebhookBindingResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/workflows/runs/{id}/callbacks/{callback_id}/webhook-binding',
      path: {
        'id': id,
        'callback_id': callbackId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Stop routing the provider event to this callback.
   * Later verified provider events resume ordinary app delivery; an already verified in-flight request may still complete the callback.
   * @returns void
   * @throws ApiError
   */
  public static deleteWorkflowCallbackWebhookBinding({
    id,
    callbackId,
  }: {
    /**
     * Workflow run that owns the callback binding.
     */
    id: string,
    /**
     * Stable callback handle to bind to one verified provider event.
     */
    callbackId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/workflows/runs/{id}/callbacks/{callback_id}/webhook-binding',
      path: {
        'id': id,
        'callback_id': callbackId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Deliver an external event to a waiting workflow run.
   * @returns InjectWorkflowEventResponse The event was recorded.
   * @throws ApiError
   */
  public static injectWorkflowEvent({
    id,
    requestBody,
  }: {
    /**
     * Workflow-run identifier that receives the event.
     */
    id: string,
    requestBody: InjectWorkflowEventRequest,
  }): CancelablePromise<InjectWorkflowEventResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/workflows/runs/{id}/events',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        409: `code: workflow_not_running — only running or awaiting_event runs accept events.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect a workflow run and preview its safe continuation.
   * Requires the normal account read scope and MFA.
   * Returns a consistent read-only snapshot with queue reason, step states,
   * code identity and the existing resume planner. No actions, admission
   * reservations or audit mutations occur. Capacity and resume generation
   * are checked again by POST resume. Inputs, outputs, error text, tenant
   * identities and credentials are omitted. Responses use Cache-Control: no-store.
   *
   * @returns WorkflowRunDiagnosticsResponse Current diagnostics and an advisory resume preview, including blockers for ineligible runs.
   * @throws ApiError
   */
  public static getWorkflowRunDiagnostics({
    id,
  }: {
    /**
     * Durable workflow-run identifier.
     */
    id: string,
  }): CancelablePromise<WorkflowRunDiagnosticsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/workflows/runs/{id}/diagnostics',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Resume eligible failed actions in a durable workflow run.
   * Reopens eligible failed actions with a fresh retry budget while preserving
   * the original definition, inputs, successful steps, batch results, guard
   * decisions, action idempotency keys and attempt history. Requires the
   * current resume_count and honors Idempotency-Key for request replay.
   * Cancelled runs, active calls or waits, executed failure/timeout handlers,
   * failed control steps and replay-unsafe integration mutations are rejected.
   * A live default deployment and valid integration bindings are required.
   * Each run permits at most 16 resumptions and counts against active-run quotas.
   *
   * @returns WorkflowRunResponse The queued workflow run with its incremented resume_count.
   * @throws ApiError
   */
  public static resumeWorkflowRun({
    id,
    requestBody,
  }: {
    /**
     * Failed or dead workflow-run identifier to resume.
     */
    id: string,
    requestBody: ResumeWorkflowRunRequest,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/workflows/runs/{id}/resume',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_workflows_not_allowed — this plan does not include workflows.`,
        403: `code: plan_workflows_quota | forbidden — active-run quota exhausted or required scope missing.`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        409: `code: workflow_resume_conflict, workflow_resume_unsafe, or workflow_resume_limit.`,
        413: `code: request_body_too_large — resume requests are limited to 4096 bytes.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * List the recorded resumptions of a workflow run.
   * @returns ListWorkflowResumesResponse Resume history in increasing resume_number order, bounded to 16 records.
   * @throws ApiError
   */
  public static listWorkflowResumes({
    id,
  }: {
    /**
     * Workflow-run identifier whose continuation history is requested.
     */
    id: string,
  }): CancelablePromise<ListWorkflowResumesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/workflows/runs/{id}/resumes',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Cancel a durable workflow run.
   * Marks a non-terminal run failed with an operator-cancelled error and
   * skips its pending, running, and waiting steps. Repeating the request on
   * a terminal run returns the existing run.
   *
   * @returns WorkflowRunResponse The terminal workflow run.
   * @throws ApiError
   */
  public static cancelWorkflowRun({
    id,
  }: {
    /**
     * Workflow-run identifier to cancel.
     */
    id: string,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/workflows/runs/{id}/cancel',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
}
