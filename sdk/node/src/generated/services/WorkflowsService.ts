/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationResponse } from '../models/AutomationResponse.js';
import type { CompleteWorkflowCallbackResponse } from '../models/CompleteWorkflowCallbackResponse.js';
import type { CreateWorkflowCallbackWebhookBindingRequest } from '../models/CreateWorkflowCallbackWebhookBindingRequest.js';
import type { InjectWorkflowEventRequest } from '../models/InjectWorkflowEventRequest.js';
import type { InjectWorkflowEventResponse } from '../models/InjectWorkflowEventResponse.js';
import type { ListAutomationsResponse } from '../models/ListAutomationsResponse.js';
import type { ListWorkflowCallbacksResponse } from '../models/ListWorkflowCallbacksResponse.js';
import type { ListWorkflowResumesResponse } from '../models/ListWorkflowResumesResponse.js';
import type { ListWorkflowRunsResponse } from '../models/ListWorkflowRunsResponse.js';
import type { ListWorkflowSchedulesResponse } from '../models/ListWorkflowSchedulesResponse.js';
import type { ListWorkflowStepAttemptsResponse } from '../models/ListWorkflowStepAttemptsResponse.js';
import type { ListWorkflowStepsResponse } from '../models/ListWorkflowStepsResponse.js';
import type { PublishAutomationRequest } from '../models/PublishAutomationRequest.js';
import type { ResumeWorkflowRunRequest } from '../models/ResumeWorkflowRunRequest.js';
import type { SaveAutomationDraftRequest } from '../models/SaveAutomationDraftRequest.js';
import type { SetAutomationEnabledRequest } from '../models/SetAutomationEnabledRequest.js';
import type { SimulateAutomationRequest } from '../models/SimulateAutomationRequest.js';
import type { SimulateAutomationResponse } from '../models/SimulateAutomationResponse.js';
import type { ValidateAutomationRequest } from '../models/ValidateAutomationRequest.js';
import type { ValidateAutomationResponse } from '../models/ValidateAutomationResponse.js';
import type { WorkflowCallbackWebhookBindingResponse } from '../models/WorkflowCallbackWebhookBindingResponse.js';
import type { WorkflowRunResponse } from '../models/WorkflowRunResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class WorkflowsService {
  /**
   * List drafts and published automations, including YAML definitions.
   * @returns ListAutomationsResponse listAutomations result.
   * @throws ApiError
   */
  public static listAutomations({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListAutomationsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/automations',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: automation_version_conflict | automation_ownership_conflict — reload a stale revision or explicitly confirm transfer of YAML ownership.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: automation_invalid | validation_failed — the definition, revision, or request fields are invalid.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Simulate automation data flow with sample input and successful action mocks.
   * Returns a deterministic hypothetical trace without saving a definition,
   * creating runs, invoking handlers, publishing events or calling integrations.
   * Requires ownership, MFA and read scope. Works without a live deployment
   * or enabled workflow runtime. Definition validation uses the account plan
   * and checks managed integration bindings without opening credentials.
   * Missing action results block dependent steps. Waits remain unresolved;
   * failure and timeout outcomes cannot be injected. Action mocks using the
   * reserved exact {"timeout":true} output with an on_timeout route return
   * 400. Loop mocks form a
   * sequential prefix. A complete trace means all roots resolved or skipped
   * under the supplied successful mocks, not that live execution will succeed.
   * Limits: 3 MiB request, 1 MiB definition and each sample value, 128 roots,
   * 1024 trace entries and 4 MiB response, plus existing loop bounds.
   * Invalid definitions return 200 with definition_valid=false and no trace.
   *
   * @returns SimulateAutomationResponse Definition validation and the bounded hypothetical execution trace.
   * @throws ApiError
   */
  public static simulateAutomation({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Candidate definition, workflow input and hypothetical successful action results.
     */
    requestBody: SimulateAutomationRequest,
  }): CancelablePromise<SimulateAutomationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/automations:simulate',
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
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Validate a definition without saving it or executing any steps.
   * @returns ValidateAutomationResponse validateAutomation result.
   * @throws ApiError
   */
  public static validateAutomation({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: ValidateAutomationRequest,
  }): CancelablePromise<ValidateAutomationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/automations:validate',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: automation_version_conflict | automation_ownership_conflict — reload a stale revision or explicitly confirm transfer of YAML ownership.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: automation_invalid | validation_failed — the definition, revision, or request fields are invalid.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Read an automation draft and its published definition.
   * @returns AutomationResponse getAutomation result.
   * @throws ApiError
   */
  public static getAutomation({
    slug,
    name,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Automation name, shared with workflow run endpoints.
     */
    name: string,
  }): CancelablePromise<AutomationResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/automations/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: automation_version_conflict | automation_ownership_conflict — reload a stale revision or explicitly confirm transfer of YAML ownership.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: automation_invalid | validation_failed — the definition, revision, or request fields are invalid.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Save a draft with optimistic version checking; running definitions stay unchanged.
   * @returns AutomationResponse saveAutomationDraft result.
   * @throws ApiError
   */
  public static saveAutomationDraft({
    slug,
    name,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Automation name, shared with workflow run endpoints.
     */
    name: string,
    requestBody: SaveAutomationDraftRequest,
  }): CancelablePromise<AutomationResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/automations/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: automation_version_conflict | automation_ownership_conflict — reload a stale revision or explicitly confirm transfer of YAML ownership.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: automation_invalid | validation_failed — the definition, revision, or request fields are invalid.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Remove a dashboard definition; restoring YAML ownership requires explicit confirmation.
   * @returns void
   * @throws ApiError
   */
  public static deleteAutomation({
    slug,
    name,
    expectedVersion,
    restoreManifest = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Automation name, shared with workflow run endpoints.
     */
    name: string,
    /**
     * Revision returned by the most recent read or save.
     */
    expectedVersion: number,
    /**
     * Explicitly return ownership to the current YAML definition after deletion.
     */
    restoreManifest?: boolean,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/automations/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      query: {
        'expected_version': expectedVersion,
        'restore_manifest': restoreManifest,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: automation_version_conflict | automation_ownership_conflict — reload a stale revision or explicitly confirm transfer of YAML ownership.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: automation_invalid | validation_failed — the definition, revision, or request fields are invalid.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Validate and publish the saved draft; taking over a YAML name requires explicit confirmation.
   * Published definitions survive future YAML deployments. A live default deployment is required. Runtime execution also requires FAAS_WORKFLOWS_ENABLED on apid and schedd. Events accepted before publication retain their captured definition.
   * @returns AutomationResponse publishAutomation result.
   * @throws ApiError
   */
  public static publishAutomation({
    slug,
    name,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Automation whose saved draft will be published.
     */
    name: string,
    requestBody: PublishAutomationRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AutomationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/automations/{name}/publish',
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
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: automation_version_conflict | automation_ownership_conflict — reload a stale revision or explicitly confirm transfer of YAML ownership.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: automation_invalid | validation_failed — the definition, revision, or request fields are invalid.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Pause or resume automatic starts without cancelling existing runs.
   * @returns AutomationResponse setAutomationEnabled result.
   * @throws ApiError
   */
  public static setAutomationEnabled({
    slug,
    name,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Automation whose automatic starts will be paused or resumed.
     */
    name: string,
    requestBody: SetAutomationEnabledRequest,
  }): CancelablePromise<AutomationResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/automations/{name}/enabled',
      path: {
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: automation_version_conflict | automation_ownership_conflict — reload a stale revision or explicitly confirm transfer of YAML ownership.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: automation_invalid | validation_failed — the definition, revision, or request fields are invalid.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect deployed workflow schedules and their latest admission outcome.
   * Returns schedules from the default-scope live deployment, their next
   * nominal fire time and the most recent durable admission outcome.
   * When scheduling is unavailable, unavailable_reason explains why and
   * next_fire_at is omitted. Runtime availability reflects apid configuration;
   * operators must enable the same workflow gate on schedd.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Start a durable workflow run.
   * Snapshots the named effective workflow definition from the app's live
   * default deployment and dashboard publications and creates a pending run. The optional request body is
   * retained as the workflow input and may be any valid JSON value.
   *
   * @returns WorkflowRunResponse The new pending workflow run.
   * @throws ApiError
   */
  public static createWorkflowRun({
    slug,
    name,
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
    requestBody?: any,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/workflows/{name}/runs',
      path: {
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_workflows_not_allowed — this plan does not include durable workflows.`,
        403: `code: plan_workflows_quota | forbidden — the concurrent-run cap is exhausted or the caller lacks scope.`,
        404: `code: workflow_definition_not_found | app_not_found — the app or named live workflow definition does not exist.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        'limit': limit,
        'offset': offset,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        409: `The run or step is not in a state that can be safely retried.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * List callback handles for a workflow run.
   * Callback IDs identify waits but are not bearer credentials; completion requires account authorization.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        409: `Callback is closed or the ID was completed with different JSON.`,
        410: `Callback wait has expired.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        409: `The callback is closed, or the callback/provider event already has a different binding.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        409: `code: workflow_not_running — only running or awaiting_event runs accept events.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        409: `code: workflow_resume_conflict, workflow_resume_unsafe, or workflow_resume_limit.`,
        413: `code: request_body_too_large — resume requests are limited to 4096 bytes.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or belongs to another account.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
}
