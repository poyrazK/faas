/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
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
import type { RestoreMCPReadinessPolicyRequest } from '../models/RestoreMCPReadinessPolicyRequest.js';
import type { SetMCPReadinessPolicyRequest } from '../models/SetMCPReadinessPolicyRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DefaultService {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
   * inserted (issue #791 PR-D / ADR-090 §Sub-decision 7).
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
    return __request(OpenAPI, {
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
