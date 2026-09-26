/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CompleteWorkflowCallbackResponse } from '../models/CompleteWorkflowCallbackResponse.js';
import type { CreateWorkflowCallbackWebhookBindingRequest } from '../models/CreateWorkflowCallbackWebhookBindingRequest.js';
import type { InjectWorkflowEventRequest } from '../models/InjectWorkflowEventRequest.js';
import type { InjectWorkflowEventResponse } from '../models/InjectWorkflowEventResponse.js';
import type { ListWorkflowCallbacksResponse } from '../models/ListWorkflowCallbacksResponse.js';
import type { ListWorkflowRunsResponse } from '../models/ListWorkflowRunsResponse.js';
import type { ListWorkflowStepAttemptsResponse } from '../models/ListWorkflowStepAttemptsResponse.js';
import type { ListWorkflowStepsResponse } from '../models/ListWorkflowStepsResponse.js';
import type { WorkflowCallbackWebhookBindingResponse } from '../models/WorkflowCallbackWebhookBindingResponse.js';
import type { WorkflowRunResponse } from '../models/WorkflowRunResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class WorkflowsService {
  /**
   * Start a durable workflow run.
   * Snapshots the named workflow definition from the app's current live
   * deployment and creates a pending run. The optional request body is
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
     * Workflow name from the app's current live deployment.
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
