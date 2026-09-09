/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InjectWorkflowEventRequest } from '../models/InjectWorkflowEventRequest.js';
import type { InjectWorkflowEventResponse } from '../models/InjectWorkflowEventResponse.js';
import type { ListWorkflowRunsResponse } from '../models/ListWorkflowRunsResponse.js';
import type { ListWorkflowStepsResponse } from '../models/ListWorkflowStepsResponse.js';
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
        503: `code: capacity — server-side error; retry with backoff.`,
      },
    });
  }
}
