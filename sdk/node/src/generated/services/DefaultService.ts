/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BeginToolPublicationResponse } from '../models/BeginToolPublicationResponse.js';
import type { DelegatedToolInvocationIntent } from '../models/DelegatedToolInvocationIntent.js';
import type { FireCronRequestResponse } from '../models/FireCronRequestResponse.js';
import type { ManagedRealtimeChannelBatchRequest } from '../models/ManagedRealtimeChannelBatchRequest.js';
import type { ManagedRealtimeChannelBatchResponse } from '../models/ManagedRealtimeChannelBatchResponse.js';
import type { ManagedRealtimeChannelSnapshotRequest } from '../models/ManagedRealtimeChannelSnapshotRequest.js';
import type { ManagedRealtimeChannelSnapshotResponse } from '../models/ManagedRealtimeChannelSnapshotResponse.js';
import type { ManagedRealtimeEventSchemaRequest } from '../models/ManagedRealtimeEventSchemaRequest.js';
import type { ManagedRealtimeEventSchemaResponse } from '../models/ManagedRealtimeEventSchemaResponse.js';
import type { ManagedRealtimeNotificationControlResponse } from '../models/ManagedRealtimeNotificationControlResponse.js';
import type { ManagedRealtimeNotificationRescheduleRequest } from '../models/ManagedRealtimeNotificationRescheduleRequest.js';
import type { ManagedRealtimeNotificationTimelineEvent } from '../models/ManagedRealtimeNotificationTimelineEvent.js';
import type { ManagedRealtimeReducerRequest } from '../models/ManagedRealtimeReducerRequest.js';
import type { ManagedRealtimeReducerResponse } from '../models/ManagedRealtimeReducerResponse.js';
import type { ManagedRealtimeScheduleGroupRequest } from '../models/ManagedRealtimeScheduleGroupRequest.js';
import type { ManagedRealtimeScheduleHistoryResponse } from '../models/ManagedRealtimeScheduleHistoryResponse.js';
import type { ManagedRealtimeSchedulePauseRequest } from '../models/ManagedRealtimeSchedulePauseRequest.js';
import type { ManagedRealtimeScheduleRequest } from '../models/ManagedRealtimeScheduleRequest.js';
import type { ManagedRealtimeScheduleResponse } from '../models/ManagedRealtimeScheduleResponse.js';
import type { ManagedRealtimeScheduleRetryRequest } from '../models/ManagedRealtimeScheduleRetryRequest.js';
import type { ManagedRealtimeSchedulesResponse } from '../models/ManagedRealtimeSchedulesResponse.js';
import type { ManagedRealtimeScheduleTotals } from '../models/ManagedRealtimeScheduleTotals.js';
import type { ManagedRealtimeScheduleUpdate } from '../models/ManagedRealtimeScheduleUpdate.js';
import type { ManagedRealtimeSignalRequest } from '../models/ManagedRealtimeSignalRequest.js';
import type { ManagedRealtimeSignalResponse } from '../models/ManagedRealtimeSignalResponse.js';
import type { MCPAdmissionEvidence } from '../models/MCPAdmissionEvidence.js';
import type { MCPAdmissionHistoryResponse } from '../models/MCPAdmissionHistoryResponse.js';
import type { MCPCatalogMonitorConfigRequest } from '../models/MCPCatalogMonitorConfigRequest.js';
import type { MCPCatalogMonitorSnapshot } from '../models/MCPCatalogMonitorSnapshot.js';
import type { MCPCatalogMonitorSummary } from '../models/MCPCatalogMonitorSummary.js';
import type { MCPCatalogOAuthRequest } from '../models/MCPCatalogOAuthRequest.js';
import type { MCPCatalogOAuthResponse } from '../models/MCPCatalogOAuthResponse.js';
import type { MCPCatalogObservationRequest } from '../models/MCPCatalogObservationRequest.js';
import type { MCPCatalogReceipt } from '../models/MCPCatalogReceipt.js';
import type { MCPConnectionHealthSnapshot } from '../models/MCPConnectionHealthSnapshot.js';
import type { MCPOAuthRetirement } from '../models/MCPOAuthRetirement.js';
import type { MCPOAuthRetirementAction } from '../models/MCPOAuthRetirementAction.js';
import type { MCPOAuthRetirementsResponse } from '../models/MCPOAuthRetirementsResponse.js';
import type { MCPReadinessEvidence } from '../models/MCPReadinessEvidence.js';
import type { MCPReadinessIncidentTimeline } from '../models/MCPReadinessIncidentTimeline.js';
import type { MCPReadinessOverride } from '../models/MCPReadinessOverride.js';
import type { MCPReadinessOverrideRequest } from '../models/MCPReadinessOverrideRequest.js';
import type { MCPReadinessPolicy } from '../models/MCPReadinessPolicy.js';
import type { MCPReadinessPolicyDiff } from '../models/MCPReadinessPolicyDiff.js';
import type { MCPReadinessPolicyHistory } from '../models/MCPReadinessPolicyHistory.js';
import type { MCPReadinessProbeRequest } from '../models/MCPReadinessProbeRequest.js';
import type { MCPReadinessSnapshot } from '../models/MCPReadinessSnapshot.js';
import type { PrivateToolInvocationRequest } from '../models/PrivateToolInvocationRequest.js';
import type { RestoreMCPReadinessPolicyRequest } from '../models/RestoreMCPReadinessPolicyRequest.js';
import type { SetMCPReadinessPolicyRequest } from '../models/SetMCPReadinessPolicyRequest.js';
import type { ToolGrantIntent } from '../models/ToolGrantIntent.js';
import type { ToolGrantPage } from '../models/ToolGrantPage.js';
import type { ToolGrantRecord } from '../models/ToolGrantRecord.js';
import type { ToolInvocationIntent } from '../models/ToolInvocationIntent.js';
import type { ToolInvocationOutcome } from '../models/ToolInvocationOutcome.js';
import type { ToolInvocationReceipt } from '../models/ToolInvocationReceipt.js';
import type { ToolInvocationRecord } from '../models/ToolInvocationRecord.js';
import type { ToolOAuthAuthorizationRequest } from '../models/ToolOAuthAuthorizationRequest.js';
import type { ToolOAuthAuthorizationResponse } from '../models/ToolOAuthAuthorizationResponse.js';
import type { ToolOAuthConnectionIntent } from '../models/ToolOAuthConnectionIntent.js';
import type { ToolOAuthConnectionPage } from '../models/ToolOAuthConnectionPage.js';
import type { ToolOAuthConnectionRecord } from '../models/ToolOAuthConnectionRecord.js';
import type { ToolOAuthConnectionStatus } from '../models/ToolOAuthConnectionStatus.js';
import type { ToolPublicationIntent } from '../models/ToolPublicationIntent.js';
import type { ToolPublicationOutcome } from '../models/ToolPublicationOutcome.js';
import type { ToolPublicationRecord } from '../models/ToolPublicationRecord.js';
import type { ToolRegistryPage } from '../models/ToolRegistryPage.js';
import type { ToolRegistryTool } from '../models/ToolRegistryTool.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
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
   * Read opt-in catalog monitoring state and retained transitions
   * @returns any Configured snapshot, or an exact receipt when evidence is supplied
   * @throws ApiError
   */
  public static getMcpCatalogMonitor({
    slug,
    role,
    allRoles = false,
    evidence,
  }: {
    /**
     * HTTP application whose catalog monitor is managed
     */
    slug: string,
    /**
     * Independent reviewed authorization view; omit for default
     */
    role?: string,
    /**
     * App summary of all roles; cannot combine with role or evidence
     */
    allRoles?: boolean,
    /**
     * Read one retained transition receipt
     */
    evidence?: string,
  }): CancelablePromise<(MCPCatalogMonitorSnapshot | MCPCatalogReceipt | MCPCatalogMonitorSummary)> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-catalog-monitor',
      path: {
        'slug': slug,
      },
      query: {
        'role': role,
        'all_roles': allRoles,
        'evidence': evidence,
      },
      errors: {
        404: `Monitor or evidence unavailable to this account`,
      },
    });
  }
  /**
   * Configure a reviewed role baseline and optional managed observer; secret binding also requires secrets-read scope
   * @returns MCPCatalogMonitorSnapshot Monitor revision; current observation is reset until this revision reports
   * @throws ApiError
   */
  public static configureMcpCatalogMonitor({
    slug,
    requestBody,
  }: {
    /**
     * HTTP application whose catalog monitor is managed
     */
    slug: string,
    requestBody: MCPCatalogMonitorConfigRequest,
  }): CancelablePromise<MCPCatalogMonitorSnapshot> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/mcp-catalog-monitor',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid complete baseline or freshness limit`,
        404: `App unavailable to this account`,
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
   * Read administrator-only OAuth retirement history including deleted applications
   * @returns MCPOAuthRetirementsResponse Up to fifty retirement records with sealed data omitted
   * @throws ApiError
   */
  public static listMcpoAuthRetirements({
    offset,
  }: {
    /**
     * Bounded history page offset
     */
    offset?: number,
  }): CancelablePromise<MCPOAuthRetirementsResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/mcp-oauth-retirements',
      query: {
        'offset': offset,
      },
      errors: {
        400: `Invalid offset`,
        403: `Administrator access required`,
      },
    });
  }
  /**
   * Begin, complete or disconnect guided OAuth for an observer role; requires deploy intent and secret-read permission
   * @returns MCPCatalogOAuthResponse Authorization URL or completed connection state without token values
   * @throws ApiError
   */
  public static mcpCatalogOAuth({
    slug,
    requestBody,
  }: {
    /**
     * Application owning the OAuth connection
     */
    slug: string,
    requestBody: MCPCatalogOAuthRequest,
  }): CancelablePromise<MCPCatalogOAuthResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/mcp-catalog-monitor/oauth',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid connection request or unsuccessful authorization`,
        403: `Missing connection permissions`,
        404: `App or role unavailable to this account`,
        409: `Expired, consumed or superseded authorization state`,
      },
    });
  }
  /**
   * Retry retirement or record provider-side manual revocation; administrator access and MFA policy apply
   * @returns MCPOAuthRetirement Updated public retirement status
   * @throws ApiError
   */
  public static recoverMcpoAuthRetirement({
    id,
    requestBody,
  }: {
    /**
     * Account-owned retirement identifier
     */
    id: string,
    requestBody: MCPOAuthRetirementAction,
  }): CancelablePromise<MCPOAuthRetirement> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/mcp-oauth-retirements/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid recovery action or unavailable credentials`,
        403: `Administrator access or MFA requirement not satisfied`,
        404: `Retirement unavailable to this account`,
        409: `Retirement is leased, terminal or expired`,
      },
    });
  }
  /**
   * Read a receipt through its exact active invocation grant
   * Personal agent or MCP-login key with apps-read only. Rechecks current key, grant, application ownership and project limits on every read. Only the original key and grant can read the retained sanitized runtime result. Revocation or expiry denies access. This operation never starts work.
   * @returns ToolInvocationRecord Grant-scoped receipt with private noncacheable runtime evidence
   * @throws ApiError
   */
  public static getDelegatedToolInvocation({
    id,
  }: {
    /**
     * Canonical nonzero UUID retained for one delegated attempt
     */
    id: string,
  }): CancelablePromise<ToolInvocationRecord> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/delegated-tool-invocations/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `Delegated receipt UUID is malformed`,
        403: `Personal agent connection or native read scope is missing`,
        404: `Delegated receipt or its current grant is inaccessible`,
      },
    });
  }
  /**
   * Record one explicitly approved open-tool call under an exact grant
   * Requires a personal apps-read agent key, an active allow-invocation grant for the exact version/tool, and native MFA policy. Does not require deploy-write. APID freezes grant, key, application and project identities. Only created=true permits one bounded worker launch; dispatch is a separate atomic transition. No application token, arbitrary URL or argument body is stored. Reusing the UUID never renews execution permission.
   * @returns ToolInvocationReceipt First delegated insertion or retained fact without another worker grant
   * @throws ApiError
   */
  public static beginDelegatedToolInvocation({
    id,
    requestBody,
  }: {
    /**
     * Canonical nonzero UUID retained for one delegated attempt
     */
    id: string,
    requestBody: DelegatedToolInvocationIntent,
  }): CancelablePromise<ToolInvocationReceipt> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/delegated-tool-invocations/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Delegated call consent or bounded intent is invalid`,
        403: `Delegated creation scope or MFA policy is not satisfied`,
        404: `Exact invocation grant, tool or native target is unavailable`,
        409: `Delegated request identity was reused with changed intent or grant`,
        429: `Shared account call concurrency or retained receipt quota reached`,
      },
    });
  }
  /**
   * Start one exact credential-bound private invocation; native read scope and MFA policy apply
   * Requires operator enablement and an active owner-created credential binding. Only a newly created native identity launches a bounded worker. Arguments are transient; no provider credential is returned or stored in the journal. Replays recover the retained fact without another execution. Rotation, deletion, expiry and revocation prevent subsequent dispatch or receipt access. Only the native worker may dispatch or report private outcomes.
   * @returns ToolInvocationReceipt Single-shot receipt; created authorizes only the native worker, never the caller
   * @throws ApiError
   */
  public static executePrivateToolInvocation({
    id,
    requestBody,
  }: {
    /**
     * Caller-retained canonical nonzero invocation UUID
     */
    id: string,
    requestBody: PrivateToolInvocationRequest,
  }): CancelablePromise<ToolInvocationReceipt> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/delegated-tool-invocations/{id}/execute',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid or oversized private invocation intent or arguments`,
        403: `Agent scope or MFA policy not satisfied`,
        404: `Exact tool grant or current credential binding is unavailable`,
        409: `Immutable invocation identity conflicts, deployment changed, or managed renewal is already in progress`,
        429: `Account active-call or retained-identity limit reached`,
        503: `Private invocation is disabled, native workers are unavailable, or the managed connection requires owner reconnection`,
      },
    });
  }
  /**
   * Dispatch, cancel or finish a call through its frozen invocation grant
   * Requires the original personal agent key, the same active invocation grant and native MFA policy. Dispatch locks the current grant, key, app and journal row before granting only the first queued-to-running transition. Lost dispatch replies are never replayed as permission. Cancellation after dispatch requests stopping the worker wait without rolling back remote effects. Outcomes are immutable; unknown outcomes never authorize retry.
   * @returns ToolInvocationReceipt Updated delegated journal fact with a fresh single-use dispatch flag
   * @throws ApiError
   */
  public static changeDelegatedToolInvocation({
    id,
    action,
    requestBody,
  }: {
    /**
     * Existing frozen delegated invocation identity
     */
    id: string,
    /**
     * Explicit delegated journal transition
     */
    action: 'dispatch' | 'cancel' | 'outcome',
    /**
     * Empty object to dispatch or cancel; complete bounded worker evidence to report an outcome.
     */
    requestBody: (Record<string, any> | ToolInvocationOutcome),
  }): CancelablePromise<ToolInvocationReceipt> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/delegated-tool-invocations/{id}/{action}',
      path: {
        'id': id,
        'action': action,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Delegated journal action or complete result is malformed`,
        403: `Delegated mutation authority or MFA policy was not satisfied`,
        404: `Original invocation binding or currently active grant cannot be accessed`,
        409: `Delegated target is unavailable or retained terminal evidence differs`,
      },
    });
  }
  /**
   * Read an account-owned invocation receipt and sanitized result
   * Requires an unscoped personal-account connection with deploy-write permission. Runtime results are not disclosed by registry-read permission. This read never dispatches work.
   * @returns ToolInvocationRecord Durable invocation fact with private noncacheable runtime data
   * @throws ApiError
   */
  public static getToolInvocation({
    id,
  }: {
    /**
     * Caller-retained UUID for the local registered-tool invocation journal
     */
    id: string,
  }): CancelablePromise<ToolInvocationRecord> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/tool-invocations/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `Malformed invocation identity`,
        403: `Runtime-data authority is missing`,
        404: `Invocation does not belong to this account`,
      },
    });
  }
  /**
   * Journal explicit consent for one exact registered tool invocation
   * Requires deploy-write permission and MFA policy. Only created=true authorizes starting one local worker. The native dispatch transition is still required before tools-call. Arguments and credentials are never stored in the intent.
   * @returns ToolInvocationReceipt New queued receipt or retained attempt without renewed execution authority
   * @throws ApiError
   */
  public static beginToolInvocation({
    id,
    requestBody,
  }: {
    /**
     * Caller-retained UUID for the local registered-tool invocation journal
     */
    id: string,
    requestBody: ToolInvocationIntent,
  }): CancelablePromise<ToolInvocationReceipt> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/tool-invocations/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid consent or incomplete publication metadata`,
        403: `Invocation creation permission or MFA policy is not satisfied`,
        404: `Exact publication or deployment is inaccessible`,
        409: `Request identity changed or exact version is unavailable`,
        429: `Account invocation concurrency or retained-identity limit reached`,
      },
    });
  }
  /**
   * Acquire single dispatch authority or report invocation cancellation and outcome
   * Requires deploy-write authority and MFA policy. Dispatch grants only the first queued-to-running transition after native target revalidation. Queued cancellation prevents dispatch; running cancellation requests stopping the local wait and cannot roll back remote effects. Terminal outcomes are immutable. Owner inspection and cancellation also cover delegated records; dispatch and outcome reporting for those records must use the original grant-scoped route.
   * @returns ToolInvocationReceipt Retained receipt; dispatch_granted is never replayed by this API
   * @throws ApiError
   */
  public static changeToolInvocation({
    id,
    action,
    requestBody,
  }: {
    /**
     * Existing account-owned invocation UUID
     */
    id: string,
    /**
     * Single-dispatch transition or explicit receipt mutation
     */
    action: 'dispatch' | 'cancel' | 'outcome',
    /**
     * Empty object for dispatch or cancel; an outcome object for outcome reporting.
     */
    requestBody: (Record<string, any> | ToolInvocationOutcome),
  }): CancelablePromise<ToolInvocationReceipt> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/tool-invocations/{id}/{action}',
      path: {
        'id': id,
        'action': action,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid transition or malformed bounded result evidence`,
        403: `Receipt mutation permission or MFA policy is not satisfied`,
        404: `Invocation or target deployment is unavailable to this account`,
        409: `Terminal evidence changed or dispatch binding no longer matches`,
      },
    });
  }
  /**
   * Read a private publication journal; requires account-wide apps-read scope
   * Unscoped personal-account connections only. Agent and MCP-login keys additionally require explicit deploy-write scope to inspect the account-wide publication journal. This does not grant registry discovery or invocation. An abandoned attempt is outcome_unknown after its deadline and never authorizes replay.
   * @returns ToolPublicationRecord Retained publication evidence; Cache-Control is no-store
   * @throws ApiError
   */
  public static getToolPublication({
    id,
  }: {
    /**
     * Immutable publication identity
     */
    id: string,
  }): CancelablePromise<ToolPublicationRecord> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/tool-publications/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `Invalid identity`,
        403: `Scoped principal cannot read account-wide evidence`,
        404: `Record unavailable to this account`,
      },
    });
  }
  /**
   * Journal a reviewed publication before execution; requires deploy-write scope and MFA policy
   * Only created=true authorizes the publisher to start once. Duplicate identities return created=false; changed reviews conflict. Local paths and credentials are excluded.
   * @returns BeginToolPublicationResponse Created flag and immutable journal record
   * @throws ApiError
   */
  public static beginToolPublication({
    id,
    requestBody,
  }: {
    /**
     * Immutable publication identity
     */
    id: string,
    requestBody: ToolPublicationIntent,
  }): CancelablePromise<BeginToolPublicationResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/tool-publications/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid or oversized review`,
        403: `Publication review requires account authority and the configured MFA policy`,
        409: `Identity already binds another review or app identity changed`,
        429: `Preview journal limit of 256 identities reached`,
      },
    });
  }
  /**
   * Retain immutable publication evidence; requires deploy-write scope and MFA policy
   * Publisher-attested runtime catalogs and tests are bound to a native owned serving deployment and source archive. Identical terminal writes are idempotent. This endpoint executes no tools.
   * @returns ToolPublicationRecord Final publication record
   * @throws ApiError
   */
  public static finishToolPublication({
    id,
    requestBody,
  }: {
    /**
     * Retained publication identity
     */
    id: string,
    requestBody: ToolPublicationOutcome,
  }): CancelablePromise<ToolPublicationRecord> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/tool-publications/{id}/outcome',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Catalog or test evidence does not match review`,
        403: `Account authority or MFA requirement not satisfied`,
        404: `Publication or deployment unavailable to this account`,
        409: `Outcome immutable or publisher deadline elapsed`,
      },
    });
  }
  /**
   * List retained execution OAuth connections as the personal administrator
   * Owner metadata only; no token values, ciphertext or renewal leases. This read never exchanges credentials.
   * @returns ToolOAuthConnectionPage Retained connections; Cache-Control is no-store
   * @throws ApiError
   */
  public static listToolOAuthConnections({
    cursor,
    limit = 20,
  }: {
    /**
     * Last retained connection UUID from next_cursor; omitted starts the owner-scoped list
     */
    cursor?: string,
    /**
     * Maximum retained OAuth connections returned for this page
     */
    limit?: number,
  }): CancelablePromise<ToolOAuthConnectionPage> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/tool-oauth-connections',
      query: {
        'cursor': cursor,
        'limit': limit,
      },
      errors: {
        400: `Invalid page bounds`,
        403: `Listing execution connections requires a personal administrator and current MFA policy`,
      },
    });
  }
  /**
   * Approve an immutable execution OAuth connection; administrator and MFA policy required
   * Binds existing ordinary default-environment app secret references and their ciphertext digests. No credentials are accepted. Identical intent retains the original state; changed intent requires a new UUID. Creating intent performs no provider request. Guided connections require separate browser authorization before exact tool grants. Monitoring OAuth consent is never reused.
   * @returns ToolOAuthConnectionRecord Retained owner configuration and credential-free renewal metadata
   * @throws ApiError
   */
  public static putToolOAuthConnection({
    id,
    requestBody,
  }: {
    /**
     * Owner-assigned immutable execution connection UUID; reconnect uses a new UUID
     */
    id: string,
    requestBody: ToolOAuthConnectionIntent,
  }): CancelablePromise<ToolOAuthConnectionRecord> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/tool-oauth-connections/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid immutable provider configuration or secret reference`,
        403: `Creating immutable execution authority requires personal administrator access and MFA`,
        404: `Application or ordinary source secret unavailable`,
        409: `UUID already names different immutable intent`,
        429: `Retained account or per-app connection capacity reached`,
      },
    });
  }
  /**
   * Inspect an exact execution OAuth connection as the personal administrator
   * @returns ToolOAuthConnectionRecord Credential-free metadata; no refresh is attempted
   * @throws ApiError
   */
  public static getToolOAuthConnection({
    id,
  }: {
    /**
     * Owner-assigned immutable execution connection UUID; reconnect uses a new UUID
     */
    id: string,
  }): CancelablePromise<ToolOAuthConnectionRecord> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/tool-oauth-connections/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `Invalid connection UUID`,
        403: `Reading this execution connection requires personal administrator access and MFA`,
        404: `Connection unavailable to this account`,
      },
    });
  }
  /**
   * Locally revoke execution use and discard stored renewed credentials
   * Irreversible and idempotent. Fences subsequent dispatch and renewal completion; cannot undo dispatched effects. Does not revoke the provider grant or delete the original app secret.
   * @returns ToolOAuthConnectionRecord Retained revoked connection metadata
   * @throws ApiError
   */
  public static revokeToolOAuthConnection({
    id,
  }: {
    /**
     * Owner-assigned immutable execution connection UUID; reconnect uses a new UUID
     */
    id: string,
  }): CancelablePromise<ToolOAuthConnectionRecord> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/tool-oauth-connections/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `Invalid revocation UUID`,
        403: `Revoking execution credentials requires personal administrator access and MFA`,
        404: `Execution connection to revoke is unavailable within the caller personal account`,
      },
    });
  }
  /**
   * Inspect a guided execution authorization attempt as the personal administrator
   * MFA policy applies. Returns phase and expiry without consent URLs, callback state, codes, verifiers or tokens. Never exchanges a code or renews a credential.
   * @returns ToolOAuthAuthorizationResponse Owner-only authorization status; Cache-Control is no-store
   * @throws ApiError
   */
  public static getToolOAuthAuthorization({
    id,
  }: {
    /**
     * Execution connection UUID whose single browser authorization attempt is inspected or consumed
     */
    id: string,
  }): CancelablePromise<ToolOAuthAuthorizationResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/tool-oauth-connections/{id}/authorization',
      path: {
        'id': id,
      },
      errors: {
        400: `Invalid identity or connection is not guided`,
        403: `Inspecting a PKCE authorization requires personal administrator access and MFA`,
        404: `No guided authorization is retained for this account and execution connection`,
      },
    });
  }
  /**
   * Begin or complete owner-approved execution OAuth with S256 PKCE
   * Owner and MFA policy required. One authorization attempt per fresh guided connection. Begin returns an expiring browser URL bound to an exact IPv4 loopback callback. Same pending begin and callback return the original URL. Completion consumes state and records an exclusive lease before code exchange; no code exchange is replayed after consumption, failure or restart. Issued tokens are sealed directly in Account-domain state, never app environment secrets. Successful provider authorization does not grant an agent access to any tool. Reconnect requires a new connection UUID and exact tool grants. Provider-side revocation is separate.
   * @returns ToolOAuthAuthorizationResponse Owner-only consent URL or final connection state; never includes provider tokens or verifiers
   * @throws ApiError
   */
  public static authorizeToolOAuthConnection({
    id,
    requestBody,
  }: {
    /**
     * Execution connection UUID whose single browser authorization attempt is inspected or consumed
     */
    id: string,
    requestBody: ToolOAuthAuthorizationRequest,
  }): CancelablePromise<ToolOAuthAuthorizationResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/tool-oauth-connections/{id}/authorization',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid action, callback, code or issuer binding`,
        403: `Issuing or consuming browser authorization requires personal administrator access and MFA`,
        404: `Connection or frozen app authority unavailable`,
        409: `Attempt consumed, identity already used, callback changed or connection revoked`,
        503: `Credential storage unavailable or fresh owner authorization required`,
      },
    });
  }
  /**
   * Read credential-free execution connection status through an exact active tool grant
   * Personal agent key, immutable app/project binding and exact invocation grant are checked on every read. No provider configuration, secret names, tokens or ciphertext are returned. Does not trigger renewal.
   * @returns ToolOAuthConnectionStatus Status bound to this exact granted tool; Cache-Control is no-store
   * @throws ApiError
   */
  public static getDelegatedToolConnection({
    grant,
  }: {
    /**
     * Exact active invocation-grant UUID bound to the calling personal agent key
     */
    grant: string,
  }): CancelablePromise<ToolOAuthConnectionStatus> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/delegated-tool-connections/{grant}',
      path: {
        'grant': grant,
      },
      errors: {
        400: `Invalid grant UUID`,
        403: `Personal agent read authority required`,
        404: `Grant or managed connection unavailable within current authority`,
      },
    });
  }
  /**
   * List retained tool grants as the unscoped personal administrator
   * Agent connections cannot enumerate delegation records. Revoked and expired grants remain readable by the owner.
   * @returns ToolGrantPage Retained grant page; Cache-Control is no-store
   * @throws ApiError
   */
  public static listToolGrants({
    cursor,
    limit = 20,
  }: {
    /**
     * Continuation from the previous page
     */
    cursor?: string,
    /**
     * Maximum grants per page
     */
    limit?: number,
  }): CancelablePromise<ToolGrantPage> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/tool-grants',
      query: {
        'cursor': cursor,
        'limit': limit,
      },
      errors: {
        400: `Invalid grant-list cursor or requested page bound`,
        403: `Unscoped personal administrator required`,
      },
    });
  }
  /**
   * Read an exact retained delegation as the personal administrator
   * @returns ToolGrantRecord Immutable grant intent and revocation metadata; Cache-Control is no-store
   * @throws ApiError
   */
  public static getToolGrant({
    id,
  }: {
    /**
     * Caller-retained canonical nonzero grant UUID
     */
    id: string,
  }): CancelablePromise<ToolGrantRecord> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/tool-grants/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `Invalid grant identity`,
        403: `Personal administrator needed to inspect a retained grant`,
        404: `Grant unavailable to this account`,
      },
    });
  }
  /**
   * Delegate one exact tool to an existing agent connection; administrator and MFA policy required
   * Grants one connection key an immutable version/tool within its existing project scope for at most 30 days. Identical requests return the retained record including revocation; changed intent conflicts. This endpoint never starts execution or exposes credentials.
   * @returns ToolGrantRecord Retained exact grant
   * @throws ApiError
   */
  public static putToolGrant({
    id,
    requestBody,
  }: {
    /**
     * Caller-retained canonical nonzero grant UUID
     */
    id: string,
    requestBody: ToolGrantIntent,
  }): CancelablePromise<ToolGrantRecord> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/tool-grants/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid binding or expiry beyond grant or credential lifetime`,
        403: `Personal administrator or MFA policy not satisfied`,
        404: `Connection or completed tool version unavailable within its project scope`,
        409: `Grant identity already names another immutable intent`,
        429: `Retained account or per-key grant limit reached`,
      },
    });
  }
  /**
   * Revoke exact delegated tool access; administrator and MFA policy required
   * Repeated revocation retains the original timestamp. Registry reads recheck authority on every request; this endpoint does not roll back external effects.
   * @returns ToolGrantRecord Retained revoked grant
   * @throws ApiError
   */
  public static revokeToolGrant({
    id,
  }: {
    /**
     * Caller-retained canonical nonzero grant UUID
     */
    id: string,
  }): CancelablePromise<ToolGrantRecord> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/tool-grants/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `Grant revocation requires a canonical nonzero UUID`,
        403: `Grant revocation exceeds current authority or MFA policy`,
        404: `No retained grant can be revoked by this personal account`,
      },
    });
  }
  /**
   * Read a single-tool catalog projection; apps-read and exact delegated access required for agents
   * Returns only the selected tool and its declared application scopes. Agents require a current exact grant, live personal connection key and matching current app/project identities. Full publication catalogs and unrelated test evidence remain owner-only. No execution or runtime availability is implied.
   * @returns ToolRegistryTool Canonical single-tool catalog with separate projected and original digests; Cache-Control is no-store
   * @throws ApiError
   */
  public static getRegistryTool({
    version,
    name,
  }: {
    /**
     * Exact immutable publication version
     */
    version: string,
    /**
     * Exact published tool name
     */
    name: string,
  }): CancelablePromise<ToolRegistryTool> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/tool-registry/{version}/tools/{name}',
      path: {
        'version': version,
        'name': name,
      },
      errors: {
        400: `Invalid version or tool name`,
        403: `Principal cannot use this discovery surface`,
        404: `Tool or exact grant unavailable within current authority`,
      },
    });
  }
  /**
   * Search private completed tool versions; apps-read plus exact grants for agent connections
   * Owners read account summaries. Personal agent connections see only actively granted version/tool names within their current project limits. Registry membership grants no invocation permission and does not establish current availability. Shared organizations and tenant/app principals remain excluded.
   * @returns ToolRegistryPage Version summaries; Cache-Control is no-store
   * @throws ApiError
   */
  public static searchToolRegistry({
    query,
    cursor,
    limit = 20,
  }: {
    /**
     * Case-insensitive app or tool-name substring
     */
    query?: string,
    /**
     * Continuation returned by the previous page
     */
    cursor?: string,
    /**
     * Number of publication versions
     */
    limit?: number,
  }): CancelablePromise<ToolRegistryPage> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/tool-registry',
      query: {
        'query': query,
        'cursor': cursor,
        'limit': limit,
      },
      errors: {
        400: `Invalid query or continuation`,
        403: `Principal cannot search this registry surface`,
        404: `Personal agent connection unavailable within current authority`,
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
   * Report an observed catalog or failed probe; requires metrics-write scope
   * @returns MCPCatalogReceipt Server-computed transition receipt; repeated unchanged observations reuse the receipt
   * @throws ApiError
   */
  public static reportMcpCatalogObservation({
    slug,
    requestBody,
  }: {
    /**
     * Application receiving the catalog observation
     */
    slug: string,
    requestBody: MCPCatalogObservationRequest,
  }): CancelablePromise<MCPCatalogReceipt> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/mcp-catalog-monitor/observations',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid observation`,
        404: `Monitor unavailable to this account`,
        409: `Monitor disabled or baseline revision changed`,
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
  /**
   * Send a backend signal to a private activity scope
   * Publishes to the canonical isolated activity channel for this parent and scope. Scope labels are at most 64 UTF-8 bytes and the encoded channel must fit 256 bytes. Subscribers require a separate read_activity authorization callback grant. Best-effort live delivery with no retained history, sequence, replay, or idempotency receipt. Sender member_id is backend. JSON data is limited to 2048 encoded bytes and the endpoint payload limit; request body is limited to 4096 bytes. Rate limit is 20 requests per second per endpoint/channel per API process, in addition to normal auth limits. Named signals default to a 5000 ms expiry; ttl_ms 0 clears a named signal. Deploy-write scopes and MFA apply. Acceptance is not subscriber acknowledgement; failures may follow partial delivery.
   * @returns ManagedRealtimeSignalResponse Best-effort fanout accepted, including when no subscribers exist
   * @throws ApiError
   */
  public static publishManagedRealtimeScopedSignal({
    slug,
    id,
    channel,
    activityScope,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Private activity scope
     */
    activityScope: string,
    requestBody: ManagedRealtimeSignalRequest,
  }): CancelablePromise<ManagedRealtimeSignalResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/activity-scopes/{activity_scope}/signals',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'activity_scope': activityScope,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
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
   * Send an ephemeral backend signal to connected v2 channel subscribers
   * Best-effort live delivery with no retained history, sequence, replay, or idempotency receipt. Sender member_id is backend. JSON data is limited to 2048 encoded bytes and the endpoint payload limit; request body is limited to 4096 bytes. Rate limit is 20 requests per second per endpoint/channel per API process, in addition to normal auth limits. Named signals default to a 5000 ms expiry; ttl_ms 0 clears a named signal. Deploy-write scopes and MFA apply. Acceptance is not subscriber acknowledgement; failures may follow partial delivery.
   * @returns ManagedRealtimeSignalResponse Channel signal fanout accepted even when no subscribers exist.
   * @throws ApiError
   */
  public static publishManagedRealtimeSignal({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    requestBody: ManagedRealtimeSignalRequest,
  }): CancelablePromise<ManagedRealtimeSignalResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/signals',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
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
   * List pending and recent terminal retained-event schedules
   * @returns ManagedRealtimeSchedulesResponse Schedules ordered by delivery time and ID
   * @throws ApiError
   */
  public static listManagedRealtimeSchedules({
    slug,
    id,
    channel,
    group,
    status,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Schedule group filter
     */
    group?: string,
    /**
     * Schedule status filter
     */
    status?: 'pending' | 'paused' | 'published' | 'failed' | 'skipped' | 'canceled',
  }): CancelablePromise<ManagedRealtimeSchedulesResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      query: {
        'group': group,
        'status': status,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Atomically pause, resume, or cancel a channel schedule group
   * Requires exactly every pending/paused member's version. Membership/version changes or incompatible transitions reject the entire action. Terminal members are excluded. Already-target-state members remain unchanged. Supports one-time and recurring schedules; deploy-write scopes, MFA and preview gating apply.
   * @returns any Pending/paused members included in the action, with updated states and totals
   * @throws ApiError
   */
  public static applyManagedRealtimeScheduleGroup({
    slug,
    id,
    channel,
    group,
    groupAction,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Schedule group
     */
    group: string,
    /**
     * Bulk schedule action
     */
    groupAction: 'pause' | 'resume' | 'cancel',
    requestBody: ManagedRealtimeScheduleGroupRequest,
  }): CancelablePromise<{
    schedules: Array<ManagedRealtimeScheduleResponse>;
    totals: ManagedRealtimeScheduleTotals;
  }> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/groups/{group}/{group_action}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
        'group_action': groupAction,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Schedule a retained channel event using an idempotent schedule ID
   * Future delivery within 30 days; timestamps normalize to microseconds.
   * Up to 256 pending and recent terminal schedules per endpoint. Terminal
   * receipts remain available for 24 hours. Identical ID/content retries
   * return the existing record; different content conflicts. Schema and reducer
   * validation run at delivery time. Preview gate and deploy-write scopes apply.
   *
   * @returns ManagedRealtimeScheduleResponse Pending schedule or identical existing record
   * @throws ApiError
   */
  public static putManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Bounded schedule identifier
     */
    scheduleId: string,
    requestBody: ManagedRealtimeScheduleRequest,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        409: `code: conflict`,
        429: `Endpoint schedule capacity reached`,
      },
    });
  }
  /**
   * Change a pending schedule deadline with a version check
   * @returns ManagedRealtimeScheduleResponse Updated pending schedule
   * @throws ApiError
   */
  public static rescheduleManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Bounded schedule identifier
     */
    scheduleId: string,
    requestBody: ManagedRealtimeScheduleUpdate,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Cancel a pending schedule with a version check
   * @returns ManagedRealtimeScheduleResponse Canceled schedule; cancellation retries are idempotent
   * @throws ApiError
   */
  public static cancelManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    expectedVersion,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Bounded schedule identifier
     */
    scheduleId: string,
    /**
     * Required current schedule version
     */
    expectedVersion: number,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      query: {
        'expected_version': expectedVersion,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Pause a recurring retained-event schedule
   * @returns ManagedRealtimeScheduleResponse Schedule updated
   * @throws ApiError
   */
  public static pauseManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Schedule ID
     */
    scheduleId: string,
    requestBody: ManagedRealtimeSchedulePauseRequest,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/pause',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Resume a recurring retained-event schedule
   * @returns ManagedRealtimeScheduleResponse Schedule resumed with its updated version.
   * @throws ApiError
   */
  public static resumeManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Schedule ID
     */
    scheduleId: string,
    requestBody: ManagedRealtimeSchedulePauseRequest,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/resume',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Inspect schedule changes and recorded delivery attempts
   * Events commit with schedule changes. Up to 128 entries are retained per
   * schedule, ordered by version. history_truncated marks missing earlier
   * history, including migrated baseline records. Terminal history expires
   * with its schedule receipt. Preview, ownership, MFA, and read scopes apply.
   *
   * @returns ManagedRealtimeScheduleHistoryResponse Consistent schedule timeline snapshot
   * @throws ApiError
   */
  public static getManagedRealtimeScheduleHistory({
    slug,
    id,
    channel,
    scheduleId,
    afterVersion,
    limit = 50,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Bounded schedule identifier
     */
    scheduleId: string,
    /**
     * Exclusive event version cursor
     */
    afterVersion?: number,
    /**
     * Maximum history records
     */
    limit?: number,
  }): CancelablePromise<ManagedRealtimeScheduleHistoryResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/history',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      query: {
        'after_version': afterVersion,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Retry a failed schedule using its existing ID and payload
   * Requires the current version. Resets the cycle attempt budget, preserves lifetime attempts, and advances the version. Published or canceled schedules cannot retry.
   * @returns ManagedRealtimeScheduleResponse Retry pending
   * @throws ApiError
   */
  public static retryManagedRealtimeSchedule({
    slug,
    id,
    channel,
    scheduleId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Bounded schedule identifier
     */
    scheduleId: string,
    requestBody: ManagedRealtimeScheduleRetryRequest,
  }): CancelablePromise<ManagedRealtimeScheduleResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/retry',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'schedule_id': scheduleId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Enable a channel reducer from a state baseline matching the current sequence
   * Retained JSON events support set, merge, delete, increment, append, and remove operations.
   * Increment payloads use op=increment, key, field, and a required integer delta;
   * optional min/max bounds reject results outside the allowed range.
   * Values, deltas, bounds, and results must be safe integers in
   * [-9007199254740991, 9007199254740991]. Missing entities or fields start at zero.
   * Increment supports expected_version and preserves expires_at unless replaced
   * or cleared with null. Array operations use key, field, and items (1..32 JSON values).
   * Append accepts unique=true for structural JSON deduplication; remove deletes
   * all matching items. Optional max_length (0..256) rejects longer results.
   * Missing fields start as empty arrays; existing targets must be arrays of at
   * most 256 items. Array operations support expected_version and the same
   * expires_at behavior as increment. Every operation accepts optional conditions
   * (1..16 predicates), each with a literal field and exactly one of equals (any
   * JSON value) or absent:true. All predicates must hold before the update.
   * Equality is structural and compares numbers exactly. Failed conditions return
   * 409 realtime_condition_conflict with entity_key, condition_field,
   * condition_index, message_index, current_version, entity_exists, and field_exists.
   * Invalid operations roll back the whole batch.
   *
   * @returns ManagedRealtimeReducerResponse Enabled reducer or identical current state
   * @throws ApiError
   */
  public static putManagedRealtimeReducer({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    requestBody: ManagedRealtimeReducerRequest,
  }): CancelablePromise<ManagedRealtimeReducerResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid state or size`,
        409: `Stale baseline or attempt to reseed an active reducer`,
      },
    });
  }
  /**
   * Read channel entities and their exact sequence
   * @returns ManagedRealtimeReducerResponse Current reduced state
   * @throws ApiError
   */
  public static getManagedRealtimeReducer({
    slug,
    id,
    channel,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
  }): CancelablePromise<ManagedRealtimeReducerResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      errors: {
        404: `Reducer not enabled`,
      },
    });
  }
  /**
   * Disable the reducer and remove its state and generated snapshot
   * @returns any Reducer disabled; retained history remains
   * @throws ApiError
   */
  public static deleteManagedRealtimeReducer({
    slug,
    id,
    channel,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
  }): CancelablePromise<any> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
    });
  }
  /**
   * Register an immutable event schema version and enable channel enforcement
   * @returns ManagedRealtimeEventSchemaResponse Registered version or identical existing version
   * @throws ApiError
   */
  public static putManagedRealtimeEventSchema({
    slug,
    id,
    channel,
    eventType,
    version,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Retained event type
     */
    eventType: string,
    /**
     * Current schema version
     */
    version: number,
    requestBody: ManagedRealtimeEventSchemaRequest,
  }): CancelablePromise<ManagedRealtimeEventSchemaResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schemas/{event_type}/{version}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'event_type': eventType,
        'version': version,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid schema or identity`,
        409: `Existing version has a different schema`,
        429: `Endpoint has 64 registered versions`,
      },
    });
  }
  /**
   * Get a registered event schema version
   * @returns ManagedRealtimeEventSchemaResponse Immutable schema
   * @throws ApiError
   */
  public static getManagedRealtimeEventSchema({
    slug,
    id,
    channel,
    eventType,
    version,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Retained event type
     */
    eventType: string,
    /**
     * Current schema version
     */
    version: number,
  }): CancelablePromise<ManagedRealtimeEventSchemaResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schemas/{event_type}/{version}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'event_type': eventType,
        'version': version,
      },
      errors: {
        404: `Version not registered`,
      },
    });
  }
  /**
   * Atomically retain a bounded batch, then attempt live delivery
   * @returns ManagedRealtimeChannelBatchResponse Durable batch, including per-message best-effort fanout outcomes
   * @throws ApiError
   */
  public static publishManagedRealtimeChannelBatch({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    requestBody: ManagedRealtimeChannelBatchRequest,
  }): CancelablePromise<ManagedRealtimeChannelBatchResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish-batch',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid batch count, size or ID`,
        409: `Sequence conflict includes current_sequence and expected_sequence; also batch ID conflict or channel capacity reached.`,
        429: `Endpoint has 256 retained batch IDs`,
      },
    });
  }
  /**
   * Get channel state and its replay baseline
   * @returns ManagedRealtimeChannelSnapshotResponse Snapshot with a replayable tail
   * @throws ApiError
   */
  public static getManagedRealtimeChannelSnapshot({
    slug,
    id,
    channel,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
  }): CancelablePromise<ManagedRealtimeChannelSnapshotResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/snapshot',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      errors: {
        404: `Snapshot or channel not found`,
        410: `Snapshot expired or tail no longer replayable`,
      },
    });
  }
  /**
   * Save state representing an explicit committed channel sequence
   * @returns ManagedRealtimeChannelSnapshotResponse Saved snapshot, expires after 24 hours
   * @throws ApiError
   */
  public static putManagedRealtimeChannelSnapshot({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    requestBody: ManagedRealtimeChannelSnapshotRequest,
  }): CancelablePromise<ManagedRealtimeChannelSnapshotResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/snapshot',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid sequence, size, or older snapshot overwrite`,
        404: `Channel has no retained history`,
        410: `Sequence no longer replayable`,
      },
    });
  }
  /**
   * Remove a snapshot without deleting channel history
   * @returns any Snapshot removed
   * @throws ApiError
   */
  public static deleteManagedRealtimeChannelSnapshot({
    slug,
    id,
    channel,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
  }): CancelablePromise<any> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/snapshot',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
    });
  }
  /**
   * List notification state changes across devices, newest first
   * @returns ManagedRealtimeNotificationTimelineEvent Up to 100 retained events, newest first; empty when none remain.
   * @throws ApiError
   */
  public static listManagedRealtimeNotificationTimeline({
    slug,
    id,
    messageId,
    principal,
    before,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Notification message ID
     */
    messageId: string,
    /**
     * Verified principal ID
     */
    principal: string,
    /**
     * Exclusive event ID cursor. Omit or zero for the newest page.
     */
    before?: number,
  }): CancelablePromise<Array<ManagedRealtimeNotificationTimelineEvent>> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}/timeline',
      path: {
        'slug': slug,
        'id': id,
        'message_id': messageId,
      },
      query: {
        'principal': principal,
        'before': before,
      },
      errors: {
        400: `Invalid principal, message ID or cursor`,
        404: `Endpoint or preview unavailable`,
      },
    });
  }
  /**
   * Cancel pending notification fallback and device deliveries without deleting inbox messages
   * @returns ManagedRealtimeNotificationControlResponse Counts of affected pending fallback timers and device deliveries; zero counts are an idempotent no-op.
   * @throws ApiError
   */
  public static cancelManagedRealtimeNotification({
    slug,
    id,
    messageId,
    principal,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Notification message ID
     */
    messageId: string,
    /**
     * Verified principal ID
     */
    principal: string,
  }): CancelablePromise<ManagedRealtimeNotificationControlResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}',
      path: {
        'slug': slug,
        'id': id,
        'message_id': messageId,
      },
      query: {
        'principal': principal,
      },
      errors: {
        400: `Invalid principal or message ID`,
        404: `Notification cancellation endpoint or preview unavailable.`,
      },
    });
  }
  /**
   * Reschedule active notifications across devices without reviving completed deliveries
   * @returns ManagedRealtimeNotificationControlResponse Counts of affected active timers and device deliveries.
   * @throws ApiError
   */
  public static rescheduleManagedRealtimeNotification({
    slug,
    id,
    messageId,
    principal,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Notification message ID
     */
    messageId: string,
    /**
     * Verified principal ID
     */
    principal: string,
    requestBody: ManagedRealtimeNotificationRescheduleRequest,
  }): CancelablePromise<ManagedRealtimeNotificationControlResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}',
      path: {
        'slug': slug,
        'id': id,
        'message_id': messageId,
      },
      query: {
        'principal': principal,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid schedule or schedule beyond expiration`,
        404: `Notification rescheduling endpoint or preview unavailable.`,
      },
    });
  }
}
