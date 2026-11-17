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
   * @returns HostedTaskSourceIntent Immutable hosted source intent metadata, projected as expired after its fixed retention deadline.
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
        400: `The requested hosted source intent identity is malformed.`,
        401: `Authenticate before inspecting hosted source intent metadata.`,
        403: `This source intent requires an unscoped personal-account inspector.`,
        404: `No hosted source intent is visible to this inspecting principal.`,
        409: `The retained source metadata cannot be inspected in its current form.`,
        429: `Hosted source intent inspection is currently rate-limited.`,
        503: `The native source intent journal is temporarily unavailable for inspection.`,
      },
    });
  }
  /**
   * Retain reviewed authored Task source under a stable identity
   * Opt-in preview: requires FAAS_MCP_HOSTED_TASK_SOURCE_RETENTION=1, independent Account-domain sealing, unscoped personal deploy-write authority and MFA policy. Atomically saves encrypted exact source bundles and immutable native app/config/policy metadata for fifteen minutes. Maximum 2 MiB and 1024 entries per role, two active source intents, 16 MiB ciphertext and 256 lifetime identities per account. Exact retries preserve the original expiry; different reviews conflict. Native current app baselines are checked on first creation. Metadata is not current readiness evidence. No owner decision, credentials, build, migration or execution is authorized.
   * @returns HostedTaskSourceIntent Newly retained or identically replayed Task source intent with its original expiry and no execution authority.
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
        400: `Authored Task bundles or their review failed bounded source validation.`,
        401: `Authenticate the requesting publisher before retaining Task source.`,
        403: `Preparing retained source requires personal deploy-write authority and the account MFA policy.`,
        404: `The requesting Task publisher is no longer a fresh authorized key.`,
        409: `The source intent identity, requesting key or native app baseline differs from this preparation.`,
        429: `The account source retention identity, active-intent or ciphertext quota is exhausted.`,
        503: `New source retention is disabled or independent Account-domain sealing is unavailable.`,
      },
    });
  }
}
