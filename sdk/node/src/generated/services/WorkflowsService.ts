/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationHealthResponse } from '../models/AutomationHealthResponse.js';
import type { AutomationResponse } from '../models/AutomationResponse.js';
import type { AutomationRevisionResponse } from '../models/AutomationRevisionResponse.js';
import type { CompleteWorkflowCallbackResponse } from '../models/CompleteWorkflowCallbackResponse.js';
import type { CreateWorkflowCallbackWebhookBindingRequest } from '../models/CreateWorkflowCallbackWebhookBindingRequest.js';
import type { InjectWorkflowEventRequest } from '../models/InjectWorkflowEventRequest.js';
import type { InjectWorkflowEventResponse } from '../models/InjectWorkflowEventResponse.js';
import type { ListAutomationRevisionsResponse } from '../models/ListAutomationRevisionsResponse.js';
import type { ListAutomationsResponse } from '../models/ListAutomationsResponse.js';
import type { ListTenantWorkflowSchedulesResponse } from '../models/ListTenantWorkflowSchedulesResponse.js';
import type { ListWorkflowCallbacksResponse } from '../models/ListWorkflowCallbacksResponse.js';
import type { ListWorkflowResumesResponse } from '../models/ListWorkflowResumesResponse.js';
import type { ListWorkflowRunsResponse } from '../models/ListWorkflowRunsResponse.js';
import type { ListWorkflowScheduleOccurrencesResponse } from '../models/ListWorkflowScheduleOccurrencesResponse.js';
import type { ListWorkflowSchedulesResponse } from '../models/ListWorkflowSchedulesResponse.js';
import type { ListWorkflowStepAttemptsResponse } from '../models/ListWorkflowStepAttemptsResponse.js';
import type { ListWorkflowStepsResponse } from '../models/ListWorkflowStepsResponse.js';
import type { PublishAutomationRequest } from '../models/PublishAutomationRequest.js';
import type { RestoreAutomationRevisionRequest } from '../models/RestoreAutomationRevisionRequest.js';
import type { ResumeWorkflowRunRequest } from '../models/ResumeWorkflowRunRequest.js';
import type { SaveAutomationDraftRequest } from '../models/SaveAutomationDraftRequest.js';
import type { SetAutomationEnabledRequest } from '../models/SetAutomationEnabledRequest.js';
import type { SimulateAutomationRequest } from '../models/SimulateAutomationRequest.js';
import type { SimulateAutomationResponse } from '../models/SimulateAutomationResponse.js';
import type { TenantWorkflowScheduleResponse } from '../models/TenantWorkflowScheduleResponse.js';
import type { UpdateTenantWorkflowScheduleRequest } from '../models/UpdateTenantWorkflowScheduleRequest.js';
import type { ValidateAutomationRequest } from '../models/ValidateAutomationRequest.js';
import type { ValidateAutomationResponse } from '../models/ValidateAutomationResponse.js';
import type { WorkflowCallbackWebhookBindingResponse } from '../models/WorkflowCallbackWebhookBindingResponse.js';
import type { WorkflowQueuedRunCancelRequest } from '../models/WorkflowQueuedRunCancelRequest.js';
import type { WorkflowQueuedRunCancelResponse } from '../models/WorkflowQueuedRunCancelResponse.js';
import type { WorkflowRunDiagnosticsResponse } from '../models/WorkflowRunDiagnosticsResponse.js';
import type { WorkflowRunResponse } from '../models/WorkflowRunResponse.js';
import type { WorkflowSchedulePreviewResponse } from '../models/WorkflowSchedulePreviewResponse.js';
import type { WorkflowScheduleReplayRequest } from '../models/WorkflowScheduleReplayRequest.js';
import type { WorkflowScheduleReplayResponse } from '../models/WorkflowScheduleReplayResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class WorkflowsService {
  /**
   * List schedules this tenant may customize for a linked app.
   * Requires platform_tenant:automations:read. Results include only live published schedule triggers that the app owner marked tenant_configurable. Version zero means the tenant still uses the published defaults.
   * @returns ListTenantWorkflowSchedulesResponse Tenant-visible schedules and their current versions.
   * @throws ApiError
   */
  public static listPlatformTenantSelfWorkflowSchedules({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListTenantWorkflowSchedulesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/apps/{slug}/workflows/schedules',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
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
   * Preview fire times and catch-up for this tenant's schedule.
   * Read-only simulation using this tenant's effective schedule and durable cursor. Fire times follow Gregale daylight-saving rules. No run is admitted or cursor changed.
   * @returns WorkflowSchedulePreviewResponse Read-only schedule simulation.
   * @throws ApiError
   */
  public static getPlatformTenantSelfWorkflowSchedulePreview({
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
     * Tenant-configurable schedule workflow from the live deployment.
     */
    name: string,
    /**
     * Hypothetical evaluator time in RFC3339; defaults to now and is bounded to five years in either direction.
     */
    at?: string,
    /**
     * Simulated prior evaluation time, useful for reviewing missed-fire catch-up. Defaults to the durable tenant cursor.
     */
    since?: string,
    /**
     * Upcoming occurrences to return.
     */
    count?: number,
  }): CancelablePromise<WorkflowSchedulePreviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/apps/{slug}/workflows/schedules/{name}/preview',
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Set this tenant's cadence for an opted-in workflow.
   * Requires platform_tenant:automations:manage. expected_version is zero
   * to create a tenant override and otherwise must match the current version.
   * A stale version returns 409. Omitting timezone or overlap keeps the
   * published defaults; enabled defaults to true. The tenant controls only
   * its own cadence, timezone, overlap behavior, and enabled state. Input,
   * workflow steps, credentials, and app concurrency remain app-owned.
   * Existing runs keep their admitted definition.
   *
   * @returns TenantWorkflowScheduleResponse The updated tenant schedule and its new version.
   * @throws ApiError
   */
  public static updatePlatformTenantSelfWorkflowSchedule({
    slug,
    name,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Name of an opted-in schedule workflow from the live deployment.
     */
    name: string,
    /**
     * The tenant's complete schedule settings and the version currently observed by the caller.
     */
    requestBody: UpdateTenantWorkflowScheduleRequest,
  }): CancelablePromise<TenantWorkflowScheduleResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/platform-tenant-self/apps/{slug}/workflows/schedules/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
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
   * Get bounded execution health for one automation.
   * Returns run counts by status, the completed-run success rate, median
   * and p95 duration, recent run identities and the most common failed
   * steps. Inputs, outputs, and error text are never included. The default
   * window is the previous seven days; the maximum window is 30 days.
   * created_after and created_before are inclusive RFC3339 timestamps, and
   * created_before may not be in the future. Failed loop items are grouped
   * under their parent step; at most ten failed steps are returned.
   *
   * @returns AutomationHealthResponse Safe operational summary for the selected automation and time window.
   * @throws ApiError
   */
  public static getAutomationHealth({
    slug,
    name,
    createdAfter,
    createdBefore,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Automation whose recent execution health is summarized.
     */
    name: string,
    /**
     * Inclusive start of the summary window; defaults to seven days before created_before or now.
     */
    createdAfter?: string,
    /**
     * Inclusive end of the summary window; defaults to now.
     */
    createdBefore?: string,
  }): CancelablePromise<AutomationHealthResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/automations/{name}/health',
      path: {
        'slug': slug,
        'name': name,
      },
      query: {
        'created_after': createdAfter,
        'created_before': createdBefore,
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
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * List published automation revisions, newest first.
   * @returns ListAutomationRevisionsResponse Immutable published definitions and pagination metadata.
   * @throws ApiError
   */
  public static listAutomationRevisions({
    slug,
    name,
    limit = 50,
    offset,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Automation whose immutable published revisions are listed.
     */
    name: string,
    /**
     * Maximum number of revisions to return (1–100; defaults to 50).
     */
    limit?: number,
    /**
     * Number of newest revisions to skip before returning results (maximum 2147483647).
     */
    offset?: number,
  }): CancelablePromise<ListAutomationRevisionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/automations/{name}/revisions',
      path: {
        'slug': slug,
        'name': name,
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
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Read one immutable published automation revision.
   * @returns AutomationRevisionResponse The selected published definition and its canonical hash.
   * @throws ApiError
   */
  public static getAutomationRevision({
    slug,
    name,
    version,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Automation that owns the requested published revision.
     */
    name: string,
    /**
     * Published revision identifier returned by the revision list.
     */
    version: number,
  }): CancelablePromise<AutomationRevisionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/automations/{name}/revisions/{version}',
      path: {
        'slug': slug,
        'name': name,
        'version': version,
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
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Restore a published revision as a new draft using optimistic version checking.
   * Copies the selected immutable revision into the automation's draft. It
   * does not publish the copy or change running and accepted workflows.
   * Send expected_version from the latest automation read; use zero when
   * creating a draft after the automation was deleted. YAML ownership still
   * requires explicit takeover when the restored draft is later published.
   *
   * @returns AutomationResponse The updated automation draft; publishing remains a separate operation.
   * @throws ApiError
   */
  public static restoreAutomationRevision({
    slug,
    name,
    version,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Automation that receives the restored draft.
     */
    name: string,
    /**
     * Published revision to copy into the current draft.
     */
    version: number,
    requestBody: RestoreAutomationRevisionRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AutomationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/automations/{name}/revisions/{version}/restore',
      path: {
        'slug': slug,
        'name': name,
        'version': version,
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
   * Preview a deployed workflow schedule.
   * Read-only simulation of upcoming local fire times and the next catch-up decision using the durable cursor. Fire times follow Gregale daylight-saving rules. No run is admitted or cursor changed.
   * @returns WorkflowSchedulePreviewResponse Read-only schedule simulation.
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
     * Hypothetical evaluator time in RFC3339; defaults to now and is bounded to five years in either direction.
     */
    at?: string,
    /**
     * Simulated prior evaluation time, useful for reviewing missed-fire catch-up. Defaults to the durable cursor.
     */
    since?: string,
    /**
     * Upcoming occurrences to return.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Start a durable workflow run for an account-owned tenant.
   * Account owners can start a run for a tenant linked to the app through an
   * active API consumer or active tenant surface. The tenant ID is persisted
   * and propagated through trusted internal step dispatch metadata. Managed
   * operation steps may deliver effects only to an explicitly subscribed
   * receiver owned by that same tenant. Outbound steps can use an existing
   * app-bound customer-managed integration; the signed run identity and
   * active tenant-to-app link are checked at dispatch and outbound
   * authorization. Integration credentials and route policies remain
   * app-scoped and shared across tenants. Tenant-bound runs can wait for
   * externally supplied events and callbacks through the authenticated
   * tenant-self continuation routes. Schedule-triggered workflows on tenant-required apps
   * are admitted once per active linked tenant from the live default publication.
   *
   * @returns WorkflowRunResponse The new pending tenant-scoped workflow run.
   * @throws ApiError
   */
  public static createTenantWorkflowRun({
    tenantId,
    slug,
    name,
    requestBody,
  }: {
    /**
     * Customer record to bind to the new workflow execution.
     */
    tenantId: string,
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Name of the published workflow definition to run.
     */
    name: string,
    requestBody?: any,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/platform-tenants/{tenant_id}/apps/{slug}/workflows/{name}/runs',
      path: {
        'tenant_id': tenantId,
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `The account plan does not include tenant-scoped durable workflow runs.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The app is not configured to accept tenant-scoped workflow runs.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Start a durable workflow run as the authenticated platform tenant.
   * Uses the tenant ID from the authenticated platform tenant access token.
   * The tenant must be actively linked to the app. Requires the
   * platform_tenant:invocations:manage scope. Managed operation effects are
   * delivered only to an explicitly subscribed receiver owned by this
   * tenant. Outbound steps can use an existing app-bound customer-managed
   * integration; the signed run identity and active tenant-to-app link are
   * checked at dispatch and outbound authorization. Integration credentials
   * and route policies remain app-scoped and shared across tenants. This
   * tenant-self API also supports authenticated event waits and callbacks.
   * Schedule-triggered workflows are admitted independently for each active
   * tenant link. The published cadence is the default; triggers marked
   * tenant_configurable can be overridden through the tenant-self schedule
   * API without changing workflow input or steps.
   *
   * @returns WorkflowRunResponse A pending workflow run created with the authenticated tenant identity.
   * @throws ApiError
   */
  public static createPlatformTenantSelfWorkflowRun({
    slug,
    name,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Identifier of the workflow definition to start for this tenant.
     */
    name: string,
    requestBody?: any,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/apps/{slug}/workflows/{name}/runs',
      path: {
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The app does not accept workflow runs for this authenticated tenant identity.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * List durable workflow runs for the authenticated tenant in an app.
   * Requires platform_tenant:invocations:read. The tenant must be actively
   * linked to the app. Results are filtered by the authenticated tenant in
   * storage, include only that tenant's runs, and use the same filters and
   * offset pagination as the account-owned app run history endpoint.
   * Foreign or unbound apps return 404.
   *
   * @returns ListWorkflowRunsResponse A page of workflow runs owned by the authenticated tenant.
   * @throws ApiError
   */
  public static listPlatformTenantSelfWorkflowRuns({
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
     * Return only runs in this lifecycle state.
     */
    status?: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead',
    /**
     * Restrict results to one workflow name.
     */
    workflowName?: string,
    /**
     * Keep runs created at or after this RFC3339 timestamp.
     */
    createdAfter?: string,
    /**
     * Keep runs created at or before this RFC3339 timestamp.
     */
    createdBefore?: string,
    /**
     * Maximum tenant-owned runs to include in this response.
     */
    limit?: number,
    /**
     * Number of matching tenant-owned runs to skip.
     */
    offset?: number,
  }): CancelablePromise<ListWorkflowRunsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/apps/{slug}/workflows/runs',
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
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
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
   * Read this tenant's workflow run status and result.
   * Requires a tenant-bound token with platform_tenant:invocations:read. Foreign, unbound, and missing runs return the same 404.
   * @returns WorkflowRunResponse Workflow status and result for the authenticated tenant.
   * @throws ApiError
   */
  public static getPlatformTenantSelfWorkflowRun({
    id,
  }: {
    /**
     * Tenant-owned workflow run returned when starting the workflow.
     */
    id: string,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflows/runs/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * List callback handles for this tenant's workflow run.
   * Requires platform_tenant:invocations:read. Callback IDs identify waits but are not bearer credentials. Foreign, unbound, inactive-link, and missing runs return the same 404.
   * @returns ListWorkflowCallbacksResponse The run's declared callback waits and their handles.
   * @throws ApiError
   */
  public static listPlatformTenantSelfWorkflowCallbacks({
    id,
  }: {
    /**
     * Run whose callback waits are visible to the authenticated tenant.
     */
    id: string,
  }): CancelablePromise<ListWorkflowCallbacksResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflows/runs/{id}/callbacks',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Complete a callback wait for this tenant's workflow run.
   * Requires platform_tenant:invocations:manage. The callback ID is not a
   * credential; the authenticated tenant identity and active tenant-to-app
   * link are checked at the durable write. An identical retry returns
   * duplicate=true, including after the run finishes; a different payload
   * conflicts. Completion may arrive before the step parks and will be
   * consumed when the step becomes runnable. Foreign, unbound, inactive-link,
   * and missing runs return the same 404.
   *
   * @returns CompleteWorkflowCallbackResponse Receipt for a new callback value or an identical retry.
   * @throws ApiError
   */
  public static completePlatformTenantSelfWorkflowCallback({
    id,
    callbackId,
    requestBody,
  }: {
    /**
     * Tenant-owned run receiving this callback.
     */
    id: string,
    /**
     * UUID handle for the declared callback wait in this run.
     */
    callbackId: string,
    requestBody?: any,
  }): CancelablePromise<CompleteWorkflowCallbackResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflows/runs/{id}/callbacks/{callback_id}',
      path: {
        'id': id,
        'callback_id': callbackId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: workflow_run_not_found | workflow_step_not_found — the tenant cannot access the run or its callback handle is not declared.`,
        409: `The wait is closed or this handle already contains a different JSON value.`,
        410: `The configured callback wait timeout has elapsed.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Deliver an external event to this tenant's waiting workflow run.
   * Requires platform_tenant:invocations:manage. The authenticated tenant identity and active tenant-to-app link are checked at the durable write. Foreign, unbound, inactive-link, and missing runs return the same 404.
   * @returns InjectWorkflowEventResponse The run accepted the event for processing.
   * @throws ApiError
   */
  public static injectPlatformTenantSelfWorkflowEvent({
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Tenant-owned run receiving the event.
     */
    id: string,
    requestBody: InjectWorkflowEventRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<InjectWorkflowEventResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflows/runs/{id}/events',
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
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        409: `code: workflow_not_running — only active runs accept events.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Cancel this tenant's active workflow run.
   * Requires a tenant-bound token with platform_tenant:invocations:manage. Repeating the request on a terminal run returns its current state.
   * @returns WorkflowRunResponse Workflow status after the cancellation request.
   * @throws ApiError
   */
  public static cancelPlatformTenantSelfWorkflowRun({
    id,
  }: {
    /**
     * Tenant-owned workflow run to cancel.
     */
    id: string,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflows/runs/{id}/cancel',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Inspect this tenant's workflow run and preview its safe continuation.
   * Requires a tenant-bound token with platform_tenant:invocations:read.
   * Foreign, unbound and missing runs return the same 404.
   * Returns a consistent read-only snapshot with queue reason, step states,
   * code identity and the existing resume planner. No actions, admission
   * reservations or audit mutations occur. Capacity and resume generation
   * are checked again by POST resume. Inputs, outputs, error text, tenant
   * identities and credentials are omitted. Responses use Cache-Control: no-store.
   *
   * @returns WorkflowRunDiagnosticsResponse Diagnostics and continuation blockers for the authenticated tenant's run.
   * @throws ApiError
   */
  public static getPlatformTenantSelfWorkflowRunDiagnostics({
    id,
  }: {
    /**
     * Workflow-run identifier restricted to the authenticated tenant.
     */
    id: string,
  }): CancelablePromise<WorkflowRunDiagnosticsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/platform-tenant-self/workflows/runs/{id}/diagnostics',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
  /**
   * Resume eligible failed actions in this tenant's workflow run.
   * Requires a tenant-bound token with platform_tenant:invocations:manage.
   * Send the current resume_count. The platform preserves completed work and
   * only reopens actions accepted by the workflow's safe-resume rules.
   * Foreign, unbound, and missing runs return the same 404.
   *
   * @returns WorkflowRunResponse The queued run with its incremented resume_count.
   * @throws ApiError
   */
  public static resumePlatformTenantSelfWorkflowRun({
    id,
    requestBody,
  }: {
    /**
     * Failed or dead workflow run owned by the authenticated tenant.
     */
    id: string,
    requestBody: ResumeWorkflowRunRequest,
  }): CancelablePromise<WorkflowRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/platform-tenant-self/workflows/runs/{id}/resume',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `The account plan does not include durable workflows.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        409: `The run is stale, unsafe to resume, or has reached its resume limit.`,
        413: `Resume requests are limited to 4096 bytes.`,
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
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
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
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
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
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
        404: `code: workflow_run_not_found | workflow_step_not_found — the caller does not own the run or its callback handle is not declared.`,
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
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
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
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
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
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
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
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
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
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
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
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
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
        404: `code: workflow_run_not_found — the run is absent or outside the caller's workflow access.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
}
