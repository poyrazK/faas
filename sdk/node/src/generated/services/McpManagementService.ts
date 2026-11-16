/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { HostedTaskSourceIntent } from '../models/HostedTaskSourceIntent.js';
import type { HostedTaskSourcePrepare } from '../models/HostedTaskSourcePrepare.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class McpManagementService {
  protected static readonly clientConfig?: typeof OpenAPI;
  /**
   * Recover hosted Task source intent metadata
   * Requester or personal owner reads the retained source review and fixed expiry. Requires a fresh unscoped account key with deployment authority, or an authenticated personal owner. No source retrieval, renewal, owner decision or execution.
   * @returns HostedTaskSourceIntent Original immutable intent metadata; no source bytes or credentials
   * @throws ApiError
   */
  public static getHostedTaskSourceIntent({
    id,
  }: {
    /**
     * Caller-chosen immutable source intent identity
     */
    id: string,
  }): CancelablePromise<HostedTaskSourceIntent> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/task-tool-source-intents/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `Invalid or oversized source/review`,
        401: `Authentication required`,
        403: `Unscoped personal-account authority required`,
        404: `Intent unavailable to this requester`,
        409: `Immutable identity or current native app review changed`,
        429: `Account retention quota reached`,
        503: `Source retention or Account-domain sealing is disabled`,
      },
    });
  }
  /**
   * Retain reviewed authored Task source under a stable identity
   * Opt-in preview: requires FAAS_MCP_HOSTED_TASK_SOURCE_RETENTION=1, independent Account-domain sealing, unscoped personal deploy-write authority and MFA policy. Atomically saves encrypted exact source bundles and immutable native app/config/policy metadata for fifteen minutes. Maximum 2 MiB and 1024 entries per role, two active source intents, 16 MiB ciphertext and 256 lifetime identities per account. Exact retries preserve the original expiry; different reviews conflict. Native current app baselines are checked on first creation. Metadata is not current readiness evidence. No owner decision, credentials, build, migration or execution is authorized.
   * @returns HostedTaskSourceIntent Original immutable intent metadata; no source bytes or credentials
   * @throws ApiError
   */
  public static prepareHostedTaskSourceIntent({
    id,
    requestBody,
  }: {
    /**
     * Caller-chosen immutable source intent identity
     */
    id: string,
    requestBody: HostedTaskSourcePrepare,
  }): CancelablePromise<HostedTaskSourceIntent> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/task-tool-source-intents/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid or oversized source/review`,
        401: `Authentication required`,
        403: `Unscoped personal-account authority required`,
        404: `Intent unavailable to this requester`,
        409: `Immutable identity or current native app review changed`,
        429: `Account retention quota reached`,
        503: `Source retention or Account-domain sealing is disabled`,
      },
    });
  }
}
