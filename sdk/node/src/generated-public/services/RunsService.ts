/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateManagedExecutionWorkflowRequest } from '../models/CreateManagedExecutionWorkflowRequest.js';
import type { ExecutionWorkflowResponse } from '../models/ExecutionWorkflowResponse.js';
import type { ManagedExecutionWorkflowResponse } from '../models/ManagedExecutionWorkflowResponse.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class RunsService {
  /**
   * Submit a server-managed Runs DAG.
   * Stores a bounded dependency graph encrypted with the host age identity
   * and admits each step as a separate disposable Run after its dependencies
   * succeed. Independent steps may run in parallel up to the submitted
   * workflow limit and the account's concurrent Run limit. Continuation is
   * owned by the control plane and survives CLI disconnects and the API
   * restarts. Source and input are never returned; the encrypted plan is
   * erased when the workflow reaches a terminal state. Requires
   * `runs:write`, `deploy:write`, or `admin` and an enabled Runs control
   * plane with the host age recipient and identity configured. Each account
   * may have at most 16 active managed workflows; terminal workflows
   * release their encrypted plan and queue slot.
   *
   * @returns ManagedExecutionWorkflowResponse Encrypted DAG accepted for control-plane continuation.
   * @throws ApiError
   */
  public static createManagedExecutionWorkflow({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateManagedExecutionWorkflowRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedExecutionWorkflowResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/execution-workflows',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `This workflow identifier already has a different managed plan for the caller's Runs principal.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Get workflow status and usage totals.
   * Aggregates run counts by lifecycle state and host-measured usage for
   * one caller-generated workflow identifier. Runs-only keys see only
   * runs created by their own stable key family; broad credentials retain
   * account-wide visibility. Importing an artifact from another key family
   * does not grant access to that family's run metadata. A server-managed
   * workflow includes its continuation status and admitted step count; a
   * plan can be visible before its first Run is admitted. Requires
   * `apps:read`, `runs:read`, `runs:write`, or `admin`.
   *
   * @returns ExecutionWorkflowResponse Workflow counts and terminal-run usage visible to the authenticated principal.
   * @throws ApiError
   */
  public static getExecutionWorkflow({
    workflowId,
  }: {
    /**
     * Caller-generated identifier shared by runs in one workflow.
     */
    workflowId: string,
  }): CancelablePromise<ExecutionWorkflowResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/execution-workflows/{workflow_id}',
      path: {
        'workflow_id': workflowId,
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
}
