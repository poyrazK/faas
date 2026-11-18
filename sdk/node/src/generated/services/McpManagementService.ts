/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { HostedTaskBindingCheck } from '../models/HostedTaskBindingCheck.js';
import type { HostedTaskBindingSelection } from '../models/HostedTaskBindingSelection.js';
import type { HostedTaskBindingVerification } from '../models/HostedTaskBindingVerification.js';
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
  /**
   * Inspect hosted Task binding qualification
   * Fresh original requester or personal owner reads qualification metadata only. Native source, app, credential and authority drift permanently invalidates previously observed evidence. This read can persist terminal invalidation but never opens credentials or repeats probes. Passing evidence expires within one minute, ends at the original source deadline and grants no readiness or execution authority.
   * @returns HostedTaskBindingVerification Closed qualification metadata with current terminal invalidation and no source, references or credential values.
   * @throws ApiError
   */
  public static getHostedTaskBindings({
    id,
  }: {
    /**
     * Immutable retained source identity whose native Task binding selection is being inspected or set.
     */
    id: string,
  }): CancelablePromise<HostedTaskBindingVerification> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/task-tool-source-intents/{id}/bindings',
      path: {
        'id': id,
      },
      errors: {
        400: `The retained source identity supplied for metadata inspection is malformed.`,
        401: `Authenticate the inspecting requester or personal owner before reading qualification metadata.`,
        403: `This caller lacks permission to inspect the selected source binding metadata.`,
        404: `No qualification for this source is visible to the current account or reader.`,
        409: `Qualification metadata could not be refreshed atomically; inspect the same source identity again.`,
        429: `Metadata reads for this retained Task source are currently rate limited.`,
        503: `The native qualification journal is temporarily unavailable for metadata inspection.`,
      },
    });
  }
  /**
   * Select owner-reviewed native Task bindings
   * An authenticated account owner or fresh unscoped non-agent, non-MCP-login personal administrator selects existing native managed PostgreSQL references under the account MFA policy. Both verification and source-retention opt-ins and independent sealing identities are required. Selection immutably pins the approved source digests, queue namespace, app baselines and secret versions for one ready account-owned database with distinct runtime, migration and observer roles. Retries preserve the original non-renewable source deadline; selection starts neither probes nor execution.
   * @returns HostedTaskBindingVerification New or identically replayed native-reference selection metadata with its fixed source deadline; no checks or execution are started.
   * @throws ApiError
   */
  public static selectHostedTaskBindings({
    id,
    requestBody,
  }: {
    /**
     * Immutable retained source identity whose native Task binding selection is being inspected or set.
     */
    id: string,
    requestBody: HostedTaskBindingSelection,
  }): CancelablePromise<HostedTaskBindingVerification> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/task-tool-source-intents/{id}/bindings',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `The binding selection contains malformed source digests, namespace or native secret references.`,
        401: `Authenticate the selecting personal owner before recording native binding references.`,
        403: `Selecting Task bindings requires personal owner authority and satisfaction of the account MFA policy.`,
        404: `The retained source or selecting owner is unavailable within this account.`,
        409: `The source approval, selected references or pinned native facts differ from the immutable qualification.`,
        429: `Owner requests to select hosted Task bindings are currently rate limited.`,
        503: `New binding selection, source retention or the required independent seal identities are unavailable.`,
      },
    });
  }
  /**
   * Probe selected hosted Task bindings once
   * A fresh personal owner claims the exact selected fingerprint once and runs bounded read-only catalog qualification under the account MFA policy. Native authority and pins are checked before and after the attempt. Validated public destinations, verified TLS and sequential role connections keep credentials private. Concurrent calls and restarts cannot repeat the claim. Success records at most one minute of historical evidence, clamped to source expiry, without proving retained payload readability, migration compatibility, VM connectivity or permission to apply.
   * @returns HostedTaskBindingVerification Recorded one-time catalog results or the existing in-progress or terminal receipt for that same immutable selection.
   * @throws ApiError
   */
  public static checkHostedTaskBindings({
    id,
    requestBody,
  }: {
    /**
     * Retained source identity for the one-time personal-owner binding probe.
     */
    id: string,
    requestBody: HostedTaskBindingCheck,
  }): CancelablePromise<HostedTaskBindingVerification> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/task-tool-source-intents/{id}/bindings/check',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `The probe request must name a valid source identity and lowercase SHA256 qualification fingerprint.`,
        401: `Authenticate the personal owner before requesting a one-time catalog probe.`,
        403: `Probing selected bindings requires fresh personal owner authority and the account MFA policy.`,
        404: `The source, selected qualification or current probing owner cannot be found in this account.`,
        409: `The probe fingerprint conflicts with the selection or all bounded private workers are occupied.`,
        429: `Personal-owner qualification probe requests are currently rate limited.`,
        503: `Owner probing, retained source admission or the required unseal configuration is unavailable.`,
      },
    });
  }
  /**
   * Revoke hosted Task binding qualification
   * A fresh personal owner permanently revokes the exact selected qualification under the account MFA policy. Revocation remains available while new admission is disabled and does not require the old requester or native app baseline to remain live. Revoked evidence cannot be revived and previously observed passing checks never supply execution authority.
   * @returns HostedTaskBindingVerification Terminal revocation metadata for the exact selected qualification fingerprint.
   * @throws ApiError
   */
  public static revokeHostedTaskBindings({
    id,
    requestBody,
  }: {
    /**
     * Retained source identity whose selected binding evidence is being revoked.
     */
    id: string,
    requestBody: HostedTaskBindingCheck,
  }): CancelablePromise<HostedTaskBindingVerification> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/task-tool-source-intents/{id}/bindings/revoke',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `The revocation request contains an invalid source identity or qualification fingerprint.`,
        401: `Authenticate the personal owner before revoking selected binding evidence.`,
        403: `Revoking binding evidence requires personal owner authority and compliance with the account MFA policy.`,
        404: `The retained source, selected qualification or current revoking owner is unavailable in this account.`,
        409: `Revocation names a fingerprint different from the immutable selected qualification.`,
        429: `Owner revocations of hosted Task qualifications are currently rate limited.`,
        503: `The native qualification journal cannot currently record binding revocations.`,
      },
    });
  }
}
