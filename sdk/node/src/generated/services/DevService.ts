/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevSessionResponse } from '../models/DevSessionResponse.js';
import type { DevSyncHistoryItem } from '../models/DevSyncHistoryItem.js';
import type { DevSyncHistoryResponse } from '../models/DevSyncHistoryResponse.js';
import type { RecordDevSyncRequest } from '../models/RecordDevSyncRequest.js';
import type { UpsertDevSessionRequest } from '../models/UpsertDevSessionRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DevService {
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
