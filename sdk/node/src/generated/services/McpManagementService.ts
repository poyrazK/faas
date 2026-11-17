/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { HostedTaskSourceCheck } from '../models/HostedTaskSourceCheck.js';
import type { HostedTaskSourceDecision } from '../models/HostedTaskSourceDecision.js';
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
  /**
   * Recheck retained hosted Task source review
   * Revalidates the original personal requester and locked native app configuration and serving pins. Observed drift permanently makes the review stale. Source expiry cannot renew. The original publisher or personal owner may check; this creates no owner decision, credential binding or execution authority. HTTP response idempotency caching is excluded so every call rechecks current facts.
   * @returns HostedTaskSourceIntent Current hosted source review observation, including permanently stale or expired outcomes.
   * @throws ApiError
   */
  public static checkHostedTaskSourceIntent({
    id,
    requestBody,
  }: {
    /**
     * Retained hosted source identity to observe for drift
     */
    id: string,
    requestBody: HostedTaskSourceCheck,
  }): CancelablePromise<HostedTaskSourceIntent> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/task-tool-source-intents/{id}/check',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `The hosted source freshness check fingerprint is invalid.`,
        401: `Authenticate before rechecking the retained hosted source review.`,
        403: `Hosted source freshness checks require personal publishing authority and the account MFA policy.`,
        404: `The retained hosted review is not visible to this checking requester.`,
        409: `The retained hosted source review cannot be checked against this request.`,
        429: `Hosted source freshness observations are currently rate-limited.`,
        503: `The native source review freshness journal is temporarily unavailable.`,
      },
    });
  }
  /**
   * Record an owner decision on hosted Task source
   * An authenticated personal owner or fresh unscoped non-agent personal administrator approves, rejects or revokes the exact retained intent and review digests. Agent and MCP-login credentials cannot decide. Approval requires enabled source retention and rechecks retained source availability, original requester authority and native app baselines atomically. Rejection and revocation do not require live requester or app facts; stale and expired reviews stay terminal. Exact retries preserve the first decision timestamp and fixed deadline. Approval is only a source review; readiness, credential verification and execution authorization remain absent. Native journal idempotency rechecks authority on every call instead of replaying an HTTP response cache.
   * @returns HostedTaskSourceIntent Recorded hosted source owner review or a terminal stale or expired review that cannot be approved.
   * @throws ApiError
   */
  public static decideHostedTaskSourceIntent({
    id,
    requestBody,
  }: {
    /**
     * Exact retained hosted source identity named by its personal owner
     */
    id: string,
    requestBody: HostedTaskSourceDecision,
  }): CancelablePromise<HostedTaskSourceIntent> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/task-tool-source-intents/{id}/decision',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `The hosted source owner decision or its digest selectors are malformed.`,
        401: `Authenticate the personal owner before deciding this retained source review.`,
        403: `A hosted source decision requires personal non-agent owner authority and the account MFA policy.`,
        404: `The source review or current deciding owner is unavailable within this account.`,
        409: `The hosted source decision targets a different digest or conflicts with its retained terminal state.`,
        429: `Owner decisions for hosted source reviews are currently rate-limited.`,
        503: `Source review approval is disabled or the owner review journal is temporarily unavailable.`,
      },
    });
  }
}
