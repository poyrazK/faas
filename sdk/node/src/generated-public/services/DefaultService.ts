/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FireCronRequestResponse } from '../models/FireCronRequestResponse.js';
import type { MCPAdmissionEvidence } from '../models/MCPAdmissionEvidence.js';
import type { MCPAdmissionHistoryResponse } from '../models/MCPAdmissionHistoryResponse.js';
import type { MCPConnectionHealthSnapshot } from '../models/MCPConnectionHealthSnapshot.js';
import type { MCPReadinessEvidence } from '../models/MCPReadinessEvidence.js';
import type { MCPReadinessIncidentTimeline } from '../models/MCPReadinessIncidentTimeline.js';
import type { MCPReadinessOverride } from '../models/MCPReadinessOverride.js';
import type { MCPReadinessOverrideRequest } from '../models/MCPReadinessOverrideRequest.js';
import type { MCPReadinessPolicy } from '../models/MCPReadinessPolicy.js';
import type { MCPReadinessPolicyDiff } from '../models/MCPReadinessPolicyDiff.js';
import type { MCPReadinessPolicyHistory } from '../models/MCPReadinessPolicyHistory.js';
import type { MCPReadinessProbeRequest } from '../models/MCPReadinessProbeRequest.js';
import type { MCPReadinessSnapshot } from '../models/MCPReadinessSnapshot.js';
import type { RestoreMCPReadinessPolicyRequest } from '../models/RestoreMCPReadinessPolicyRequest.js';
import type { SetMCPReadinessPolicyRequest } from '../models/SetMCPReadinessPolicyRequest.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class DefaultService {
  protected static readonly clientConfig?: typeof OpenAPI;
  /**
   * List readiness incident events and notification status
   * Newest logical events first, independently of notification success. Retries update delivery status without adding timeline rows. Omitting scope lists every environment.
   * @returns MCPReadinessIncidentTimeline Bounded incident event timeline
   * @throws ApiError
   */
  public static listMcpReadinessIncidents({
    slug,
    scope,
    before,
    limit = 20,
  }: {
    /**
     * Application owning the requested readiness incident history.
     */
    slug: string,
    /**
     * Exact environment selected for readiness incident history. Omission selects default.
     */
    scope?: string,
    /**
     * Opaque keyset cursor returned as next_before.
     */
    before?: string,
    /**
     * Maximum number of entries returned in this readiness incident history page.
     */
    limit?: number,
  }): CancelablePromise<MCPReadinessIncidentTimeline> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-readiness-incidents',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `Invalid environment or cursor`,
        401: `Authenticate to list retained MCP readiness incident events.`,
        403: `Read scope and MFA required`,
        404: `Owned app not found`,
      },
    });
  }
  /**
   * List recent policy revisions for one environment
   * @returns MCPReadinessPolicyHistory Readiness policy result
   * @throws ApiError
   */
  public static listMcpReadinessPolicyHistory({
    slug,
    scope,
    beforeRevision,
    limit = 20,
  }: {
    /**
     * Application owning the requested readiness policy revision history.
     */
    slug: string,
    /**
     * Exact environment selected for readiness policy revision history. Omission selects default.
     */
    scope?: string,
    /**
     * Revision cursor returned by the preceding policy-history page.
     */
    beforeRevision?: string,
    /**
     * Maximum number of entries returned in this readiness policy revision history page.
     */
    limit?: number,
  }): CancelablePromise<MCPReadinessPolicyHistory> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-readiness-policy/history',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'before_revision': beforeRevision,
        'limit': limit,
      },
      errors: {
        400: `Invalid scope, cursor, revisions or reason`,
        404: `Owned app or scoped revision not found`,
        409: `Current policy revision changed`,
      },
    });
  }
  /**
   * Read one immutable environment policy revision
   * @returns MCPReadinessPolicy Configuration and audit context for the requested historical policy revision.
   * @throws ApiError
   */
  public static getMcpReadinessPolicyRevision({
    slug,
    revision,
    scope,
  }: {
    /**
     * Application owning the requested one historical readiness policy revision.
     */
    slug: string,
    /**
     * Historical policy revision UUID selected for one historical readiness policy revision.
     */
    revision: string,
    /**
     * Exact environment selected for one historical readiness policy revision. Omission selects default.
     */
    scope?: string,
  }): CancelablePromise<MCPReadinessPolicy> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-readiness-policy/history/{revision}',
      path: {
        'slug': slug,
        'revision': revision,
      },
      query: {
        'scope': scope,
      },
      errors: {
        400: `The historical policy revision or its environment is invalid.`,
        404: `The requested historical policy revision was not found for this application.`,
        409: `The requested historical revision conflicts with current readiness state.`,
      },
    });
  }
  /**
   * Compare configuration between two revisions; omitted to_revision selects current
   * @returns MCPReadinessPolicyDiff Field changes between the selected readiness policy revisions.
   * @throws ApiError
   */
  public static diffMcpReadinessPolicy({
    slug,
    fromRevision,
    scope,
    toRevision,
  }: {
    /**
     * Application owning the requested readiness policy revision comparison.
     */
    slug: string,
    /**
     * Historical readiness policy revision used as the comparison baseline.
     */
    fromRevision: string,
    /**
     * Exact environment selected for readiness policy revision comparison. Omission selects default.
     */
    scope?: string,
    /**
     * Comparison policy revision UUID; omission compares against the current policy.
     */
    toRevision?: string,
  }): CancelablePromise<MCPReadinessPolicyDiff> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-readiness-policy/diff',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'from_revision': fromRevision,
        'to_revision': toRevision,
      },
      errors: {
        400: `Invalid policy comparison environment or revision pair.`,
        404: `A policy selected for comparison was not found for this application.`,
        409: `Policy comparison could not use the expected current revision.`,
      },
    });
  }
  /**
   * Restore historical configuration as a new audited revision requiring fresh probes
   * Requires admin scope and applicable MFA. Expected revision must match the current policy in the same environment. Restores configuration only, validates current OAuth bindings and app applicability, and records a new revision, author, time, reason and restored_from. Existing evidence and emergency overrides cannot authorize traffic under the new revision.
   * @returns MCPReadinessPolicy New policy revision restored from the requested historical configuration.
   * @throws ApiError
   */
  public static restoreMcpReadinessPolicy({
    slug,
    revision,
    requestBody,
    scope,
  }: {
    /**
     * Application owning the requested readiness policy restoration.
     */
    slug: string,
    /**
     * Historical policy revision UUID selected for readiness policy restoration.
     */
    revision: string,
    requestBody: RestoreMCPReadinessPolicyRequest,
    /**
     * Exact environment selected for readiness policy restoration. Omission selects default.
     */
    scope?: string,
  }): CancelablePromise<MCPReadinessPolicy> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/mcp-readiness-policy/history/{revision}/restore',
      path: {
        'slug': slug,
        'revision': revision,
      },
      query: {
        'scope': scope,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid policy restore environment, revision or audit reason.`,
        404: `The policy revision selected for restoration was not found.`,
        409: `The current readiness policy revision changed before restoration.`,
      },
    });
  }
  /**
   * Read the opt-in platform readiness promotion policy
   * @returns MCPReadinessPolicy Readiness result
   * @throws ApiError
   */
  public static getMcpReadinessPolicy({
    slug,
    scope,
  }: {
    /**
     * Application owning the requested current environment readiness policy.
     */
    slug: string,
    /**
     * Environment policy to select. Defaults to default; PUT body and query must agree.
     */
    scope?: string,
  }): CancelablePromise<MCPReadinessPolicy> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-readiness-policy',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
      },
      errors: {
        404: `Owned application not found`,
      },
    });
  }
  /**
   * Administratively configure readiness with a revision CAS and audit reason
   * Requires admin scope and applicable MFA. Every update creates a new revision and invalidates prior readiness evidence only in the selected environment. Deploy candidates at zero traffic before probing.
   * @returns MCPReadinessPolicy Readiness policy configuration saved as a new environment revision.
   * @throws ApiError
   */
  public static setMcpReadinessPolicy({
    slug,
    requestBody,
    scope,
  }: {
    /**
     * Application owning the requested current environment readiness policy.
     */
    slug: string,
    requestBody: SetMCPReadinessPolicyRequest,
    /**
     * Environment policy to select. Defaults to default; PUT body and query must agree.
     */
    scope?: string,
  }): CancelablePromise<MCPReadinessPolicy> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/mcp-readiness-policy',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid policy`,
        409: `Policy revision changed`,
      },
    });
  }
  /**
   * Read retained role evidence and emergency override audit for an owned deployment
   * @returns MCPReadinessSnapshot Retained policy-bound readiness evidence for the selected deployment.
   * @throws ApiError
   */
  public static getMcpReadiness({
    id,
  }: {
    /**
     * Deployment UUID selected for deployment readiness evidence and probes.
     */
    id: string,
  }): CancelablePromise<MCPReadinessSnapshot> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/mcp-readiness',
      path: {
        'id': id,
      },
      errors: {
        404: `Owned deployment not found`,
      },
    });
  }
  /**
   * Run a server-side read-only role probe and retain revision-bound readiness evidence
   * Requires deploy write scope and applicable MFA. A failed probe is retained with passed=false. Raw client observations are never accepted. The endpoint is platform-generated; credentials and response bodies are not retained.
   * @returns MCPReadinessEvidence Readiness evidence recorded from the completed deployment probe.
   * @throws ApiError
   */
  public static probeMcpReadiness({
    id,
    requestBody,
    mcpProbeAuthorization,
  }: {
    /**
     * Deployment UUID selected for deployment readiness evidence and probes.
     */
    id: string,
    requestBody: MCPReadinessProbeRequest,
    /**
     * Separate scoped MCP bearer token, never the platform API key.
     */
    mcpProbeAuthorization?: string,
  }): CancelablePromise<MCPReadinessEvidence> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/deployments/{id}/mcp-readiness',
      path: {
        'id': id,
      },
      headers: {
        'MCP-Probe-Authorization': mcpProbeAuthorization,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid or unconfigured role`,
        404: `Owned deployment or configured probe role was not found.`,
        409: `Policy or deployment changed during probe`,
        503: `Probe or evidence store unavailable`,
      },
    });
  }
  /**
   * Create an audited five-minute readiness override for one traffic increase
   * Requires admin scope and applicable MFA. Bound to the current artifact and policy revision. Other admission guards remain enforced. Consumed atomically with a successful traffic mutation.
   * @returns MCPReadinessOverride Audited readiness override created for this deployment.
   * @throws ApiError
   */
  public static overrideMcpReadiness({
    id,
    requestBody,
  }: {
    /**
     * Deployment UUID selected for audited deployment readiness overrides.
     */
    id: string,
    requestBody: MCPReadinessOverrideRequest,
  }): CancelablePromise<MCPReadinessOverride> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/deployments/{id}/mcp-readiness-overrides',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `Owned live deployment not found`,
        409: `Policy changed or is disabled`,
      },
    });
  }
  /**
   * Read a retained MCP admission receipt by evidence UUID
   * @returns MCPAdmissionEvidence Account-owned receipt
   * @throws ApiError
   */
  public static getMcpAdmissionReceipt({
    id,
  }: {
    /**
     * Evidence UUID identifying the retained MCP admission receipt
     */
    id: string,
  }): CancelablePromise<MCPAdmissionEvidence> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/mcp/admissions/{id}',
      path: {
        'id': id,
      },
      errors: {
        404: `Receipt missing or owned by another account`,
      },
    });
  }
  /**
   * Page retained MCP receipts newest first by audit sequence
   * @returns MCPAdmissionHistoryResponse Retained receipts; an omitted next_cursor means the end of matching history
   * @throws ApiError
   */
  public static listMcpAdmissionHistory({
    id,
    limit = 50,
    cursor,
    outcome,
  }: {
    /**
     * Deployment UUID whose admission receipts are listed
     */
    id: string,
    /**
     * Maximum number of matching admission receipts to return
     */
    limit?: number,
    /**
     * Continuation cursor from the previous page
     */
    cursor?: string,
    /**
     * Filter receipts by the recorded admission outcome
     */
    outcome?: 'accepted' | 'rejected',
  }): CancelablePromise<MCPAdmissionHistoryResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/deployments/{id}/mcp-admissions',
      path: {
        'id': id,
      },
      query: {
        'limit': limit,
        'cursor': cursor,
        'outcome': outcome,
      },
      errors: {
        400: `Invalid UUID or pagination filter`,
        404: `Deployment missing or owned by another account`,
      },
    });
  }
  /**
   * Account-wide MCP connection health and recovery guidance; administrator access required
   * @returns MCPConnectionHealthSnapshot Connection health metadata without credential values
   * @throws ApiError
   */
  public static getMcpConnectionHealth({
    app,
    offset,
  }: {
    /**
     * Optional owned application slug
     */
    app?: string,
    /**
     * Issue page offset
     */
    offset?: number,
  }): CancelablePromise<MCPConnectionHealthSnapshot> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/mcp-connection-health',
      query: {
        'app': app,
        'offset': offset,
      },
      errors: {
        400: `Invalid page offset`,
        403: `Administrator scope required for the account connection inventory`,
        404: `Application unavailable to this account`,
      },
    });
  }
  /**
   * Get fire-now request state
   * Polling surface for the row that `POST /v1/crons/{id}/run`
   * inserted (decision 7).
   *
   * @returns FireCronRequestResponse Current state of the fire-now request.
   * @throws ApiError
   */
  public static getFireCronRequest({
    requestId,
  }: {
    /**
     * Fire-now request identifier returned by `POST /v1/crons/{id}/run`.
     */
    requestId: string,
  }): CancelablePromise<FireCronRequestResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/cron-fire-now-requests/{request_id}',
      path: {
        'request_id': requestId,
      },
      errors: {
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
