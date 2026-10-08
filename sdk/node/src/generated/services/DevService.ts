/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateDevBridgeRequest } from '../models/CreateDevBridgeRequest.js';
import type { CreateDevBridgeResponse } from '../models/CreateDevBridgeResponse.js';
import type { DevBridgeActivity } from '../models/DevBridgeActivity.js';
import type { DevBridgeSession } from '../models/DevBridgeSession.js';
import type { DevBridgeWebhookReplay } from '../models/DevBridgeWebhookReplay.js';
import type { DevSessionResponse } from '../models/DevSessionResponse.js';
import type { DevSyncHistoryItem } from '../models/DevSyncHistoryItem.js';
import type { DevSyncHistoryResponse } from '../models/DevSyncHistoryResponse.js';
import type { InjectScenarioTestChaosRequest } from '../models/InjectScenarioTestChaosRequest.js';
import type { InjectScenarioTestChaosResponse } from '../models/InjectScenarioTestChaosResponse.js';
import type { ListDevBridgesResponse } from '../models/ListDevBridgesResponse.js';
import type { RecordDevSyncRequest } from '../models/RecordDevSyncRequest.js';
import type { RegisterScenarioTestRequest } from '../models/RegisterScenarioTestRequest.js';
import type { ReplayDevBridgeWebhookRequest } from '../models/ReplayDevBridgeWebhookRequest.js';
import type { UpsertDevSessionRequest } from '../models/UpsertDevSessionRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DevService {
  /**
   * List active development sessions owned by the account.
   * ADR-379 bounded inventory. Returns at most 100 active leases, newest expiry first. Connection state is informational and unknown after observer restart or eviction. No credentials are returned.
   * @returns ListDevBridgesResponse Active owned sessions.
   * @throws ApiError
   */
  public static listDevBridges(): CancelablePromise<ListDevBridgesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/dev/bridges',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        503: `Development bridge inventory is disabled.`,
      },
    });
  }
  /**
   * Create a leased local service development session.
   * Operator-gated ADR-378 feature. Select an owned app in an unprotected non-production project environment. Credentials are returned once; the request token routes only the selected application graph, while the attachment token connects the laptop. The lease lasts one hour.
   * @returns CreateDevBridgeResponse Development session and one-time credentials created.
   * @throws ApiError
   */
  public static createDevBridge({
    requestBody,
  }: {
    requestBody: CreateDevBridgeRequest,
  }): CancelablePromise<CreateDevBridgeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/dev/bridges',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Development bridge is disabled or unavailable.`,
      },
    });
  }
  /**
   * Inspect a development session without its credentials.
   * @returns DevBridgeSession Owned development session metadata.
   * @throws ApiError
   */
  public static getDevBridge({
    id,
  }: {
    /**
     * Opaque development session identifier returned by creation.
     */
    id: string,
  }): CancelablePromise<DevBridgeSession> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/dev/bridges/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Revoke a development session and close its laptop connection.
   * @returns void
   * @throws ApiError
   */
  public static revokeDevBridge({
    id,
  }: {
    /**
     * Opaque development session identifier returned by creation.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/dev/bridges/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Inspect recent connection and request metadata.
   * ADR-379 temporary API observer window. Queries, headers and bodies are excluded. Restart or bounded eviction clears activity; observation never authorizes routing.
   * @returns DevBridgeActivity Bounded session activity.
   * @throws ApiError
   */
  public static getDevBridgeActivity({
    id,
  }: {
    /**
     * Owned development session identifier.
     */
    id: string,
  }): CancelablePromise<DevBridgeActivity> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/dev/bridges/{id}/activity',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Development bridge activity is disabled.`,
      },
    });
  }
  /**
   * Copy one verified webhook receipt to the local service.
   * Requires account deploy permission and the session request credential. Copies only a provider-verified receipt for the intercepted app. Creates a separate durable receipt before dispatch; the original invocation stays untouched. Repeating the same key never redispatches. An uncertain or interrupted dispatch must be inspected before explicitly requesting another copy. Provider signature headers are excluded because the original receipt already records verification.
   * @returns DevBridgeWebhookReplay New or previously recorded development replay receipt.
   * @throws ApiError
   */
  public static replayDevBridgeWebhook({
    id,
    requestBody,
  }: {
    /**
     * Destination development session identifier.
     */
    id: string,
    requestBody: ReplayDevBridgeWebhookRequest,
  }): CancelablePromise<DevBridgeWebhookReplay> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/dev/bridges/{id}/webhook-replays',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Session routing credential is invalid or expired.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Inspect an owned development webhook copy receipt.
   * @returns DevBridgeWebhookReplay Persisted copy outcome without payload or credentials.
   * @throws ApiError
   */
  public static getDevBridgeWebhookReplay({
    id,
    replay,
  }: {
    /**
     * Owning development session identifier.
     */
    id: string,
    /**
     * Replay receipt identifier, including after session revocation.
     */
    replay: string,
  }): CancelablePromise<DevBridgeWebhookReplay> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/dev/bridges/{id}/webhook-replays/{replay}',
      path: {
        'id': id,
        'replay': replay,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Create or refresh a remote developer environment.
   * Creates one stable preview app per account, project, and developer workspace, or renews its 24-hour lease. Omitting workspace_id retains the legacy account-and-project identity.
   * @returns DevSessionResponse Existing developer environment refreshed.
   * @throws ApiError
   */
  public static upsertDevSession({
    project,
    requestBody,
  }: {
    /**
     * Stable local project label used to derive the developer URL.
     */
    project: string,
    requestBody: UpsertDevSessionRequest,
  }): CancelablePromise<DevSessionResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/dev/sessions/{project}',
      path: {
        'project': project,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
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
   * Inspect a remote developer environment.
   * Returns the stable URL, app, lease expiry, and safe PostgreSQL binding state of one developer environment. Unlike the upsert, this read never renews the lease or provisions resources.
   * @returns DevSessionResponse Developer environment.
   * @throws ApiError
   */
  public static getDevSession({
    project,
    workspaceId,
  }: {
    /**
     * Stable local project label used to derive the developer URL.
     */
    project: string,
    /**
     * Opaque local workspace identity returned by the CLI derivation. Omit only to target a legacy session.
     */
    workspaceId?: string,
  }): CancelablePromise<DevSessionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/dev/sessions/{project}',
      path: {
        'project': project,
      },
      query: {
        'workspace_id': workspaceId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Tear down a remote developer environment.
   * @returns void
   * @throws ApiError
   */
  public static destroyDevSession({
    project,
    workspaceId,
  }: {
    /**
     * Stable local project label used to derive the developer URL.
     */
    project: string,
    /**
     * Opaque local workspace identity returned by the CLI derivation. Omit only to target a legacy session.
     */
    workspaceId?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/dev/sessions/{project}',
      path: {
        'project': project,
      },
      query: {
        'workspace_id': workspaceId,
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
      },
    });
  }
  /**
   * Register an isolated service namespace for a scenario test.
   * All members must be live developer sessions from this account and run. A missing sibling never resolves to production.
   * @returns void
   * @throws ApiError
   */
  public static registerScenarioTest({
    runId,
    requestBody,
  }: {
    /**
     * Lowercase hexadecimal ID shared only by sessions in this test run.
     */
    runId: string,
    requestBody: RegisterScenarioTestRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/dev/test-runs/{run_id}',
      path: {
        'run_id': runId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Release a scenario test namespace after all members are destroyed.
   * @returns void
   * @throws ApiError
   */
  public static deleteScenarioTest({
    runId,
  }: {
    /**
     * Lowercase hexadecimal ID shared only by sessions in this test run.
     */
    runId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/dev/test-runs/{run_id}',
      path: {
        'run_id': runId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Install a bounded request-level fault plan for an isolated scenario run.
   * Rules apply only to authenticated internal service calls between registered members, expire automatically, and cannot affect production or public traffic.
   * @returns InjectScenarioTestChaosResponse Fault plan installed.
   * @throws ApiError
   */
  public static injectScenarioTestChaos({
    runId,
    requestBody,
  }: {
    /**
     * Random lowercase hexadecimal identity shared by the run's developer sessions.
     */
    runId: string,
    requestBody: InjectScenarioTestChaosRequest,
  }): CancelablePromise<InjectScenarioTestChaosResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/dev/test-runs/{run_id}/chaos',
      path: {
        'run_id': runId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Record a redacted edit-to-live receipt.
   * Stores bounded phase timings for one developer deployment. Repeating the same app and deployment ID is idempotent.
   * @returns DevSyncHistoryItem Receipt stored or already present.
   * @throws ApiError
   */
  public static recordDevSync({
    project,
    requestBody,
  }: {
    /**
     * Local project label used to locate the developer environment sync endpoint.
     */
    project: string,
    requestBody: RecordDevSyncRequest,
  }): CancelablePromise<DevSyncHistoryItem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/dev/sessions/{project}/syncs',
      path: {
        'project': project,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * List bounded edit-to-live history.
   * @returns DevSyncHistoryResponse Newest-first developer sync history and trend summary.
   * @throws ApiError
   */
  public static listDevSyncHistory({
    project,
    workspaceId,
    limit = 20,
  }: {
    /**
     * Local project label used to locate the developer environment history.
     */
    project: string,
    /**
     * Opaque local workspace identity. Omit only for a legacy session.
     */
    workspaceId?: string,
    /**
     * Number of recent receipts to return.
     */
    limit?: number,
  }): CancelablePromise<DevSyncHistoryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/dev/sessions/{project}/history',
      path: {
        'project': project,
      },
      query: {
        'workspace_id': workspaceId,
        'limit': limit,
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
      },
    });
  }
}
