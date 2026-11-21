/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AgentExecutionFileGrant } from '../models/AgentExecutionFileGrant.js';
import type { AppTaskListResponse } from '../models/AppTaskListResponse.js';
import type { AppTaskResponse } from '../models/AppTaskResponse.js';
import type { CreateAgentExecutionFileGrantRequest } from '../models/CreateAgentExecutionFileGrantRequest.js';
import type { CreateAppTaskRequest } from '../models/CreateAppTaskRequest.js';
import type { CreateExecutionArtifactGrantRequest } from '../models/CreateExecutionArtifactGrantRequest.js';
import type { CreateExecutionEnvironmentRequest } from '../models/CreateExecutionEnvironmentRequest.js';
import type { CreateExecutionFileRequest } from '../models/CreateExecutionFileRequest.js';
import type { CreateExecutionRequest } from '../models/CreateExecutionRequest.js';
import type { CreateManagedExecutionWorkflowRequest } from '../models/CreateManagedExecutionWorkflowRequest.js';
import type { DeliverRunArtifactRequest } from '../models/DeliverRunArtifactRequest.js';
import type { ExecutionArtifactGrantResponse } from '../models/ExecutionArtifactGrantResponse.js';
import type { ExecutionCapabilitiesResponse } from '../models/ExecutionCapabilitiesResponse.js';
import type { ExecutionEnvironmentEventsResponse } from '../models/ExecutionEnvironmentEventsResponse.js';
import type { ExecutionEnvironmentResponse } from '../models/ExecutionEnvironmentResponse.js';
import type { ExecutionFileResponse } from '../models/ExecutionFileResponse.js';
import type { ExecutionListResponse } from '../models/ExecutionListResponse.js';
import type { ExecutionPipelineEventsResponse } from '../models/ExecutionPipelineEventsResponse.js';
import type { ExecutionResponse } from '../models/ExecutionResponse.js';
import type { ExecutionTaskListResponse } from '../models/ExecutionTaskListResponse.js';
import type { ExecutionTaskVersionResponse } from '../models/ExecutionTaskVersionResponse.js';
import type { ExecutionWorkflowResponse } from '../models/ExecutionWorkflowResponse.js';
import type { InvokeExecutionTaskRequest } from '../models/InvokeExecutionTaskRequest.js';
import type { ManagedExecutionWorkflowResponse } from '../models/ManagedExecutionWorkflowResponse.js';
import type { ObjectSignedRequest } from '../models/ObjectSignedRequest.js';
import type { PublishExecutionTaskRequest } from '../models/PublishExecutionTaskRequest.js';
import type { RerunExecutionPipelineRequest } from '../models/RerunExecutionPipelineRequest.js';
import type { RevokeExecutionArtifactGrantResponse } from '../models/RevokeExecutionArtifactGrantResponse.js';
import type { RunArtifactDelivery } from '../models/RunArtifactDelivery.js';
import type { RunAttributionBinding } from '../models/RunAttributionBinding.js';
import type { RunBindingKind } from '../models/RunBindingKind.js';
import type { RunBudgetKind } from '../models/RunBudgetKind.js';
import type { RunBudgetPolicy } from '../models/RunBudgetPolicy.js';
import type { RunHistoryResponse } from '../models/RunHistoryResponse.js';
import type { SetRunAttributionBindingRequest } from '../models/SetRunAttributionBindingRequest.js';
import type { SetRunBudgetRequest } from '../models/SetRunBudgetRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class RunsService {
  protected static readonly clientConfig?: typeof OpenAPI;
  /**
   * Get Runs capabilities and account limits.
   * Returns the authenticated account's Runs admission state, accepted
   * runtimes and profiles, and plan-specific request limits. This describes
   * the control-plane admission contract only; it does not guarantee that
   * a scheduler dispatcher or a profile image is currently available.
   * Requires `apps:read`, `runs:read`, `runs:write`, or `admin`.
   *
   * @returns ExecutionCapabilitiesResponse Runs admission contract and limits for the authenticated account.
   * @throws ApiError
   */
  public static getExecutionCapabilities(): CancelablePromise<ExecutionCapabilitiesResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/executions/capabilities',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
      },
    });
  }
  /**
   * Execute source with stored files or standalone contracts.
   * Queues source in an isolated disposable Run with optional stored inputs and outputs, standalone JSON contracts, and a ready dependency environment. The extended execution class can use up to five minutes when enabled for the account. Source and input are sealed before dispatch, and the VM is destroyed before the terminal receipt is persisted. Requires deploy:write, runs:write, or admin. Replay the same idempotency key to recover the same admission.
   * @returns ExecutionResponse Run v2 receipt queued for dispatch, or the existing receipt for an exact replay.
   * @throws ApiError
   */
  public static createExecutionV2({
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
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v2/executions',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `A workflow step already has a receipt, or a referenced input file is not ready.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Submit a versioned Run pipeline or workflow.
   * Submits a durable workflow that can invoke versioned tasks, expand a batch, and apply a pipeline deadline and retry policy. Independent steps run within the workflow and account limits. The control plane continues the encrypted plan across restarts and releases it at terminal settlement. Requires runs:write, deploy:write, or admin and the matching Run capability gates.
   * @returns ManagedExecutionWorkflowResponse Versioned managed workflow admitted or recovered by exact replay.
   * @throws ApiError
   */
  public static createManagedExecutionWorkflowV2({
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
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v2/execution-workflows',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `The workflow identifier is already associated with a different normalized request.`,
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
   * Publish an immutable account-owned task version. Requires deploy write authority.
   * @returns ExecutionTaskVersionResponse Task receipt or metadata. Source and invocation input are never returned.
   * @throws ApiError
   */
  public static publishExecutionTask({
    requestBody,
  }: {
    requestBody: PublishExecutionTaskRequest,
  }): CancelablePromise<ExecutionTaskVersionResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/run-tasks',
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * List account task metadata, including retirement tombstones.
   * @returns ExecutionTaskListResponse Page of published task versions visible to this principal.
   * @throws ApiError
   */
  public static listExecutionTasks({
    cursor,
  }: {
    /**
     * Continue listing from the cursor returned by the previous task page.
     */
    cursor?: string,
  }): CancelablePromise<ExecutionTaskListResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/run-tasks',
      query: {
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Read task metadata without retained code.
   * @returns ExecutionTaskVersionResponse Requested immutable version with its published metadata and lifecycle state.
   * @throws ApiError
   */
  public static getExecutionTask({
    name,
    version,
  }: {
    /**
     * Published task name.
     */
    name: string,
    /**
     * Immutable task version number.
     */
    version: number,
  }): CancelablePromise<ExecutionTaskVersionResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/run-tasks/{name}/versions/{version}',
      path: {
        'name': name,
        'version': version,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Erase retained code and retire a task version; admitted Runs remain readable. Requires deploy write authority.
   * @returns any Retired
   * @throws ApiError
   */
  public static retireExecutionTask({
    name,
    version,
  }: {
    /**
     * Published task name.
     */
    name: string,
    /**
     * Immutable task version number.
     */
    version: number,
  }): CancelablePromise<any> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/run-tasks/{name}/versions/{version}',
      path: {
        'name': name,
        'version': version,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Invoke an explicit task version with key-family-scoped inputs and receipt. Requires a stable Idempotency-Key.
   * @returns ExecutionResponse Invocation admitted for the selected task version or recovered by idempotency key.
   * @throws ApiError
   */
  public static invokeExecutionTask({
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Stable key to reuse when retrying the same task invocation.
     */
    idempotencyKey: string,
    requestBody: InvokeExecutionTaskRequest,
  }): CancelablePromise<ExecutionResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/run-tasks/invoke',
      headers: {
        'Idempotency-Key': idempotencyKey,
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Build an immutable dependency environment in an isolated builder VM.
   * Public registry dependencies only. Python requires exact versions and SHA-256 hashes; npm requires lockfile v3 integrity and disables install scripts. Matching recipes reuse the same account and key-family environment. Runtime base images are operator-pinned. Requires Runs write access and the environment preview gate.
   * @returns ExecutionEnvironmentResponse Durable environment build receipt; poll until ready before selecting it on a Run.
   * @throws ApiError
   */
  public static createExecutionEnvironment({
    requestBody,
  }: {
    requestBody: CreateExecutionEnvironmentRequest,
  }): CancelablePromise<ExecutionEnvironmentResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/execution-environments',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * List retained dependency environments visible to this principal.
   * @returns any Most recent environments, up to 100.
   * @throws ApiError
   */
  public static listExecutionEnvironments(): CancelablePromise<{
    environments: Array<ExecutionEnvironmentResponse>;
  }> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/execution-environments',
      errors: {
        401: `code: unauthorized`,
      },
    });
  }
  /**
   * Read a dependency environment receipt without returning manifests or storage keys.
   * @returns ExecutionEnvironmentResponse Environment metadata.
   * @throws ApiError
   */
  public static getExecutionEnvironment({
    id,
  }: {
    /**
     * Identifier of the dependency environment to read or retire.
     */
    id: string,
  }): CancelablePromise<ExecutionEnvironmentResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/execution-environments/{id}',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Retire an environment; reclaim storage after active Runs and leases finish.
   * @returns any Retirement accepted. New Runs cannot select this environment.
   * @throws ApiError
   */
  public static deleteExecutionEnvironment({
    id,
  }: {
    /**
     * Identifier of the dependency environment to read or retire.
     */
    id: string,
  }): CancelablePromise<{
    status?: 'retiring';
  }> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/execution-environments/{id}',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read resumable, bounded dependency-build diagnostics.
   * Uses environment ownership. Poll during a build; sequences survive API restarts. A gap explicitly reports an evicted retained tail. Reads remain available after writer gates are disabled.
   * @returns ExecutionEnvironmentEventsResponse Retained diagnostics and current build status.
   * @throws ApiError
   */
  public static getExecutionEnvironmentEvents({
    id,
    after,
  }: {
    /**
     * Dependency environment whose diagnostic records to read.
     */
    id: string,
    /**
     * Return build diagnostic records after this sequence number.
     */
    after?: number,
  }): CancelablePromise<ExecutionEnvironmentEventsResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/execution-environments/{id}/events',
      path: {
        'id': id,
      },
      query: {
        'after': after,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Deliver a verified Run artifact into an app-owned upload route
   * Requires account deploy authority and a bucket write grant, or a live production workload identity for the destination app. New delivery checks the source Run or delegated Agent receipt. The explicit delivery key deduplicates storage writes independently of execution. Replay survives source expiry, thread revocation and disabled Run writers. Pending outcomes are resolved by the existing object upload recovery worker without reuploading or rerunning code.
   * @returns RunArtifactDelivery Settled delivery receipt. Failed storage outcomes remain explicit in status.
   * @throws ApiError
   */
  public static deliverRunArtifact({
    slug,
    route,
    requestBody,
  }: {
    /**
     * Application receiving the Run artifact.
     */
    slug: string,
    /**
     * Configured upload route admitting this delivery.
     */
    route: string,
    requestBody: DeliverRunArtifactRequest,
  }): CancelablePromise<RunArtifactDelivery> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/run-artifact-deliveries/{route}',
      path: {
        'slug': slug,
        'route': route,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        402: `The existing object storage budget refuses a new write or download capability.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Delivery identity conflicts or the write is not completed.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Delivery or destination storage is unavailable.`,
      },
    });
  }
  /**
   * Read a durable app-owned artifact delivery receipt
   * Current app ownership and bucket write authority apply independently of Run-file retention or the original Agent thread grant.
   * @returns RunArtifactDelivery Settled delivery receipt.
   * @throws ApiError
   */
  public static getRunArtifactDelivery({
    slug,
    route,
    delivery,
  }: {
    /**
     * Application owning the delivery receipt.
     */
    slug: string,
    /**
     * Upload route under which the receipt was created.
     */
    route: string,
    /**
     * Durable storage delivery receipt UUID.
     */
    delivery: string,
  }): CancelablePromise<RunArtifactDelivery> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/run-artifact-deliveries/{route}/{delivery}',
      path: {
        'slug': slug,
        'route': route,
        'delivery': delivery,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `The delivery receipt store cannot be read.`,
      },
    });
  }
  /**
   * Issue a short-lived app-owned report download capability
   * Requires current app and bucket read authority and a completed delivery. Uses existing branded object GET capabilities with a five-minute expiry and the retained public version when available. Applications authenticate their customers before issuing a URL and keep it out of Agent transcripts.
   * @returns ObjectSignedRequest Short-lived, revocable object GET capability.
   * @throws ApiError
   */
  public static signRunArtifactDelivery({
    slug,
    route,
    delivery,
  }: {
    /**
     * Application authorizing the customer download.
     */
    slug: string,
    /**
     * Upload route locating the delivered customer object.
     */
    route: string,
    /**
     * Completed delivery UUID to sign for download.
     */
    delivery: string,
  }): CancelablePromise<ObjectSignedRequest> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/run-artifact-deliveries/{route}/{delivery}/download-url',
      path: {
        'slug': slug,
        'route': route,
        'delivery': delivery,
      },
      errors: {
        401: `code: unauthorized`,
        402: `The object storage budget refuses this download capability.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The delivery must complete before its object can be downloaded.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `The destination object download service is unavailable.`,
      },
    });
  }
  /**
   * Prepare a stable immutable upload identity without reserving quota.
   * File IDs are scoped to account and Runs key family plus checksum and size. Check Run admission replay before uploading missing bytes. Preparing a file does not admit a Run or reserve storage.
   * @returns ExecutionFileResponse Authenticated Run file metadata. No storage key or credential is returned.
   * @throws ApiError
   */
  public static prepareExecutionFile({
    requestBody,
  }: {
    requestBody: CreateExecutionFileRequest,
  }): CancelablePromise<ExecutionFileResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/execution-files',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `File upload is busy or inputs are not published.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Read account and key-family scoped file metadata.
   * @returns ExecutionFileResponse Private file state and checksum metadata visible to this caller.
   * @throws ApiError
   */
  public static getExecutionFile({
    id,
  }: {
    /**
     * Private Run file whose metadata to read.
     */
    id: string,
  }): CancelablePromise<ExecutionFileResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/execution-files/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The file is in a transient publication state.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Revoke future imports and schedule physical cleanup.
   * Returns after revocation. Active admitted readers remain protected and quota is released only after storage confirms deletion.
   * @returns any Cleanup scheduled; physical deletion is asynchronous.
   * @throws ApiError
   */
  public static deleteExecutionFile({
    id,
  }: {
    /**
     * File to revoke for future imports.
     */
    id: string,
  }): CancelablePromise<{
    status: 'cleanup_scheduled';
  }> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/execution-files/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The file is busy and cannot be revoked at this time.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Delegate a private uploaded file to one Agent task call.
   * Requires account-wide deploy authority, access to the endpoint's app and an active task version delegated to that endpoint. Submit the returned opaque token in the gregale_task arguments.file_grants array. A grant names one endpoint, thread, logical tool call, task version and guest input path. It expires within 15 minutes or when the file expires, whichever comes first. The grant authorizes staging for that call only. Set step_label to bind the upload to one explicit gregale_pipeline step. Omit it for gregale_task.
   *
   * @returns AgentExecutionFileGrant Private staging grant. Retain the same token for dispatch retries.
   * @throws ApiError
   */
  public static createAgentExecutionFileGrant({
    id,
    requestBody,
  }: {
    /**
     * Owned uploaded file to delegate to the Agent call.
     */
    id: string,
    requestBody: CreateAgentExecutionFileGrantRequest,
  }): CancelablePromise<AgentExecutionFileGrant> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/execution-files/{id}/agent-grants',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Task admission or private file grants are unavailable.`,
      },
    });
  }
  /**
   * Stream a bounded immutable Run input file.
   * Requires Content-Length and X-Content-SHA256 matching the prepared file. Reserves account storage before transfer. Length and SHA-256 must verify before publication; ready objects are never overwritten. No Run is admitted by this call.
   * @returns ExecutionFileResponse Published file metadata after the uploaded length and digest have been verified.
   * @throws ApiError
   */
  public static uploadExecutionFile({
    id,
    xContentSha256,
    requestBody,
  }: {
    /**
     * Prepared file identifier returned before uploading bytes.
     */
    id: string,
    /**
     * SHA-256 digest of the request bytes in the same format supplied at preparation.
     */
    xContentSha256: string,
    requestBody: Blob,
  }): CancelablePromise<ExecutionFileResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/execution-files/{id}/content',
      path: {
        'id': id,
      },
      headers: {
        'X-Content-SHA256': xContentSha256,
      },
      body: requestBody,
      mediaType: 'application/octet-stream',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Upload already in progress or the prepared file state changed.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Download a retained file using account and key-family authorization.
   * Verify downloaded size and checksum against metadata before publishing the local file. File IDs are locators and never bearer capabilities.
   * @returns binary Verified retained file bytes.
   * @throws ApiError
   */
  public static downloadExecutionFile({
    id,
  }: {
    /**
     * Retained input or output file to download.
     */
    id: string,
  }): CancelablePromise<Blob> {
    return __request(this?.clientConfig ?? OpenAPI, {
      responseType: 'blob',
      method: 'GET',
      url: '/v1/execution-files/{id}/content',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The requested file has not been published for download.`,
        410: `File retention expired or content is unavailable.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * List disposable executions.
   * Returns the caller's newest disposable execution receipts, ordered by
   * creation time descending. Runs-only API keys see runs created by their
   * own key family; `admin`, `apps:read`, and `deploy:write` credentials
   * retain account-wide access. Use `status`
   * to narrow the page before applying offset pagination; source and input
   * are never returned. Requires `apps:read`, `runs:read`, `runs:write`, or
   * `admin`.
   *
   * @returns ExecutionListResponse Execution page visible to the authenticated principal.
   * @throws ApiError
   */
  public static listExecutions({
    limit = 50,
    offset,
    status,
    workflowId,
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
    /**
     * Return only executions carrying this workflow identifier, within the caller's visible key family or account scope.
     */
    workflowId?: string,
  }): CancelablePromise<ExecutionListResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/executions',
      query: {
        'limit': limit,
        'offset': offset,
        'status': status,
        'workflow_id': workflowId,
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
   * 501 while the host isolation gate is disabled. Requires `deploy:write`,
   * `runs:write`, or `admin`. A Runs-only key owns the resulting receipt
   * under its stable key-family identity, which is preserved during key
   * rotation. Agent workflow step labels beginning with `gwf:` are unique
   * within the same workflow and key family; retry clients should reload
   * the existing workflow receipt after a 409.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
        409: `An agent workflow step with this workflow ID and step label already has a Run receipt.`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
  /**
   * Read durable observed pipeline progress.
   * Metadata-only cursor pages with a retained-tail gap and current snapshot. Runs-only credentials are scoped to their original key family. Observation remains available with writer gates off. Accept text/event-stream for SSE; closing a watch does not cancel work.
   * @returns ExecutionPipelineEventsResponse Progress page or resumable SSE; terminal is true only once the returned page drains the journal.
   * @throws ApiError
   */
  public static getExecutionPipelineEvents({
    id,
    after,
    limit = 256,
    lastEventId,
  }: {
    /**
     * Internal managed pipeline job UUID used to observe progress.
     */
    id: string,
    /**
     * Return pipeline updates after this journal cursor.
     */
    after?: number,
    /**
     * Maximum number of pipeline journal entries in a page.
     */
    limit?: number,
    /**
     * Last pipeline event received on the previous stream, used to resume SSE.
     */
    lastEventId?: string,
  }): CancelablePromise<ExecutionPipelineEventsResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/execution-pipelines/{id}/events',
      path: {
        'id': id,
      },
      headers: {
        'Last-Event-ID': lastEventId,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Create a new execution of failed branches and affected descendants.
   * Requires the original manifest and key family. Every previously started affected step must be acknowledged in repeat_steps. Healthy independent outputs are reused only with intact fingerprints, contracts and owned unexpired artifacts. The original receipt stays immutable. New attempts use normal budgets. An exact child workflow_id replay returns its receipt even with writers off or outputs subsequently unavailable. Agent callback pipelines require a new delegated invocation.
   * @returns ManagedExecutionWorkflowResponse New rerun or existing exact replay.
   * @throws ApiError
   */
  public static rerunExecutionPipeline({
    id,
    requestBody,
  }: {
    /**
     * Original managed pipeline job UUID whose failed branches should run again.
     */
    id: string,
    requestBody: RerunExecutionPipelineRequest,
  }): CancelablePromise<ManagedExecutionWorkflowResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v2/execution-pipelines/{id}/reruns',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Stop pipeline admissions and request cancellation of active Runs.
   * Idempotent for an existing pipeline. Cancellation closes the durable admission fence before responding; the continuation worker cancels admitted Runs and waits for their terminal settlement. Available with writer gates disabled. Runs-only keys are limited to their stable key family. Requires runs:write, deploy:write, or admin.
   * @returns ExecutionWorkflowResponse Pipeline cancellation requested; inspect managed pipeline metadata for completion.
   * @throws ApiError
   */
  public static cancelExecutionPipeline({
    workflowId,
  }: {
    /**
     * Caller-supplied workflow identifier for the pipeline to cancel.
     */
    workflowId: string,
  }): CancelablePromise<ExecutionWorkflowResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/execution-workflows/{workflow_id}/cancel',
      path: {
        'workflow_id': workflowId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Get one disposable execution.
   * Runs-only keys can read receipts created by their own key family. `admin` and broad legacy scopes retain account-wide access. Requires `apps:read`, `runs:read`, `runs:write`, or `admin`.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
   * Cancellation is idempotent and destroys a claimed VM during teardown. Runs-only keys can cancel only receipts created by their own key family. Requires `deploy:write`, `runs:write`, or `admin`.
   * @returns ExecutionResponse Execution cancellation requested, or its existing terminal receipt returned.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
  /**
   * Stream disposable execution events.
   * Opens a resumable Server-Sent Events stream for one execution. Event
   * ids are monotonically increasing control-plane cursors; reconnect with
   * `after` or `Last-Event-ID`. The stream emits bounded status, stdout,
   * stderr, and terminal events and closes after the terminal event.
   * When the cursor predates retained events, a gap event with
   * `execution_id` and `through_sequence` precedes the retained events.
   * Complete bounded stdout/stderr remain available in the terminal receipt;
   * `output_truncated` describes the output byte limit, not replay retention.
   * Source, input, host paths, and VM internals are never included.
   * Runs-only keys can stream only receipts created by their own key family.
   * Requires `apps:read`, `runs:read`, `runs:write`, or `admin`.
   *
   * @returns string Resumable execution event stream.
   * @throws ApiError
   */
  public static streamExecutionEvents({
    id,
    after,
    limit = 100,
    lastEventId,
  }: {
    /**
     * Canonical UUID for a disposable execution.
     */
    id: string,
    /**
     * Resume after this event id (exclusive).
     */
    after?: number,
    /**
     * Maximum number of events returned per replay batch.
     */
    limit?: number,
    /**
     * Standard SSE reconnect cursor; `after` takes precedence.
     */
    lastEventId?: number,
  }): CancelablePromise<string> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/executions/{id}/events',
      path: {
        'id': id,
      },
      headers: {
        'Last-Event-ID': lastEventId,
      },
      query: {
        'after': after,
        'limit': limit,
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
  /**
   * Share one output artifact with another agent.
   * Creates a single-use, short-lived capability for one named artifact on
   * a successful execution. The creating Runs key must own the source run;
   * account-wide principals may grant any account run. The bearer token is
   * returned once and only its hash is stored. The recipient can stage only
   * these bytes using `artifact_inputs.grant_token`; this does not grant
   * access to the source receipt, events, cancellation, or other artifacts.
   * Requires `runs:write`, `deploy:write`, or `admin`.
   *
   * @returns ExecutionArtifactGrantResponse Capability created. Store the token securely; it is shown only once.
   * @throws ApiError
   */
  public static createExecutionArtifactGrant({
    id,
    requestBody,
  }: {
    /**
     * Canonical UUID for a disposable execution.
     */
    id: string,
    requestBody: CreateExecutionArtifactGrantRequest,
  }): CancelablePromise<ExecutionArtifactGrantResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/executions/{id}/artifact-grants',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Revoke an unredeemed artifact grant.
   * Revokes future use of one artifact capability. A Runs-only key may
   * revoke grants it created; account-wide principals may revoke any
   * account grant. A grant already redeemed cannot be undone, but the
   * recipient's new run retains its own encrypted copy. Revocation remains
   * available when new Runs admission is disabled.
   *
   * @returns RevokeExecutionArtifactGrantResponse Grant revoked or was already revoked.
   * @throws ApiError
   */
  public static revokeExecutionArtifactGrant({
    id,
    idempotencyKey,
  }: {
    /**
     * Canonical UUID for an execution artifact grant.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<RevokeExecutionArtifactGrantResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/execution-artifact-grants/{id}',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * List deployment-attached tasks for an app.
   * Returns newest-first one-off command receipts for the app. Task rows
   * remain pinned to the immutable deployment selected at admission.
   * Scheduler leases and artifact storage identifiers are never returned.
   *
   * @returns AppTaskListResponse App-scoped task page.
   * @throws ApiError
   */
  public static listAppTasks({
    slug,
    limit = 50,
    offset,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Maximum number of task receipts to return.
     */
    limit?: number,
    /**
     * Number of task receipts to skip.
     */
    offset?: number,
  }): CancelablePromise<AppTaskListResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/tasks',
      path: {
        'slug': slug,
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Run a one-off command against an app deployment.
   * Queues one manual command in a fresh task VM using the app's current
   * live deployment, scoped configuration, bindings, and network policy.
   * The selected deployment is pinned at admission and the VM is destroyed
   * before terminal completion. Release tasks cannot be created here.
   *
   * @returns AppTaskResponse Manual app task admitted and queued.
   * @throws ApiError
   */
  public static createAppTask({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateAppTaskRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppTaskResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/tasks',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `code: conflict`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Get one deployment-attached app task.
   * @returns AppTaskResponse Current or terminal app-task receipt.
   * @throws ApiError
   */
  public static getAppTask({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Canonical UUID for a deployment-attached app task.
     */
    id: string,
  }): CancelablePromise<AppTaskResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/tasks/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Cancel one deployment-attached app task.
   * Cancellation is idempotent; claimed work is torn down before terminal acknowledgement.
   * @returns AppTaskResponse App-task cancellation recorded, or its existing terminal receipt returned.
   * @throws ApiError
   */
  public static cancelAppTask({
    slug,
    id,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Canonical UUID for a deployment-attached app task.
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppTaskResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/tasks/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Attach to an interactive app task (WebSocket).
   * Served by the compute gateway, not the API. Requires the account
   * credential, deploy-write scope, the `X-Gregale-Attach-Token` from the
   * task's create response, and the `gregale-app-task-attach-v1`
   * WebSocket subprotocol. Optional `rows`/`cols` set the initial terminal
   * size. The token is consumed by the first successful attach. Binary
   * messages start with a type byte: client 0x01 stdin, 0x02 resize
   * (rows, cols as big-endian uint16), 0x03 stdin close; server 0x10
   * attached, 0x11 stdout, 0x12 stderr, 0x13 terminal JSON
   * (`status`, `exit_code`, `failure`).
   *
   * @returns void
   * @throws ApiError
   */
  public static attachAppTask({
    slug,
    id,
    rows,
    cols,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Canonical UUID for a deployment-attached app task.
     */
    id: string,
    /**
     * Initial terminal height for TTY sessions; defaults to 24.
     */
    rows?: number,
    /**
     * Initial terminal width for TTY sessions; defaults to 80.
     */
    cols?: number,
  }): CancelablePromise<void> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/tasks/{id}/attach',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'rows': rows,
        'cols': cols,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The task VM is still starting; retry after the Retry-After interval.`,
        410: `The session ended, already has a client, or its attach window closed.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read an account-owned customer or task budget and current UTC-day usage.
   * @returns RunBudgetPolicy Revisioned Run control or payload-free history.
   * @throws ApiError
   */
  public static getRunBudget({
    kind,
    ref,
  }: {
    /**
     * Controls whether the policy limits a customer or a task.
     */
    kind: RunBudgetKind,
    /**
     * Customer reference or task name covered by this budget.
     */
    ref: string,
  }): CancelablePromise<RunBudgetPolicy> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/run-controls/budgets/{kind}/{ref}',
      path: {
        'kind': kind,
        'ref': ref,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Replace a Run budget using its expected revision. Zero limits refuse new work; omitted limits add no restriction.
   * @returns RunBudgetPolicy Updated budget with its new revision and usage snapshot.
   * @throws ApiError
   */
  public static setRunBudget({
    kind,
    ref,
    requestBody,
  }: {
    /**
     * Controls whether the policy limits a customer or a task.
     */
    kind: RunBudgetKind,
    /**
     * Customer reference or task name covered by this budget.
     */
    ref: string,
    requestBody: SetRunBudgetRequest,
  }): CancelablePromise<RunBudgetPolicy> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/run-controls/budgets/{kind}/{ref}',
      path: {
        'kind': kind,
        'ref': ref,
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Read an attribution binding by public API key or owned Agent endpoint ID.
   * @returns RunAttributionBinding Active or retired attribution binding and its recorded revision.
   * @throws ApiError
   */
  public static getRunAttributionBinding({
    kind,
    id,
  }: {
    /**
     * Public API key or Agent endpoint subject type.
     */
    kind: RunBindingKind,
    /**
     * Public identifier of the API key or owned Agent endpoint.
     */
    id: string,
  }): CancelablePromise<RunAttributionBinding> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/run-controls/bindings/{kind}/{id}',
      path: {
        'kind': kind,
        'id': id,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Bind a public API key or Agent endpoint to a customer. Key bindings follow rotation.
   * @returns RunAttributionBinding Replacement binding identifying the assigned customer for later Runs.
   * @throws ApiError
   */
  public static setRunAttributionBinding({
    kind,
    id,
    requestBody,
  }: {
    /**
     * Public API key or Agent endpoint subject type.
     */
    kind: RunBindingKind,
    /**
     * Public identifier of the API key or owned Agent endpoint.
     */
    id: string,
    requestBody: SetRunAttributionBindingRequest,
  }): CancelablePromise<RunAttributionBinding> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/run-controls/bindings/{kind}/{id}',
      path: {
        'kind': kind,
        'id': id,
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Disable a binding while retaining its revision tombstone.
   * @returns void
   * @throws ApiError
   */
  public static deleteRunAttributionBinding({
    kind,
    id,
    expectedRevision,
  }: {
    /**
     * Public API key or Agent endpoint subject type.
     */
    kind: RunBindingKind,
    /**
     * Public identifier of the API key or owned Agent endpoint.
     */
    id: string,
    /**
     * Current binding revision required to disable it.
     */
    expectedRevision: number,
  }): CancelablePromise<void> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/run-controls/bindings/{kind}/{id}',
      path: {
        'kind': kind,
        'id': id,
      },
      query: {
        'expected_revision': expectedRevision,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Read metadata-only invocation history under existing account/key-family visibility.
   * @returns RunHistoryResponse Page of metadata-only invocations matching the history filters.
   * @throws ApiError
   */
  public static listRunHistory({
    taskName,
    taskVersion,
    customerRef,
    agentRef,
    status,
    since,
    until,
    cursor,
  }: {
    /**
     * Return only invocations of this task name.
     */
    taskName?: string,
    /**
     * Return only invocations of this published task version.
     */
    taskVersion?: number,
    /**
     * Filter history by the recorded customer reference.
     */
    customerRef?: string,
    /**
     * Filter history by the recorded Agent reference.
     */
    agentRef?: string,
    /**
     * Filter invocations by their recorded execution status.
     */
    status?: 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'out_of_memory' | 'cancelled',
    /**
     * Start timestamp for filtering invocation history.
     */
    since?: string,
    /**
     * End timestamp for filtering invocation history.
     */
    until?: string,
    /**
     * Continue from the cursor supplied by the previous history page.
     */
    cursor?: string,
  }): CancelablePromise<RunHistoryResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/run-history',
      query: {
        'task_name': taskName,
        'task_version': taskVersion,
        'customer_ref': customerRef,
        'agent_ref': agentRef,
        'status': status,
        'since': since,
        'until': until,
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
}
