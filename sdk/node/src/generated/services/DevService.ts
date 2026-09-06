/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevSessionResponse } from '../models/DevSessionResponse.js';
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
