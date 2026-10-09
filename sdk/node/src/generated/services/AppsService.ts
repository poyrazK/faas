/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppBindingInventory } from '../models/AppBindingInventory.js';
import type { AppErrorRequestsResponse } from '../models/AppErrorRequestsResponse.js';
import type { AppErrorSampleResponse } from '../models/AppErrorSampleResponse.js';
import type { AppErrorsSummaryResponse } from '../models/AppErrorsSummaryResponse.js';
import type { AppHealthHistoryPage } from '../models/AppHealthHistoryPage.js';
import type { AppHealthResponse } from '../models/AppHealthResponse.js';
import type { AppMetricsResponse } from '../models/AppMetricsResponse.js';
import type { AppOperationalSummary } from '../models/AppOperationalSummary.js';
import type { AppResponse } from '../models/AppResponse.js';
import type { AppRestartResponse } from '../models/AppRestartResponse.js';
import type { AppRoutesResponse } from '../models/AppRoutesResponse.js';
import type { ApproveRouteLifecycleRequest } from '../models/ApproveRouteLifecycleRequest.js';
import type { ApproveRouteRemovalRequest } from '../models/ApproveRouteRemovalRequest.js';
import type { AppSLOResponse } from '../models/AppSLOResponse.js';
import type { AppsMetricsResponse } from '../models/AppsMetricsResponse.js';
import type { AppStreamingStatus } from '../models/AppStreamingStatus.js';
import type { AppUsageSummaryResponse } from '../models/AppUsageSummaryResponse.js';
import type { AppWakeResponse } from '../models/AppWakeResponse.js';
import type { AppWakeTimelineResponse } from '../models/AppWakeTimelineResponse.js';
import type { AutomaticRouteCheck } from '../models/AutomaticRouteCheck.js';
import type { BindingReleasePolicy } from '../models/BindingReleasePolicy.js';
import type { CanaryRouteGate } from '../models/CanaryRouteGate.js';
import type { CheckProfileRegressionRequest } from '../models/CheckProfileRegressionRequest.js';
import type { CheckRouteRequirementsRequest } from '../models/CheckRouteRequirementsRequest.js';
import type { CreateAppRequest } from '../models/CreateAppRequest.js';
import type { CreateDeployTokenRequest } from '../models/CreateDeployTokenRequest.js';
import type { CreateIssueIngestTokenRequest } from '../models/CreateIssueIngestTokenRequest.js';
import type { CreateTCPListenerRequest } from '../models/CreateTCPListenerRequest.js';
import type { CreateUDPListenerRequest } from '../models/CreateUDPListenerRequest.js';
import type { CustomMetricListResponse } from '../models/CustomMetricListResponse.js';
import type { CustomMetricRequest } from '../models/CustomMetricRequest.js';
import type { CustomMetricSeriesResponse } from '../models/CustomMetricSeriesResponse.js';
import type { DebugCompareRequest } from '../models/DebugCompareRequest.js';
import type { DebugCompareResponse } from '../models/DebugCompareResponse.js';
import type { DebugCoverageResponse } from '../models/DebugCoverageResponse.js';
import type { DebugCriticalPathHistoryResponse } from '../models/DebugCriticalPathHistoryResponse.js';
import type { DebugDependencyLatencyResponse } from '../models/DebugDependencyLatencyResponse.js';
import type { DebugRegressionActionRequest } from '../models/DebugRegressionActionRequest.js';
import type { DebugRegressionActionResponse } from '../models/DebugRegressionActionResponse.js';
import type { DebugRegressionsResponse } from '../models/DebugRegressionsResponse.js';
import type { DebugReplayRequest } from '../models/DebugReplayRequest.js';
import type { DebugReplayResponse } from '../models/DebugReplayResponse.js';
import type { DebugRequestEvidenceResponse } from '../models/DebugRequestEvidenceResponse.js';
import type { DebugRunningResponse } from '../models/DebugRunningResponse.js';
import type { DebugTelemetryListResponse } from '../models/DebugTelemetryListResponse.js';
import type { DebugTelemetryRequestItem } from '../models/DebugTelemetryRequestItem.js';
import type { DeployTokenResponse } from '../models/DeployTokenResponse.js';
import type { DiscoveredRoutesResponse } from '../models/DiscoveredRoutesResponse.js';
import type { Issue } from '../models/Issue.js';
import type { IssueActionRequest } from '../models/IssueActionRequest.js';
import type { IssueDetail } from '../models/IssueDetail.js';
import type { IssueEvent } from '../models/IssueEvent.js';
import type { IssueEventResponse } from '../models/IssueEventResponse.js';
import type { IssueImpactAlertPolicy } from '../models/IssueImpactAlertPolicy.js';
import type { IssueIngestToken } from '../models/IssueIngestToken.js';
import type { IssueOwnershipRules } from '../models/IssueOwnershipRules.js';
import type { ListDeployTokensResponse } from '../models/ListDeployTokensResponse.js';
import type { ListIssueIngestTokensResponse } from '../models/ListIssueIngestTokensResponse.js';
import type { ListIssuesResponse } from '../models/ListIssuesResponse.js';
import type { ListProfileDeploymentChecksResponse } from '../models/ListProfileDeploymentChecksResponse.js';
import type { ListProfileInvestigationsResponse } from '../models/ListProfileInvestigationsResponse.js';
import type { ListProfilePeriodicMonitorsResponse } from '../models/ListProfilePeriodicMonitorsResponse.js';
import type { PreAuthObservationsResponse } from '../models/PreAuthObservationsResponse.js';
import type { PreviewRouteMonitorRequest } from '../models/PreviewRouteMonitorRequest.js';
import type { PrewarmIntentResponse } from '../models/PrewarmIntentResponse.js';
import type { PrewarmRequest } from '../models/PrewarmRequest.js';
import type { Problem } from '../models/Problem.js';
import type { ProfileCanaryHistoryPage } from '../models/ProfileCanaryHistoryPage.js';
import type { ProfileCompareRequest } from '../models/ProfileCompareRequest.js';
import type { ProfileCompareResponse } from '../models/ProfileCompareResponse.js';
import type { ProfileDeploymentCheck } from '../models/ProfileDeploymentCheck.js';
import type { ProfileDeploymentPolicy } from '../models/ProfileDeploymentPolicy.js';
import type { ProfileInvestigationResponse } from '../models/ProfileInvestigationResponse.js';
import type { ProfileResponse } from '../models/ProfileResponse.js';
import type { RenameAppRequest } from '../models/RenameAppRequest.js';
import type { RequestAnalyticsResponse } from '../models/RequestAnalyticsResponse.js';
import type { RequestAnalyticsTimeseriesResponse } from '../models/RequestAnalyticsTimeseriesResponse.js';
import type { RequestAuditListResponse } from '../models/RequestAuditListResponse.js';
import type { RotateDeployTokenRequest } from '../models/RotateDeployTokenRequest.js';
import type { RotateDeployTokenResponse } from '../models/RotateDeployTokenResponse.js';
import type { RouteCheckHistoryEntry } from '../models/RouteCheckHistoryEntry.js';
import type { RouteCheckHistoryPage } from '../models/RouteCheckHistoryPage.js';
import type { RouteCustomerUsageResponse } from '../models/RouteCustomerUsageResponse.js';
import type { RouteHealthGate } from '../models/RouteHealthGate.js';
import type { RouteHealthHistoryEntry } from '../models/RouteHealthHistoryEntry.js';
import type { RouteHealthHistoryPage } from '../models/RouteHealthHistoryPage.js';
import type { RouteHealthInvestigation } from '../models/RouteHealthInvestigation.js';
import type { RouteHealthReport } from '../models/RouteHealthReport.js';
import type { RouteLifecycleApproval } from '../models/RouteLifecycleApproval.js';
import type { RouteLifecycleHistoryPage } from '../models/RouteLifecycleHistoryPage.js';
import type { RouteMonitorConfig } from '../models/RouteMonitorConfig.js';
import type { RouteMonitorIncident } from '../models/RouteMonitorIncident.js';
import type { RouteMonitorIncidentPage } from '../models/RouteMonitorIncidentPage.js';
import type { RouteMonitorPreview } from '../models/RouteMonitorPreview.js';
import type { RouteMonitorReport } from '../models/RouteMonitorReport.js';
import type { RoutePolicyApplyRequest } from '../models/RoutePolicyApplyRequest.js';
import type { RoutePolicyApplyResponse } from '../models/RoutePolicyApplyResponse.js';
import type { RoutePolicyPlan } from '../models/RoutePolicyPlan.js';
import type { RoutePolicyPlanRequest } from '../models/RoutePolicyPlanRequest.js';
import type { RoutePolicyReceipt } from '../models/RoutePolicyReceipt.js';
import type { RouteRemovalApproval } from '../models/RouteRemovalApproval.js';
import type { RouteRemovalCheck } from '../models/RouteRemovalCheck.js';
import type { RouteRemovalPolicy } from '../models/RouteRemovalPolicy.js';
import type { RouteRequirementsCheck } from '../models/RouteRequirementsCheck.js';
import type { RuntimeConfigRestartStatusResponse } from '../models/RuntimeConfigRestartStatusResponse.js';
import type { RuntimePolicyStatusResponse } from '../models/RuntimePolicyStatusResponse.js';
import type { SavedRouteRequirements } from '../models/SavedRouteRequirements.js';
import type { SaveProfileDeploymentPolicyRequest } from '../models/SaveProfileDeploymentPolicyRequest.js';
import type { SaveProfileInvestigationRequest } from '../models/SaveProfileInvestigationRequest.js';
import type { SaveRouteRequirementsRequest } from '../models/SaveRouteRequirementsRequest.js';
import type { SetBindingReleasePolicyRequest } from '../models/SetBindingReleasePolicyRequest.js';
import type { SetCanaryRouteGateRequest } from '../models/SetCanaryRouteGateRequest.js';
import type { SetRouteHealthGateRequest } from '../models/SetRouteHealthGateRequest.js';
import type { SetRouteMonitorRequest } from '../models/SetRouteMonitorRequest.js';
import type { SetRouteRemovalPolicyRequest } from '../models/SetRouteRemovalPolicyRequest.js';
import type { SidecarTimelineResponse } from '../models/SidecarTimelineResponse.js';
import type { TCPListenerResponse } from '../models/TCPListenerResponse.js';
import type { TCPListenerTLSStatusResponse } from '../models/TCPListenerTLSStatusResponse.js';
import type { UDPListenerResponse } from '../models/UDPListenerResponse.js';
import type { UpdateAppRequest } from '../models/UpdateAppRequest.js';
import type { UpdateIssueImpactAlertPolicyRequest } from '../models/UpdateIssueImpactAlertPolicyRequest.js';
import type { UpdateTCPListenerRequest } from '../models/UpdateTCPListenerRequest.js';
import type { UpdateUDPListenerRequest } from '../models/UpdateUDPListenerRequest.js';
import type { WakeTimelineResponse } from '../models/WakeTimelineResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class AppsService {
  /**
   * Read a scope's stored binding release policy.
   * Requires apps:read or admin and completed MFA. Defaults to off with revision 0. No probes or provider calls occur.
   * @returns BindingReleasePolicy Stored policy, or the disabled default.
   * @throws ApiError
   */
  public static getBindingReleasePolicy({
    slug,
    scope = 'default',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Exact deployment scope. Omit to select default.
     */
    scope?: string,
  }): CancelablePromise<BindingReleasePolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/bindings/release-policy',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Replace a binding release policy using its current revision.
   * Requires deploy:write or admin and completed MFA. expected_revision is
   * mandatory (0 initially); each accepted write increments it. Enforcement
   * requires complete fresh verification on every deployment gaining traffic,
   * including through redistribution. Promotion, direct traffic PATCH and
   * canary advance evaluate the policy; request flags may only strengthen it.
   * New candidates must be admitted with explicit zero traffic. Automatic
   * cutovers, legacy recovery and project release graph switches fail closed
   * until they support binding fences. To recover without evidence, explicitly
   * set off with the current revision and a reason, then retry. Every update
   * has durable policy history. Existing traffic is not changed by this call.
   *
   * @returns BindingReleasePolicy Saved policy with incremented revision.
   * @throws ApiError
   */
  public static setBindingReleasePolicy({
    slug,
    requestBody,
    scope = 'default',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: SetBindingReleasePolicyRequest,
    /**
     * Exact deployment scope. Omit to select default.
     */
    scope?: string,
  }): CancelablePromise<BindingReleasePolicy> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/bindings/release-policy',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Inspect all runtime resource bindings attached to an app.
   * Read-only, best-effort metadata for service, PostgreSQL, object-storage,
   * queue and outbound bindings. Requires apps:read or admin. PostgreSQL
   * sections additionally require managed-postgres:read or admin; object
   * storage sections require storage:manage or admin, matching the existing
   * compute-binding read surface. MFA-pending sessions are rejected.
   * Missing permissions and failed sections appear as structured issues in
   * a 200 response with complete=false; successfully read sections remain.
   * An unconfigured managed PostgreSQL feature is a warning. Other issues
   * have error severity. No provider calls, workload probes or writes occur.
   * State describes configuration/provisioning, not applied runtime health.
   * Runtime status is unknown unless a scheduler queue observation exists;
   * verification status includes durable service, PostgreSQL and object-storage task-guest canary results. GeneratedAt is collection time, not a
   * promise of an atomic snapshot. Credential material and raw errors are
   * excluded. Responses use Cache-Control: no-store.
   *
   * @returns AppBindingInventory Complete or partial binding inventory, without credentials.
   * @throws ApiError
   */
  public static getAppBindingInventory({
    slug,
    deploymentId,
    scope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Select evidence for this exact app-owned, materialized live deployment, including zero-traffic candidates. No fallback to evidence from another deployment. Omit to use the manual-task selection.
     */
    deploymentId?: string,
    /**
     * Filter database and bucket binding scopes. Omit to include all scopes. App-wide service, queue and outbound bindings always remain included.
     */
    scope?: string,
  }): CancelablePromise<AppBindingInventory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/bindings',
      path: {
        'slug': slug,
      },
      query: {
        'deployment_id': deploymentId,
        'scope': scope,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List apps on the account.
   * @returns AppResponse Apps on the account.
   * @throws ApiError
   */
  public static listApps(): CancelablePromise<Array<AppResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Create an app.
   * @returns AppResponse The new app.
   * @throws ApiError
   */
  public static createApp({
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App creation payload (slug, type, runtime, RAM, …). See CreateAppRequest schema.
     */
    requestBody: CreateAppRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied | email_verification_required`,
        409: `code: conflict`,
        422: `code: invalid_cpu_ram_pair — explicit ram_mb and vcpu do not match the canonical shape for the account plan.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Fetch one app.
   * @returns AppResponse The app.
   * @throws ApiError
   */
  public static getApp({
    slug,
    environment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Read or edit desired settings in this project environment. New deployments pin the revision; existing deployments keep their settings. Omit for legacy app settings.
     */
    environment?: string,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
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
   * Partial-update an app.
   * @returns AppResponse The updated app.
   * @throws ApiError
   */
  public static updateApp({
    slug,
    requestBody,
    environment,
    ifWorkloadRevision,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Patch payload — every field is optional; omitted fields are unchanged. See UpdateAppRequest.
     */
    requestBody: UpdateAppRequest,
    /**
     * Edit desired workload settings in a registered project environment; deploy again to test the revision. Omit to update legacy app settings.
     */
    environment?: string,
    /**
     * Optional expected desired revision when editing an environment; zero means no revision exists. Protected environments require an approved plan or promotion.
     */
    ifWorkloadRevision?: number,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}',
      path: {
        'slug': slug,
      },
      headers: {
        'If-Workload-Revision': ifWorkloadRevision,
      },
      query: {
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: invalid_min_instances — must be in [0, plan max_concurrency].`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Delete an app.
   * @returns void
   * @throws ApiError
   */
  public static deleteApp({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}',
      path: {
        'slug': slug,
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
   * Check whether runtime policy changes have reached their serving consumers.
   * Reports desired/applied positions for the gateway request envelope,
   * gateway app-cache and traffic policy, edge rules, account CORS presets,
   * explicit response-cache purges, app egress allowlists on nodes hosting
   * live instances, and scheduler scaling-policy observation. The
   * `request_policy` component covers app-row request settings such as
   * request timeout and concurrency, and excludes deployment-traffic
   * revisions. A response-cache purge is active only after every serving
   * gateway has invalidated its local cache and optional shared tier. The
   * top-level state and gateway counts remain the app-cache/traffic
   * projection; use each named component for its own convergence state.
   * `active` requires fresh observations from every relevant serving
   * consumer. The egress allowlist is replayed from current app state by
   * schedd if a notification is missed. Scheduler scaling `active` means
   * the owning schedd loaded the policy, not that the replica target was
   * reached. This does not attest host-level firewall policy or guest
   * configuration.
   * `unverified` means no revision or no relevant serving fleet can be observed.
   *
   * @returns RuntimePolicyStatusResponse Current application state; pending remains possible after wait expires.
   * @throws ApiError
   */
  public static getRuntimePolicyStatus({
    slug,
    wait,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Optional bounded wait for active state, up to 10s.
     */
    wait?: string,
  }): CancelablePromise<RuntimePolicyStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/policy/status',
      path: {
        'slug': slug,
      },
      query: {
        'wait': wait,
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
   * List an app's raw TCP listeners.
   * Lists stable public TCP endpoints owned by the app. Each listener
   * forwards its public port to the declared guest port; disabled
   * listeners fail closed at the edge while retaining their public port.
   *
   * @returns TCPListenerResponse The app's TCP listeners.
   * @throws ApiError
   */
  public static listAppTcpListeners({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<TCPListenerResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/tcp-listeners',
      path: {
        'slug': slug,
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
   * Expose one app port over raw TCP.
   * Creates a TCP listener. Passthrough is enabled immediately; TLS
   * termination starts disabled and requires a verified app-owned domain
   * and an explicitly provisioned edge certificate before enabling.
   * `public_port` is optional; when it
   * is omitted Gregale allocates a free port from the reserved
   * 40000–49999 range. Public ports remain stable across instance wake,
   * migration, and redeployments. `name` and `guest_port` must match an
   * explicitly declared TCP listener in the app manifest. Unnamed
   * declarations use the deterministic name `tcp-<guest_port>`.
   *
   * @returns TCPListenerResponse The created TCP listener.
   * @throws ApiError
   */
  public static createAppTcpListener({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateTCPListenerRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<TCPListenerResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/tcp-listeners',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
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
   * Change serving state or TLS policy of an app TCP listener.
   * Supply exactly one of enabled or tls. TLS policy changes atomically disable the endpoint and cancel existing sessions; provision the selected certificate policy before explicitly re-enabling. Disabling retains the stable public port.
   * @returns TCPListenerResponse The updated TCP listener.
   * @throws ApiError
   */
  public static updateAppTcpListener({
    slug,
    name,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Listener name within the app.
     */
    name: string,
    requestBody: UpdateTCPListenerRequest,
  }): CancelablePromise<TCPListenerResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/tcp-listeners/{name}',
      path: {
        'slug': slug,
        'name': name,
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
   * Delete an app TCP listener.
   * @returns void
   * @throws ApiError
   */
  public static deleteAppTcpListener({
    slug,
    name,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Listener name within the app.
     */
    name: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/tcp-listeners/{name}',
      path: {
        'slug': slug,
        'name': name,
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
   * Read observed edge certificate status for a TCP listener.
   * Returns customer-safe certificate evidence for each observed edge.
   * Missing evidence, evidence at least sixty seconds old, and changed or
   * disabled TLS intent have unknown status. This does not establish fleet
   * coverage, client trust, public routing or guest availability.
   *
   * @returns TCPListenerTLSStatusResponse Certificate evidence from observed edges only.
   * @throws ApiError
   */
  public static appTcpListenerTlsStatus({
    slug,
    name,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * TCP endpoint whose observed certificates are requested.
     */
    name: string,
  }): CancelablePromise<TCPListenerTLSStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/tcp-listeners/{name}/tls-status',
      path: {
        'slug': slug,
        'name': name,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Certificate observation storage is unavailable.`,
      },
    });
  }
  /**
   * List an app's public UDP listeners.
   * @returns UDPListenerResponse Stable app-owned datagram endpoints.
   * @throws ApiError
   */
  public static listAppUdpListeners({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<UDPListenerResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/udp-listeners',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Reserve an app-owned public UDP endpoint.
   * Requires a matching declared UDP workload port. New endpoints start disabled and require an explicit enable request plus the operator's source-CIDR rollout.
   * @returns UDPListenerResponse The reserved disabled UDP endpoint.
   * @throws ApiError
   */
  public static createAppUdpListener({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateUDPListenerRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<UDPListenerResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/udp-listeners',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Enable or disable a public UDP listener.
   * @returns UDPListenerResponse The UDP endpoint's updated serving state.
   * @throws ApiError
   */
  public static updateAppUdpListener({
    slug,
    name,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UDP workload listener selected for mutation.
     */
    name: string,
    requestBody: UpdateUDPListenerRequest,
  }): CancelablePromise<UDPListenerResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/udp-listeners/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Delete a public UDP listener.
   * @returns void
   * @throws ApiError
   */
  public static deleteAppUdpListener({
    slug,
    name,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UDP workload listener selected for mutation.
     */
    name: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/udp-listeners/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Restore an app during its deletion grace window.
   * @returns AppResponse The restored app.
   * @throws ApiError
   */
  public static restoreApp({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/restore',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
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
   * List deploy tokens for an app.
   * @returns ListDeployTokensResponse Deploy-token metadata. Plaintexts are never returned by list.
   * @throws ApiError
   */
  public static listDeployTokens({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListDeployTokensResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deploy-tokens',
      path: {
        'slug': slug,
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
   * Create a deploy token scoped to this app.
   * @returns DeployTokenResponse Token metadata and plaintext. Capture plaintext immediately; it is never returned again.
   * @throws ApiError
   */
  public static createDeployToken({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody?: CreateDeployTokenRequest,
  }): CancelablePromise<DeployTokenResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/deploy-tokens',
      path: {
        'slug': slug,
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
   * Revoke an app deploy token.
   * @returns void
   * @throws ApiError
   */
  public static revokeDeployToken({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deploy-token identifier returned by the list or create endpoint.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/deploy-tokens/{id}',
      path: {
        'slug': slug,
        'id': id,
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
   * Rotate an app deploy token.
   * @returns RotateDeployTokenResponse New token metadata and plaintext. The predecessor is revoked atomically.
   * @throws ApiError
   */
  public static rotateDeployToken({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deploy-token identifier to rotate, returned by the list or create endpoint.
     */
    id: string,
    requestBody?: RotateDeployTokenRequest,
  }): CancelablePromise<RotateDeployTokenResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/deploy-tokens/{id}/rotate',
      path: {
        'slug': slug,
        'id': id,
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
   * List the app's pushed custom metrics (ADR-202)
   * Returns every stored custom metric for the app, including rows whose last push is older than the freshness window. Stale rows are returned with `stale: true` rather than hidden — an operator debugging "why isn't my custom target scaling" needs to see that the value is old, because a hidden expired row looks identical to a missing one. Distinct from GET /v1/apps/{slug}/metrics, which serves the per-app Prometheus rollup of what the PLATFORM measured.
   * @returns CustomMetricListResponse The app's custom metrics.
   * @throws ApiError
   */
  public static listCustomMetrics({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<CustomMetricListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/custom-metrics',
      path: {
        'slug': slug,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Push a custom application metric (ADR-202)
   * Upserts one customer-pushed gauge, used as a scaling signal by a `metric: custom` target. The caller is frequently NOT the app — a cron, a database trigger, or the customer's own infrastructure — which is the point: a parked app has no process, so a scale-to-zero platform whose custom signal required a running instance could never scale from zero on it. The value is FLEET-TOTAL; the scheduler computes ceil(value / target). Pushing a name the app already holds always succeeds (it is an upsert); only a NEW name can hit the per-app cap.
   * @returns void
   * @throws ApiError
   */
  public static putCustomMetric({
    slug,
    name,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Metric name. Must match [a-z][a-z0-9_]{0,62}.
     */
    name: string,
    requestBody: CustomMetricRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/custom-metrics/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        404: `code: not_found`,
        422: `code: validation | custom_metric_limit — a malformed name or value, or a push of a NEW metric name by an app already at MaxCustomMetricsPerApp. A push to an EXISTING name never produces the limit error: it is an upsert and cannot grow the count.`,
      },
    });
  }
  /**
   * Delete a custom application metric (ADR-202)
   * Removes one stored gauge, freeing a slot against the per-app cap. Deleting a name that does not exist returns 204, not 404: the caller's intent is "this metric is gone", which is already true, and a 404 would make a retry of a successful delete look like a failure.
   * @returns void
   * @throws ApiError
   */
  public static deleteCustomMetric({
    slug,
    name,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Metric name. Must match [a-z][a-z0-9_]{0,62}.
     */
    name: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/custom-metrics/{name}',
      path: {
        'slug': slug,
        'name': name,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read a custom metric's history (ADR-745)
   * ADR-745 internal preview, enabled by `FAAS_CUSTOM_METRIC_HISTORY_ENABLED=1`;
   * otherwise 503 `custom_metric_history_unavailable`.
   *
   * Returns the values Prometheus recorded from pushed custom metrics over
   * `range` (`1h`, `6h`, `24h` default, `7d`, `15d`). Only fresh pushes are
   * recorded, so a gap means nothing was pushed then. Prometheus failure
   * returns 200 with `source: "degraded: <reason>"` and null `points`.
   *
   * @returns CustomMetricSeriesResponse The recorded history.
   * @throws ApiError
   */
  public static getCustomMetricSeries({
    slug,
    name,
    range = '24h',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * The pushed custom metric whose history to read.
     */
    name: string,
    /**
     * History window. Default 24h.
     */
    range?: '1h' | '6h' | '24h' | '7d' | '15d',
  }): CancelablePromise<CustomMetricSeriesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/custom-metrics/{name}/series',
      path: {
        'slug': slug,
        'name': name,
      },
      query: {
        'range': range,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Ingest OTLP/HTTP metrics as custom metrics (ADR-745)
   * ADR-745 internal preview, enabled by `FAAS_CUSTOM_METRIC_HISTORY_ENABLED=1`;
   * otherwise 503. Point an OpenTelemetry exporter's metrics endpoint here
   * with a `metrics:write` API key as the bearer token.
   *
   * Accepts an OTLP `ExportMetricsServiceRequest` as `application/x-protobuf`
   * or `application/json` (at most 1 MiB) and answers in the same encoding.
   * Gauges are stored as gauges and cumulative monotonic sums as counters;
   * metric names are lowercased with `.`, `-` and `/` mapped to `_`. Delta
   * or non-monotonic sums, histograms, data points with attributes,
   * negative values, and names beyond the per-app limit are not stored and
   * are reported through `partialSuccess`.
   *
   * @returns any The export was processed; rejected data points are reported in partialSuccess.
   * @throws ApiError
   */
  public static postOtlpMetrics({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: Record<string, any>,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/otlp/v1/metrics',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Observe-mode decisions for each configured pre-auth policy.
   * Read-only security telemetry, available on every app plan. Each policy
   * reports how often observe mode would have blocked a request and the
   * final response class of those requests. A 2xx response is a possible
   * false-positive signal, not proof that the requester was legitimate.
   * Route policy IDs are bounded slots (route_0..route_15 and
   * failures_0..failures_15). Reordering or replacing routes within the
   * requested range can mix counts from different configurations;
   * use a window after the last policy edit. Empty counts may mean no
   * traffic. On Prometheus failure,
   * source starts with `degraded:` and counts are zero.
   *
   * @returns PreAuthObservationsResponse Per-policy shadow decisions and final response classes.
   * @throws ApiError
   */
  public static getAppPreAuthObservations({
    slug,
    range = '5m',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Time window for the returned policy observations. Defaults to five minutes.
     */
    range?: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d',
  }): CancelablePromise<PreAuthObservationsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/pre-auth-observations',
      path: {
        'slug': slug,
      },
      query: {
        'range': range,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Explain observed app serving health.
   * Read-only assessment of default-scope HTTP serving deployments,
   * replica readiness, node evidence and the last 5 minutes of request
   * telemetry scoped to current traffic-bearing default releases. It never wakes
   * or probes a workload. Structural evidence is available on every plan;
   * request telemetry follows the existing Hobby+ metrics entitlement.
   * Missing, failed, stale or truncated evidence cannot confirm health.
   * A failed latest release does not erase older serving evidence.
   * Scale-to-zero idle is expected when no warm replicas are required.
   * Worker and job execution health is not assessed.
   * Requires apps:read or admin scope. No MFA required.
   *
   * @returns AppHealthResponse Evidence assessment, including unknown checks.
   * @throws ApiError
   */
  public static getAppHealth({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppHealthResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/health',
      path: {
        'slug': slug,
      },
      errors: {
        401: `Authentication required.`,
        403: `Read scope required.`,
        404: `App not found for this account.`,
        429: `Rate limit exceeded.`,
      },
    });
  }
  /**
   * Read recorded app health changes.
   * Requires apps:read or admin; no MFA required. Background collection
   * records default HTTP serving assessments independently of dashboard
   * reads, without waking or probing workloads. Baseline, meaningful
   * changes and expired-evidence gaps are newest first. Times describe
   * observations or evidence expiry, not exact incident start/end times.
   * Retains up to 100 entries within 4 MiB and 30 days per app; each entry
   * is bounded to 64 KiB. Missing, foreign, aged or pruned cursors return
   * 404. Latest retains its original time; collector_fresh is false when
   * unavailable or expired. Reads never create or refresh stored evidence.
   *
   * @returns AppHealthHistoryPage Recorded observations and collection freshness; Cache-Control no-store.
   * @returns Problem Invalid page parameters, access denial, missing cursor, or unavailable storage.
   * @throws ApiError
   */
  public static listAppHealthHistory({
    slug,
    limit = 20,
    before,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Maximum number of retained observations to return.
     */
    limit?: number,
    /**
     * ID of the last retained entry from the preceding page.
     */
    before?: string,
  }): CancelablePromise<AppHealthHistoryPage | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/health/history',
      path: {
        'slug': slug,
      },
      query: {
        'limit': limit,
        'before': before,
      },
    });
  }
  /**
   * Per-app request metrics (issue
   * Time-windowed rollup of one app's gateway activity. The `range`
   * parameter is a closed vocabulary bounded by Prometheus
   * retention (`prom_retention_days: 15`):
   *
   * `5m` (default) | `15m` | `1h` | `6h` | `24h` | `7d` | `15d`
   *
   * Wake latency (`wake_p95_ms`) is the FLEET p95
   * (`gateway_wake_latency_seconds` is unlabeled). On Prometheus
   * failure the endpoint returns 200 with zeroed fields and
   * `source: "degraded: <reason>"`, matching the public status
   * page contract.
   *
   * @returns AppMetricsResponse The metrics snapshot.
   * @throws ApiError
   */
  public static getAppMetrics({
    slug,
    range = '5m',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Time window. Default `5m`.
     */
    range?: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d',
  }): CancelablePromise<AppMetricsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/metrics',
      path: {
        'slug': slug,
      },
      query: {
        'range': range,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Plan does not unlock the per-app metrics surface. Free
        plan — Hobby or above required. The gate runs BEFORE
        \`loadApp\` so a Free customer probing a slug never gets a
        404 (slug-leak guard).
        `,
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
   * Per-app wake timeline (JSON mirror of the dashboard page).
   * Wire-friendly mirror of the dashboard's per-app wake-timeline
   * HTML page (`/dashboard/apps/{slug}/wake-timeline`,
   * cmd/apid/handlers_dashboard.go:2548 renderAppWakeTimeline).
   * The HTML page keeps its pre-rendered HTML chips; this endpoint
   * emits the same data as JSON so a separate frontend agent can
   * render without re-parsing HTML.
   *
   * Returns the 50 most-recent instance rows for the app, joined
   * against the events table's `wake.boot_started` rows for the
   * per-row telemetry (Trigger, QueuedCount, ConcurrencyAtAdmit,
   * AtCapacity, ReadyInMS). The aggregation math is shared with
   * the HTML page:
   *
   * - 24h cutoff descending-break: the moment a row's
   * `started_at` falls before the trailing-24h instant, the
   * loop breaks (no further iteration). Pre-ADR-123 fleet
   * rows with no `started_at` are not eligible for the break
   * (always in scope).
   * - Two-denominator rule for `at_capacity_pct`: numerator is
   * the count of rows where the events join succeeded AND
   * the at_capacity flag is true; denominator is the count
   * of rows where the events join succeeded
   * (`wake_count_with_meta`). Pre-PR-A fleet rows contribute
   * to `wake_count_24h` but not the denominator — same
   * posture as the HTML page.
   *
   * Plan-gated Hobby+ (mirror of /v1/apps/{slug}/metrics —
   * same `code` so a downgrade between the two endpoints flips
   * both at once).
   *
   * @returns AppWakeTimelineResponse The wake-timeline snapshot.
   * @throws ApiError
   */
  public static getAppWakeTimeline({
    slug,
    since,
    until,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Inclusive RFC3339 lower bound; defaults to 24 hours before until.
     */
    since?: string,
    /**
     * Inclusive RFC3339 upper bound; defaults to now.
     */
    until?: string,
  }): CancelablePromise<AppWakeTimelineResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/wake-timeline',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'until': until,
      },
      errors: {
        401: `code: unauthorized`,
        402: `Plan does not unlock the per-app wake-timeline. Free plan —
        Hobby or above required. Same code as /v1/apps/{slug}/metrics
        (plan_per_app_metrics_not_allowed) so a single downgrade
        flips both endpoints at once.
        `,
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
   * Per-app billing usage summary (trailing 30d by default).
   * Customer-facing billing rollup for one app over a caller-
   * supplied window (default: trailing 30d, clamped at 90d upper
   * bound). Plan-gated Hobby+ — Free gets 402
   * `plan_app_usage_summary_not_allowed`.
   *
   * Window resolution: `since` and `until` are RFC3339 timestamps.
   * Both default to UTC midnight snaps; `until` defaults to
   * `now()` snapped down, `since` defaults to `until - 30d`. The
   * handler clamps `since` to `until - 90d` so a customer cannot
   * unbounded-scan `usage_minutes` (ADR-048 retention is 30d; the
   * 90d ceiling is a forward-compatibility ceiling for when
   * `usage_daily` lands).
   *
   * Overage computation: `overage_gb_hours = max(0, gb_hours -
   * plan_included_gb_hours)`. The included band is echoed from
   * `acct.Plan.PlanIncludedGBHours()`; the overage figure is the
   * integer-rounded float the Stripe pusher bills at €0.01/GB-h.
   *
   * Source: `usage_minutes` today (after the 30d retention cap).
   * `usage_daily` / `mixed` land with the trail-period reader
   * follow-up — same wire shape, no migration needed.
   *
   * @returns AppUsageSummaryResponse The usage summary.
   * @throws ApiError
   */
  public static getAppUsage({
    slug,
    since,
    until,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * RFC3339 lower bound. Default: `until - 30d`.
     */
    since?: string,
    /**
     * RFC3339 upper bound. Default: `now()` snapped to UTC midnight.
     */
    until?: string,
  }): CancelablePromise<AppUsageSummaryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/usage',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'until': until,
      },
      errors: {
        400: `Invalid window — \`since\`/\`until\` not RFC3339, or \`since\`
        later than \`until\`.
        `,
        401: `code: unauthorized`,
        402: `Plan does not unlock the per-app usage summary. Free
        plan — Hobby or above required. Same posture as the
        other per-app observability surfaces; the gate runs
        BEFORE \`loadApp\` so a Free customer probing a slug
        never gets a 404 (slug-leak guard).
        `,
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
   * Aggregated historical request analytics.
   * Returns an aggregate request overview for one app: total requests,
   * errors, cold boots, weighted p50/p95/p99 latency, and the top
   * route/method combinations, or a bounded top-N grouping by country,
   * referrer host, client family, status, or stable API consumer identity. This is the customer analytics surface;
   * request identifiers and trace payloads remain on the debugger routes.
   *
   * `since` accepts a duration such as `24h` or `7d` and defaults to
   * `24h`. The effective window is clamped to the plan's
   * `DebugTelemetryRetentionDays` (Hobby 3d, Pro 7d, Scale 14d).
   * `window_clamped` tells callers when the requested lookback was wider
   * than the retained telemetry. The response contains at most 50 route
   * rows; `routes_truncated` indicates that more routes matched.
   *
   * Counts and percentiles include the recorder's collapsed row `count`,
   * so the result represents original requests rather than stored rows.
   * Route groups also include cold-request p95 and platform-runner guest
   * execution wall-time percentiles when available, p95 time from
   * `wake.boot_started` to `wake.boot_completed` for correlated route wakes,
   * plus bounded sampled dependency span timings for platform-classified dependencies. The
   * dependency values are not complete call counts; `dependencies_truncated`
   * marks row or cardinality caps. CPU time and route memory peaks are not
   * inferred from these fields.
   * Grouped results contain at most 50 groups plus `__other__`. Consumer
   * grouping uses the stable consumer UUID and reports anonymous traffic
   * as `__anonymous__`. Only a normalized User-Agent family, hostname-only
   * referrer, and country code are stored; no IP, cookie, script, raw
   * User-Agent, or full URL is used.
   * The endpoint is read-only, IDOR-safe, and plan-gated by
   * `DebugTelemetryEnabled`.
   *
   * @returns RequestAnalyticsResponse Aggregated request analytics.
   * @throws ApiError
   */
  public static getAppRequestAnalytics({
    slug,
    since = '24h',
    until,
    groupBy = 'route',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Lookback duration (`24h`, `3d`, `7d`) or RFC3339 start timestamp. Defaults to `24h` and is retention-clamped.
     */
    since?: string,
    /**
     * Optional RFC3339 upper-bound timestamp for the historical window.
     */
    until?: string,
    /**
     * Bounded top-N grouping dimension. Defaults to route.
     */
    groupBy?: 'route' | 'country' | 'referrer_host' | 'ua_family' | 'status' | 'consumer_id',
  }): CancelablePromise<RequestAnalyticsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/analytics',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'until': until,
        'group_by': groupBy,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Observed customer usage by route and deployment.
   * Returns retained route usage for one immutable deployment owned by the
   * app. Consumer and platform-tenant identities come from request-time
   * telemetry; current consumer-to-tenant links are never used to infer
   * historical attribution. Identity ownership is checked against the app
   * and account. Revoked customers remain part of historical observations.
   *
   * Counts include collapsed row weights. The top 200 route/method rows
   * each contain at most 20 identity groups, ordered by observed requests.
   * Distinct consumer and tenant counts precede these caps and overlap;
   * do not add them together. Omitted customer requests and truncation flags
   * remain explicit. Anonymous and unresolved identities are separate.
   * Observation timestamps may represent minute buckets.
   *
   * Coverage is always observed_only: disabled recording, sampling, dropped
   * events and expired telemetry prevent proof of complete customer exposure
   * or that an unobserved route is unused. Route usage does not establish
   * which clients will break. No customer names, external references,
   * credentials, payloads, query strings or request identifiers are returned.
   * This read uses the normal read scopes and DebugTelemetryEnabled plan gate.
   *
   * @returns RouteCustomerUsageResponse Observed route customer exposure for the selected deployment.
   * @throws ApiError
   */
  public static getAppRouteCustomerUsage({
    slug,
    deploymentId,
    since = '24h',
    until,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Immutable deployment UUID owned by this app; missing or foreign deployments return 404.
     */
    deploymentId: string,
    /**
     * Positive lookback duration or RFC3339 start timestamp; clamped to current plan retention.
     */
    since?: string,
    /**
     * Exclusive upper bound, default now; must be within current retained telemetry and not in the future.
     */
    until?: string,
  }): CancelablePromise<RouteCustomerUsageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/analytics/route-customers',
      path: {
        'slug': slug,
      },
      query: {
        'deployment_id': deploymentId,
        'since': since,
        'until': until,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Request analytics time series by hour.
   * Returns zero-filled UTC hourly buckets for customer request analytics.
   * Each bucket contains request and error counts, error rate, cold boots,
   * and weighted p50/p95/p99 latency. The window is half-open [since, until)
   * and is clamped to the plan's DebugTelemetryRetentionDays.
   *
   * `since` accepts a duration such as `24h` or `7d`, or an RFC3339 start
   * timestamp. `until` is an optional RFC3339 exclusive upper bound and
   * defaults to now. The endpoint is read-only, IDOR-safe, and plan-gated
   * by `DebugTelemetryEnabled`.
   * Set `group_by` to country, referrer_host, ua_family, status, or consumer_id to
   * receive zero-filled series for the top 50 groups plus `__other__`.
   *
   * @returns RequestAnalyticsTimeseriesResponse Zero-filled hourly request analytics buckets.
   * @throws ApiError
   */
  public static getAppRequestAnalyticsTimeseries({
    slug,
    since = '24h',
    until,
    route,
    method,
    groupBy,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Lookback duration or RFC3339 start timestamp. Defaults to `24h`.
     */
    since?: string,
    /**
     * Exclusive end timestamp; omitted means current server time.
     */
    until?: string,
    /**
     * Exact bounded route label to drill into (for example `GET /users/{id}`). Must be provided together with `method`; omitted means all routes.
     */
    route?: string,
    /**
     * Exact HTTP method for the selected route. Must be provided together with `route`; omitted means all methods.
     */
    method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS',
    /**
     * Return grouped series for the selected dimension. Omitted preserves the app-wide points shape; route/method filters require group_by=route or omission.
     */
    groupBy?: 'route' | 'country' | 'referrer_host' | 'ua_family' | 'status' | 'consumer_id',
  }): CancelablePromise<RequestAnalyticsTimeseriesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/analytics/timeseries',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'until': until,
        'route': route,
        'method': method,
        'group_by': groupBy,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Per-route breakdown for opt-in apps (ADR-093).
   * Returns the `routes` array of the per-app metrics surface.
   * Production reads the fleet Prometheus aggregate; single-box
   * development may use the gatewayd-internal loopback listener.
   * The array is empty when `route_metrics_enabled` is false
   * on the app (the gatewayd handler returns 200 + empty
   * rows rather than 404 — the customer-facing "feature off"
   * state is not a 404). Labels use declared templates when available,
   * otherwise common numeric/UUID/long-hex segments become `{id}`.
   * Unrecognized slug segments remain literal, and `__route_other__`
   * marks the per-app metrics cardinality overflow.
   *
   * @returns AppRoutesResponse The per-route rows for the app.
   * @throws ApiError
   */
  public static getAppRoutes({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppRoutesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/routes',
      path: {
        'slug': slug,
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
   * List exact gateway-observed request audit records (opt-in)
   * Requires an MFA session or an authorized API key. Records are one per
   * completed request, not collapsed debugger buckets. Only gateway-verified
   * consumer and platform-tenant IDs are included. Application user,
   * business action and internal/outbound dependency calls are not inferred.
   * The trusted public-gateway source IP is included when available.
   * The default window is 24 hours; at most 31 days may be
   * queried at once. Undeclared route candidates can contain literal path
   * segments; enable collection only after reviewing this privacy tradeoff.
   * Exact records are removed after 30 days. The bounded list has no
   * cursor export yet and is not a compliance/WORM archive.
   *
   * @returns RequestAuditListResponse Bounded exact request evidence, newest first.
   * @throws ApiError
   */
  public static getAppRequestAudit({
    slug,
    since,
    until,
    limit = 100,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Inclusive UTC start of the audit window.
     */
    since?: string,
    /**
     * Exclusive UTC end of the audit window.
     */
    until?: string,
    /**
     * Maximum newest records to return.
     */
    limit?: number,
  }): CancelablePromise<RequestAuditListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/audit/requests',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'until': until,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Persisted, bounded API route inventory
   * Returns up to 500 distinct observed method/template candidates with
   * first/last seen times and replay-safe request counts. Discovery has
   * its own operator opt-in, independent of exact request audit and its
   * 30-day retention. Undeclared paths can retain literal segments, so
   * operators must review path privacy before enabling discovery.
   *
   * @returns DiscoveredRoutesResponse Persisted discovered route candidates.
   * @throws ApiError
   */
  public static getAppDiscoveredRoutes({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<DiscoveredRoutesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/discovered-routes',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Per-app streaming classification probe (ADR-102 D6).
   * Returns the streaming-status enum (one of `streaming`,
   * `accept-json-downgrade`, `flag-disabled`, `plan-disallows`,
   * `operator-disabled`, `upgrade-bypass`) the gatewayd handler
   * would stamp on the `Streaming-Status` response header for a
   * representative request to this app, plus the effective
   * response-body cap (in bytes) and the per-gate flags.
   *
   * With no query parameters the probe is a pure read against the
   * apid cache (the per-account `Plan` and the per-app
   * `streaming_enabled` flag). Supplying `host`, `path`, and `method`
   * together performs a bounded loopback read of gatewayd's compiled
   * kind=limit rules and reports a matching streaming response cap with
   * `cap_kind="endpoint-rule"`. If gatewayd is unavailable or no rule
   * matches, the response falls back to the plan cap.
   *
   * The operator opt-in (`FAAS_GATEWAY_STREAMING` env) remains
   * gatewayd-side state, so the canonical signal is the
   * `Streaming-Status` response header on a real request, not this probe.
   * A customer evaluating "will my next request stream?" must
   * consider the operator-side flag separately.
   *
   * `status=plan-disallows` means the customer's plan tier
   * forbids `streaming_enabled=true`; the CreateApp gate (D5)
   * already returns 403 `CodePlanStreamingNotAllowed` so this
   * row should be unreachable from a properly-validated app,
   * but the probe still reflects the persisted state for
   * audits and pinned-SDK migrations.
   *
   * @returns AppStreamingStatus The streaming classification for the app.
   * @throws ApiError
   */
  public static getAppStreamingCap({
    slug,
    host,
    path,
    method,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Request host to resolve a per-edge-rule cap. Supply together
     * with `path` and `method`; omit all three for the plan-level cap.
     *
     */
    host?: string,
    /**
     * Request path to resolve against kind=limit rules.
     */
    path?: string,
    /**
     * HTTP method to resolve against kind=limit rules.
     */
    method?: string,
  }): CancelablePromise<AppStreamingStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/streaming-cap',
      path: {
        'slug': slug,
      },
      query: {
        'host': host,
        'path': path,
        'method': method,
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
   * Per-app customer-facing SLO panel (issue
   * Closed-set windowed SLO panel for one app — the
   * customer-facing equivalent of AWS CloudWatch
   * per-function / GCP Cloud Run per-service. Distinct from
   * `GET /v1/apps/{slug}/metrics` (issue #273 / ADR-042) which
   * is the 5m-window dashboard panel. The /slo surface is the
   * "yesterday's SLO" / "this week's SLO" summary, with the
   * customer-facing SLO signals co-located with the
   * billing-derivable `instance_hours` / `gb_hours` fields.
   *
   * The `window` parameter is a closed vocabulary, a strict
   * subset of the /metrics range vocabulary:
   *
   * `1h` | `24h` (default) | `7d`
   *
   * `wake_queue_p95_ms` is null and `wake_queue_sample_status` is
   * `unavailable` while the wake-queue histogram lacks tenant labels. On
   * Prometheus failure the endpoint returns 200 with zeroed
   * fields and `source: "degraded: <reason>"`, matching the
   * public status page contract. When Postgres is down but
   * the PromQL pass succeeded, only `instance_hours` /
   * `gb_hours` are zeroed and `source` is
   * `"degraded: postgres unavailable"`.
   *
   * This is a Hobby+ per-app observability surface. Free
   * accounts receive 402 before the app slug is resolved.
   *
   * @returns AppSLOResponse The SLO panel.
   * @throws ApiError
   */
  public static getAppSlo({
    slug,
    window = '24h',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Window for the per-app SLO panel. Default `24h`.
     */
    window?: '1h' | '24h' | '7d',
  }): CancelablePromise<AppSLOResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/slo',
      path: {
        'slug': slug,
      },
      query: {
        'window': window,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_per_app_metrics_not_allowed — the account plan does not include per-app metrics or wake narratives; upgrade to Hobby or above.`,
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
   * Ingest OTLP JSON exception events
   * Accepts bounded OTLP trace exception events or exception log records using a deployment-bound issue token. Unrelated telemetry is ignored. Deterministic event IDs permit safe retry after partial delivery. Protobuf encoding is not supported.
   * @returns any Exceptions accepted, including exact retries
   * @throws ApiError
   */
  public static ingestIssueOtlp({
    slug,
    signal,
    requestBody,
  }: {
    /**
     * Application slug owning the issue reporting scope.
     */
    slug: string,
    /**
     * OTLP JSON signal, either traces or logs.
     */
    signal: 'traces' | 'logs',
    requestBody: Record<string, any>,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/issue-events/otlp/{signal}',
      path: {
        'slug': slug,
        'signal': signal,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Capture an exception using a deployment-bound issue ingest token.
   * @returns IssueEventResponse Occurrence accepted or exact retry acknowledged.
   * @throws ApiError
   */
  public static ingestIssueEvent({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: IssueEvent,
  }): CancelablePromise<IssueEventResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/issue-events',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        409: `code: conflict`,
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
   * Read the app's customer-impact alert policy.
   * Returns the threshold for verified distinct customers in a rolling 24-hour window. A zero threshold disables the policy.
   * @returns IssueImpactAlertPolicy The current policy; disabled apps report minimum_customers as zero.
   * @throws ApiError
   */
  public static getIssueImpactAlertPolicy({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<IssueImpactAlertPolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/issue-impact-alert-policy',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Configure or disable customer-impact alerts for an app.
   * Setting minimum_customers to zero disables the policy. Policy changes apply prospectively; they do not backfill alerts for issues already above the threshold.
   * @returns IssueImpactAlertPolicy The updated policy.
   * @throws ApiError
   */
  public static setIssueImpactAlertPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: UpdateIssueImpactAlertPolicyRequest,
  }): CancelablePromise<IssueImpactAlertPolicy> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/issue-impact-alert-policy',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Read the app's automatic issue ownership rules.
   * Rules are ordered and only apply when Gregale creates a new issue group. Every populated matcher on a rule must match; route prefixes respect path-segment boundaries.
   * @returns IssueOwnershipRules The current ordered policy; apps without rules return an empty list.
   * @throws ApiError
   */
  public static getIssueOwnershipRules({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<IssueOwnershipRules> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/issue-ownership-rules',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Replace the app's automatic issue ownership rules.
   * Replaces the complete ordered policy and applies prospectively to new issue groups. Existing assignments are unchanged, and later manual assignments remain authoritative. Each target must be the app owner or an active organization member.
   * @returns IssueOwnershipRules The updated ordered policy.
   * @throws ApiError
   */
  public static setIssueOwnershipRules({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: IssueOwnershipRules,
  }): CancelablePromise<IssueOwnershipRules> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/issue-ownership-rules',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * List durable grouped application issues.
   * @returns ListIssuesResponse A bounded page of issues for this application.
   * @throws ApiError
   */
  public static listIssues({
    slug,
    state,
    environment,
    assignee,
    sort = 'recent',
    minCustomers,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Filter by open, resolved, or ignored lifecycle state.
     */
    state?: string,
    /**
     * Exact issue environment namespace.
     */
    environment?: string,
    /**
     * Filter by me, unassigned, or an owner account UUID.
     */
    assignee?: string,
    /**
     * Issue ordering; impact ranks by verified distinct customers in the fixed 24-hour window.
     */
    sort?: 'recent' | 'impact',
    /**
     * Return issues affecting at least this many verified distinct customers in the fixed 24-hour window.
     */
    minCustomers?: number,
    /**
     * Opaque next_cursor from the previous issue page.
     */
    cursor?: string,
  }): CancelablePromise<ListIssuesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/issues',
      path: {
        'slug': slug,
      },
      query: {
        'state': state,
        'environment': environment,
        'assignee': assignee,
        'sort': sort,
        'min_customers': minCustomers,
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        409: `code: conflict`,
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
   * Read sanitized evidence, release history, activity, and observed customer impact.
   * @returns IssueDetail Issue evidence, verified impact, and paginated history.
   * @throws ApiError
   */
  public static getIssue({
    slug,
    issueId,
    since,
    eventCursor,
    releaseCursor,
    activityCursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of the durable issue within this app.
     */
    issueId: string,
    /**
     * RFC3339 impact-window start, clamped to plan retention.
     */
    since?: string,
    /**
     * Opaque next_event_cursor for earlier occurrences.
     */
    eventCursor?: string,
    /**
     * Opaque next_release_cursor for earlier deployment history.
     */
    releaseCursor?: string,
    /**
     * Opaque next_activity_cursor for earlier actions.
     */
    activityCursor?: string,
  }): CancelablePromise<IssueDetail> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/issues/{issue_id}',
      path: {
        'slug': slug,
        'issue_id': issueId,
      },
      query: {
        'since': since,
        'event_cursor': eventCursor,
        'release_cursor': releaseCursor,
        'activity_cursor': activityCursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        409: `code: conflict`,
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
   * Assign, resolve in a deployment, reopen, or ignore an issue.
   * @returns Issue Issue state or ownership updated with audited activity.
   * @throws ApiError
   */
  public static actOnIssue({
    slug,
    issueId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of the issue whose ownership or lifecycle should change.
     */
    issueId: string,
    requestBody: IssueActionRequest,
  }): CancelablePromise<Issue> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/issues/{issue_id}/actions',
      path: {
        'slug': slug,
        'issue_id': issueId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        409: `code: conflict`,
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
   * Create a reporting-only credential bound to one app and deployment.
   * @returns IssueIngestToken Reporting credential created; the secret is shown once.
   * @throws ApiError
   */
  public static createIssueIngestToken({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateIssueIngestTokenRequest,
  }): CancelablePromise<IssueIngestToken> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/issue-ingest-tokens',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        409: `code: conflict`,
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
   * List issue ingest credential metadata.
   * @returns ListIssueIngestTokensResponse Reporting credential metadata without bearer secrets.
   * @throws ApiError
   */
  public static listIssueIngestTokens({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListIssueIngestTokensResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/issue-ingest-tokens',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        409: `code: conflict`,
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
   * Revoke an issue ingest credential.
   * @returns void
   * @throws ApiError
   */
  public static revokeIssueIngestToken({
    slug,
    tokenId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of the reporting credential to revoke.
     */
    tokenId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/issue-ingest-tokens/{token_id}',
      path: {
        'slug': slug,
        'token_id': tokenId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        409: `code: conflict`,
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
   * Per-app customer-facing automatic error grouping summary (ADR-096 / PR-B).
   * Sentry-style grouped error view scoped to a customer's
   * app. One row per `(account_id, app_id, fingerprint)` over
   * the requested `[since, until]` window, sorted by `count
   * DESC, last_seen_at DESC, fingerprint ASC`. Distinct from
   * `GET /v1/apps/{slug}/slo` (issue #696 / ADR-082) which is
   * the closed-set SLO summary (`1h` / `24h` / `7d`) — the
   * errors summary uses a continuous `[since, until]` window
   * with an explicit RFC3339Nano stamp instead.
   *
   * The window is clamped to `AppErrorsWindowMaxHours` (168h).
   * When the clamp fires, `window_clamped` is true so the
   * dashboard can render a "you widened the window past the
   * cap" tile. The endpoint returns 200 with `items: []`
   * when no fingerprints are present in the window — never
   * 404. Cross-account slug is a 404 (IDOR-safe; the error
   * is byte-identical to a real "no such app" 404).
   *
   * Fingerprints are derived at write time as
   * `sha256(route_template || "\x1f" || http_status ||
   * "\x1f" || error_class)`. The route is the matched
   * template (e.g. `/users/{id}`), NEVER the expanded URL —
   * this is the load-bearing cardinality fix that keeps the
   * top-N bounded.
   *
   * @returns AppErrorsSummaryResponse The grouped error summary.
   * @throws ApiError
   */
  public static getAppErrorsSummary({
    slug,
    since,
    until,
    cursor,
    limit = 20,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * RFC3339Nano UTC window start. Defaults to `until - 24h`.
     */
    since?: string | null,
    /**
     * RFC3339Nano UTC window end. Defaults to `now()`.
     */
    until?: string | null,
    /**
     * Opaque pagination cursor from the previous response's `next_cursor`. Empty for the first page.
     */
    cursor?: string | null,
    /**
     * Page size. Default `AppErrorsSummaryDefaultLimit=20`, capped at `AppErrorsSummaryMaxLimit=100`.
     */
    limit?: number,
  }): CancelablePromise<AppErrorsSummaryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/errors/summary',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'until': until,
        'cursor': cursor,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Plan does not unlock the per-app error surfacing. Free
        plan — Hobby or above required. The gate runs BEFORE
        \`loadApp\` so a Free customer probing a slug never gets
        a 404 (slug-leak guard).
        `,
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
   * Per-fingerprint drill-down rows (ADR-096 / PR-B).
   * Cursor-paginated drill-down over the request rows that
   * landed on this fingerprint. Returns 404 when the
   * fingerprint has been purged by the retention cron or
   * never existed; the cross-account slug case is also 404
   * (IDOR-safe byte-identical to a real "no such app" 404).
   *
   * @returns AppErrorRequestsResponse The drill-down rows (newest-first).
   * @throws ApiError
   */
  public static listAppErrorRequests({
    slug,
    fingerprint,
    cursor,
    limit = 20,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 64-hex-char SHA-256 fingerprint of the error group; sha256(route_template || 0x1f || status || 0x1f || error_class).
     */
    fingerprint: string,
    /**
     * Opaque pagination cursor (received_at, request_id compound). Empty for the first page.
     */
    cursor?: string | null,
    /**
     * Page size. Default `AppErrorsSummaryDefaultLimit=20`.
     */
    limit?: number,
  }): CancelablePromise<AppErrorRequestsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/errors/{fingerprint}',
      path: {
        'slug': slug,
        'fingerprint': fingerprint,
      },
      query: {
        'cursor': cursor,
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
  /**
   * Single oldest sample row + redacted headers (ADR-096 / PR-B).
   * Returns the OLDEST request row for the fingerprint plus
   * the redacted `headers_sample` (jsonb-decoded) and the
   * list of `redactions_applied` pattern names so the
   * dashboard can render a "we redacted X / Y / Z" badge.
   * Returns 404 when the fingerprint has been purged.
   *
   * @returns AppErrorSampleResponse The sample row.
   * @throws ApiError
   */
  public static getAppErrorSample({
    slug,
    fingerprint,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 64-hex-char SHA-256 fingerprint to inspect; the oldest request row for this group is returned with its redacted headers.
     */
    fingerprint: string,
  }): CancelablePromise<AppErrorSampleResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/errors/{fingerprint}/first',
      path: {
        'slug': slug,
        'fingerprint': fingerprint,
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
   * Per-app request telemetry (ADR-127 / PR-A).
   * Recent request telemetry rows for an app — status, latency_ms, route,
   * method, deployment_id, cold_boot, trace_id, received_at, and the
   * number of original requests represented by each collapsed row. Rows
   * are split by bounded latency bucket so aggregate percentiles retain
   * distribution signal; `latency_ms` is that bucket's inclusive upper
   * bound (and can therefore be slightly conservative).
   * PR-A ships the read endpoint only; the write-side (publisher
   * → gRPC IncrementRequestTelemetry → apid receiver → sqlc
   * INSERT) lands in PR-B. The endpoint is plan-gated by
   * `DebugTelemetryEnabled` (Free off; Hobby/Pro/Scale on).
   * The window is clamped to `DebugTelemetryRetentionDays`
   * (Hobby 3d, Pro 7d, Scale 14d). When the clamp fires, the
   * effective `since` is returned in the response so the
   * dashboard can render a "you widened past the cap" tile. Results are
   * cursor-paginated in `(received_at DESC, id DESC)` order. The opaque
   * cursor pins the effective window, route, and every supplied filter, so callers can safely
   * walk pages while new telemetry arrives. `complete` is true only when
   * every retained row in that bounded window is present in the page.
   * Filters are applied before pagination and echoed in `filters` so an
   * incident link can be reproduced exactly.
   * Returns 200 with `requests: []` when no rows exist in the
   * window — never 404. Cross-account slug is 404 (IDOR-safe;
   * byte-identical to "no such app").
   *
   * @returns DebugTelemetryListResponse Page of recent request telemetry rows.
   * @throws ApiError
   */
  public static listAppDebugRequests({
    slug,
    since,
    limit,
    route,
    deploymentId,
    status,
    coldBoot,
    consumerId,
    minLatencyMs,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Duration or 'Nd' alias. Defaults to `24h`. Clamped to plan's `DebugTelemetryRetentionDays`.
     */
    since?: string | null,
    /**
     * Page size, default 20, max 200.
     */
    limit?: number | null,
    /**
     * Exact route-template filter.
     */
    route?: string | null,
    /**
     * Exact deployment UUID filter.
     */
    deploymentId?: string | null,
    /**
     * Exact HTTP status filter.
     */
    status?: number | null,
    /**
     * Filter cold-start requests when true, or warm requests when false.
     */
    coldBoot?: boolean | null,
    /**
     * Consumer UUID, or __anonymous__ for requests without a stable consumer identity.
     */
    consumerId?: string | null,
    /**
     * Minimum inclusive latency bucket bound in milliseconds.
     */
    minLatencyMs?: number | null,
    /**
     * Opaque next_cursor from a previous response. The cursor must be reused with the same app and filters.
     */
    cursor?: string | null,
  }): CancelablePromise<DebugTelemetryListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/debug/requests',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'limit': limit,
        'route': route,
        'deployment_id': deploymentId,
        'status': status,
        'cold_boot': coldBoot,
        'consumer_id': consumerId,
        'min_latency_ms': minLatencyMs,
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Export retained request telemetry.
   * Downloads a bounded, metadata-only request-log artifact for the app.
   * The default format is newline-delimited JSON (`ndjson`); `csv` is
   * available for spreadsheet and support workflows. The export uses the
   * same plan retention boundary as the debugger list, never includes
   * request bodies, headers, source IPs, or raw span attributes, and is
   * capped at 10,000 rows. `X-Faas-Request-Log-Window` and
   * `X-Faas-Request-Log-Retention-Clamped` describe the effective window
   * applied to the artifact.
   *
   * @returns binary Metadata-only request telemetry export.
   * @throws ApiError
   */
  public static exportAppDebugRequests({
    slug,
    since,
    route,
    format = 'ndjson',
    limit,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Lookback duration (for example `24h` or `3d`). Defaults to `24h` and is clamped to plan retention.
     */
    since?: string | null,
    /**
     * Filter the exported rows by exact route template.
     */
    route?: string | null,
    /**
     * Export encoding.
     */
    format?: 'ndjson' | 'csv',
    /**
     * Maximum number of retained rows to export.
     */
    limit?: number | null,
  }): CancelablePromise<Blob> {
    return __request(OpenAPI, {
      responseType: 'blob',
      method: 'GET',
      url: '/v1/apps/{slug}/debug/requests/export',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'route': route,
        'format': format,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Read the automatic CPU-check policy.
   * Returns an owned app's deployment-check configuration with app-read access. Before the first save the policy is disabled at revision zero. Metadata remains readable without profiling entitlement or backend access.
   * @returns ProfileDeploymentPolicy Automatic check configuration and revision, including initial disabled defaults.
   * @throws ApiError
   */
  public static getProfileDeploymentPolicy({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ProfileDeploymentPolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/profiles/deployment-policy',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Configure CPU checks for future successful rollouts.
   * Requires deploy-write access and the current policy revision; use zero for the initial save. Enabling requires a profiling plan and configured backend. Saving advances the revision and cancels pending checks; new completions use the new policy. The worker compares the previous successful deployment in the same environment and uses the selected runtime on both sides. Equal windows end at candidate creation for the baseline and start after rollout completion plus warm-up for the candidate. Retries preserve these windows, allow up to five attempts for late or insufficient data and remain inconclusive when data is absent. Deployment activation is independent of this background check.
   * @returns ProfileDeploymentPolicy Saved deployment CPU-check policy with a new revision.
   * @throws ApiError
   */
  public static saveProfileDeploymentPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Complete automatic profiling policy with revision protection.
     */
    requestBody: SaveProfileDeploymentPolicyRequest,
  }): CancelablePromise<ProfileDeploymentPolicy> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/profiles/deployment-policy',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Inspect periodic route checks and their recent incident history.
   * App-read access and completed MFA are required. Returns up to 50 newest monitors with up to ten terminal observations each, including pinned baselines, confirmation transitions and comparison links. Samples remain in backend retention. Checks are advisory and never change rollout decisions.
   * @returns ListProfilePeriodicMonitorsResponse Bounded periodic monitor history.
   * @throws ApiError
   */
  public static listProfilePeriodicMonitors({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListProfilePeriodicMonitorsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/profiles/periodic-monitors',
      path: {
        'slug': slug,
      },
      errors: {
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
   * List recent automatic CPU-check receipts.
   * Returns up to fifty receipts for the owned app with app-read access, newest first. Receipts keep the snapshotted policy, exact windows, attempt state and authenticated comparison link. Metadata is retained for thirty days; saved investigations retain their existing lifetime and fifty-record quota. This read does not query CPU samples.
   * @returns ListProfileDeploymentChecksResponse Recent deployment profiling receipts with authenticated drill-down links.
   * @throws ApiError
   */
  public static listProfileDeploymentChecks({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListProfileDeploymentChecksResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/profiles/deployment-checks',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Read the CPU-check receipt for a deployment.
   * Reads an owned app's receipt by deployment UUID with app-read access. Missing or foreign receipts return not found. A saved-investigation link restores the differential flamegraph; if the investigation quota was full or the saved record was deleted, the link keeps the original comparison selections instead. Missing baseline comparisons have no link.
   * @returns ProfileDeploymentCheck Single deployment CPU-check receipt and comparison selection.
   * @throws ApiError
   */
  public static getProfileDeploymentCheck({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment UUID for an automatic profiling receipt.
     */
    id: string,
  }): CancelablePromise<ProfileDeploymentCheck> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/profiles/deployment-checks/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * List retained CPU assessments for a deployment's canary stages.
   * Requires app-read access and completed MFA. Returns bounded, newest-first stage assessments retained for 30 days, including exact fixed profile windows, policy revision, threshold evidence and retry state. Use next_cursor as before to read older stages. The read never queries profile samples.
   * @returns ProfileCanaryHistoryPage Newest-first canary stage history page.
   * @throws ApiError
   */
  public static listProfileCanaryChecks({
    slug,
    deployment,
    limit = 5,
    before,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Canary deployment UUID.
     */
    deployment: string,
    /**
     * Maximum number of canary stage assessments to return.
     */
    limit?: number,
    /**
     * Opaque cursor from the prior page's next_cursor.
     */
    before?: string,
  }): CancelablePromise<ProfileCanaryHistoryPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/profiles/canary-checks/{deployment}',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      query: {
        'limit': limit,
        'before': before,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * List saved profiling investigations.
   * Lists up to 50 investigations for an owned app, ordered by last update and UUID. Requires existing app-read access. Metadata remains readable after profile expiry, plan downgrades or backend outages; this operation does not query CPU samples.
   * @returns ListProfileInvestigationsResponse Bounded saved-investigation list with per-window eligibility.
   * @throws ApiError
   */
  public static listProfileInvestigations({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListProfileInvestigationsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/profiles/investigations',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Save profiling comparison selections and notes.
   * Creates comparison metadata for retained deployments belonging to the owned app. Requires profiling entitlement and deploy-write access. Use expected_revision 0. Stores notes and selections without querying or retaining profile samples; sharing requires authenticated app access.
   * @returns ProfileInvestigationResponse Created metadata and authenticated dashboard link.
   * @throws ApiError
   */
  public static createProfileInvestigation({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * New investigation with expected_revision 0 and complete comparison selections.
     */
    requestBody: SaveProfileInvestigationRequest,
  }): CancelablePromise<ProfileInvestigationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/profiles/investigations',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Read a saved investigation and window retention status.
   * Reads saved metadata by its app-owned UUID with baseline and candidate eligibility under the current plan. Requires app-read access. Returned links require authentication and app ownership. A retained status establishes query eligibility, not sample presence.
   * @returns ProfileInvestigationResponse Saved selections and notes with current retention eligibility.
   * @throws ApiError
   */
  public static getProfileInvestigation({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Saved investigation UUID belonging to this app.
     */
    id: string,
  }): CancelablePromise<ProfileInvestigationResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/profiles/investigations/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Update a saved profiling investigation using its current revision.
   * Replaces metadata using the current revision and deploy-write access. Unchanged expired selections permit commentary edits; changed selections recheck profiling entitlement, retention and deployment ownership. Concurrent edits return 409. Saving does not extend profile retention.
   * @returns ProfileInvestigationResponse Updated metadata with a new saved revision.
   * @throws ApiError
   */
  public static updateProfileInvestigation({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Saved investigation UUID belonging to this app.
     */
    id: string,
    /**
     * Complete metadata replacement with the currently observed saved revision.
     */
    requestBody: SaveProfileInvestigationRequest,
  }): CancelablePromise<ProfileInvestigationResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/profiles/investigations/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Delete a saved profiling investigation.
   * Deletes metadata for an owned app using the current revision and deploy-write access. A stale revision returns 409, preserving concurrent edits. Deletion frees one saved-investigation slot and leaves backend profile retention unchanged.
   * @returns void
   * @throws ApiError
   */
  public static deleteProfileInvestigation({
    slug,
    id,
    expectedRevision,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Saved investigation UUID belonging to this app.
     */
    id: string,
    /**
     * Current saved revision to prevent deleting concurrent edits.
     */
    expectedRevision: number,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/profiles/investigations/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'expected_revision': expectedRevision,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Check a saved comparison for CPU regressions.
   * Checks the exact saved windows with deploy-write access. Both relative and absolute CPU-rate thresholds must be met for a total, comparable function or complete call path. Missing samples, insufficient capture coverage, recorded failures or unavailable history produce a stored inconclusive assessment. This heuristic does not establish deployment causality or statistical confidence. Returns a new revision; concurrent edits or checks return 409. Later edits make the preserved assessment stale when its investigation_revision differs from the saved revision. Existing profiling query limits and retention apply; raw profiles are never stored.
   * @returns ProfileInvestigationResponse Saved regression assessment with incremented revision and authenticated comparison link.
   * @throws ApiError
   */
  public static checkProfileRegression({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * App-owned investigation UUID to assess.
     */
    id: string,
    /**
     * Current saved revision and optional complete threshold configuration; omit options for defaults.
     */
    requestBody: CheckProfileRegressionRequest,
  }): CancelablePromise<ProfileInvestigationResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/profiles/investigations/{id}/check',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Query sampled application CPU by deployment.
   * Internal opt-in profiling. Returns function CPU totals and call paths; missing samples are explicitly empty. Times must fall within the plan retention window.
   * @returns ProfileResponse Sampled CPU profile and source symbols.
   * @throws ApiError
   */
  public static getAppProfiles({
    slug,
    deploymentId,
    runtime,
    start,
    end,
    route,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment belonging to the selected app.
     */
    deploymentId: string,
    /**
     * Runtime name attached by the compute broker.
     */
    runtime: string,
    /**
     * Inclusive capture start.
     */
    start: string,
    /**
     * Capture end after start.
     */
    end: string,
    /**
     * Static METHOD /pattern or [unattributed]; omitted selects all CPU.
     */
    route?: string,
  }): CancelablePromise<ProfileResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/profiles',
      path: {
        'slug': slug,
      },
      query: {
        'deployment_id': deploymentId,
        'runtime': runtime,
        'start': start,
        'end': end,
        'route': route,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Compare deployment CPU profiles normalized by capture duration.
   * Reads authorized deployment windows from the private profiling backend. Each function delta uses self CPU seconds divided by selected window seconds. Traffic and allocation changes also affect the result.
   * @returns ProfileCompareResponse CPU rate deltas or an explicit unavailable comparison reason.
   * @throws ApiError
   */
  public static compareAppProfiles({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Baseline and candidate capture selections.
     */
    requestBody: ProfileCompareRequest,
  }): CancelablePromise<ProfileCompareResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/profiles/compare',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Observed debugger signal coverage (ADR-127 follow-up).
   * Returns bounded, weighted coverage for the debugger signals
   * attached to retained request telemetry in the requested window.
   * Counts are split between stored aggregate rows and the original
   * requests those rows represent. Rates are relative to represented
   * requests only; the platform does not infer a capture denominator for
   * requests dropped before persistence. Plan-gated by
   * `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.
   *
   * @returns DebugCoverageResponse Observed debugger signal coverage.
   * @throws ApiError
   */
  public static getAppDebugCoverage({
    slug,
    since,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Lookback duration (e.g. 30m, 24h, 3d). Defaults to 24h and is clamped by plan retention.
     */
    since?: string | null,
  }): CancelablePromise<DebugCoverageResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/debug/coverage',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Historical dependency latency and regressions.
   * Returns bounded dependency percentiles and error rates derived from
   * retained, redacted span summaries. The result compares the newer and
   * older halves of the selected window to flag a dependency regression,
   * and includes normalized parent-to-child impact edges with exclusive
   * wall-time percentiles. Each edge includes up to three representative
   * request identifiers for the current slowest sample, baseline slowest
   * sample, and an error sample when available; use them with the request
   * evidence endpoint for drill-down.
   * Raw span attributes, destinations, request bodies, and credentials
   * are never returned. Span evidence is sampled and the response marks
   * row/cardinality truncation explicitly. Plan-gated by
   * `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.
   *
   * @returns DebugDependencyLatencyResponse Historical dependency latency aggregates.
   * @throws ApiError
   */
  public static getAppDebugDependencyLatency({
    slug,
    since,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Optional dependency-history lookback; defaults to the recent 24-hour window and is bounded by plan retention.
     */
    since?: string | null,
  }): CancelablePromise<DebugDependencyLatencyResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/debug/dependencies',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Historical critical-path regressions.
   * Returns bounded canonical critical paths reconstructed from retained,
   * redacted span summaries. The result compares the newer and older
   * halves of the selected window to flag a path regression. Raw span
   * attributes, destinations, request bodies, and credentials are never
   * returned. Each path includes up to three representative request
   * identifiers (current slowest, baseline slowest, and an error sample
   * when available) plus the largest uncovered segment attribution. Span
   * evidence is sampled and the response marks row, path-cardinality, and
   * incomplete-parent coverage explicitly.
   * Plan-gated by `DebugTelemetryEnabled` and clamped to
   * `DebugTelemetryRetentionDays`.
   *
   * @returns DebugCriticalPathHistoryResponse Historical critical-path aggregates.
   * @throws ApiError
   */
  public static getAppDebugCriticalPaths({
    slug,
    since,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Optional critical-path lookback; defaults to the recent 24-hour window and is bounded by plan retention.
     */
    since?: string | null,
  }): CancelablePromise<DebugCriticalPathHistoryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/debug/critical-paths',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Explain why an app is still running.
   * Returns observed scheduler causes that prevented an application from
   * parking during the requested window. Causes are evidence-shaped: the
   * response reports request activity, open connections, tail tasks,
   * configured or temporary warm floors, cooldowns, and workload modes
   * when those signals were observed. It does not estimate a saving or
   * infer a protocol that was not instrumented. Plan-gated by
   * `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.
   *
   * @returns DebugRunningResponse Observed causes explaining why the app remained resident.
   * @throws ApiError
   */
  public static getAppDebugRunning({
    slug,
    since,
    limit,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Optional lookback window for scheduler observations (e.g. 30m, 24h, 3d); defaults to 24h and is clamped by plan retention.
     */
    since?: string | null,
    /**
     * Maximum number of recent observations to return; default 20, max 100.
     */
    limit?: number | null,
  }): CancelablePromise<DebugRunningResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/debug/running',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Get one request telemetry record (ADR-127).
   * Resolves an exact public x-faas-request-id through the durable,
   * app-scoped request-ID journal, then enriches it from detailed request
   * telemetry when that sampled row exists. If only the identity mapping
   * remains, the response sets `evidence_status` to `request_id_only` and
   * includes the W3C `trace_id` separately when available. Internal
   * telemetry row UUIDs remain accepted for compatibility. The lookup is
   * scoped to the app resolved from `slug`, so an ID belonging to another
   * app is returned as not found. This direct lookup is not limited to the
   * first page of recent requests. Plan-gated by `DebugTelemetryEnabled`.
   *
   * @returns DebugTelemetryRequestItem Request telemetry record.
   * @throws ApiError
   */
  public static getAppDebugRequest({
    slug,
    reqId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Public x-faas-request-id from the response, or the internal telemetry row UUID for compatibility.
     */
    reqId: string,
  }): CancelablePromise<DebugTelemetryRequestItem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/debug/requests/{req_id}',
      path: {
        'slug': slug,
        'req_id': reqId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Get request evidence and explanation (ADR-127).
   * Returns a deterministic request/wake timeline plus bounded,
   * redacted span evidence for one request and links it to a matching
   * active regression observation when one exists. Database statements are sanitized fingerprints; raw
   * attributes, status messages, request bodies, and headers are
   * never returned. The explanation is deterministic and suitable
   * as input to a future asynchronous synthesis layer. Plan-gated
   * by DebugTelemetryEnabled.
   *
   * @returns DebugRequestEvidenceResponse Request evidence and deterministic explanation.
   * @throws ApiError
   */
  public static getAppDebugRequestEvidence({
    slug,
    reqId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Public x-faas-request-id whose evidence should be retrieved; internal telemetry row UUIDs remain accepted for compatibility.
     */
    reqId: string,
  }): CancelablePromise<DebugRequestEvidenceResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/debug/requests/{req_id}/evidence',
      path: {
        'slug': slug,
        'req_id': reqId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: debug_regressions_unavailable — the debugger regression table or read query is unavailable; inspect migrations/readiness and retry.`,
      },
    });
  }
  /**
   * Active regression observations (ADR-127 / PR-B).
   * Returns regression observations written by the
   * debug_regression_observations table — surfaces per-route
   * p95 regressions detected by the regression cron
   * (cmd/apid/debug_regression_cron.go). Ordered by
   * regression_factor DESC, last_detected_at DESC (worst
   * first). Plan-gated by DebugTelemetryEnabled. The window
   * is clamped to DebugTelemetryRetentionDays.
   *
   * @returns DebugRegressionsResponse Page of active regression observations.
   * @throws ApiError
   */
  public static listAppDebugRegressions({
    slug,
    since,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Duration or 'Nd' alias. Defaults to `1h`.
     */
    since?: string | null,
  }): CancelablePromise<DebugRegressionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/debug/regressions',
      path: {
        'slug': slug,
      },
      query: {
        'since': since,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: debug_regressions_unavailable — the debugger regression table or read query is unavailable; inspect migrations/readiness and retry.`,
      },
    });
  }
  /**
   * Update regression triage state.
   * Acknowledge, temporarily dismiss, resolve, or reopen one
   * deployment/route regression observation. This changes debugger
   * workflow metadata only; it never changes deployment traffic.
   * `dismissed_until` is required only for dismiss and defaults to 24h
   * when omitted. The server caps dismissals at 30 days.
   *
   * @returns DebugRegressionActionResponse Updated regression observation.
   * @throws ApiError
   */
  public static updateAppDebugRegression({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: DebugRegressionActionRequest,
  }): CancelablePromise<DebugRegressionActionResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/debug/regressions',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: debug_regressions_unavailable — the debugger regression table or read query is unavailable; inspect migrations/readiness and retry.`,
      },
    });
  }
  /**
   * Per-route latency compare (ADR-127 / PR-B).
   * Compares two deployments' per-route latency
   * distributions in a shared time window. Body holds the
   * two deployment ids + optional route filter + optional
   * since/until bounds. Returns merged per-route stats with
   * per-deployment p50/p95/p99 + row counts. Plan-gated by
   * DebugTelemetryEnabled.
   *
   * @returns DebugCompareResponse Per-route compare stats.
   * @throws ApiError
   */
  public static compareAppDebugDeployments({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: DebugCompareRequest,
  }): CancelablePromise<DebugCompareResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/debug/compare',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
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
   * Replay a retained request through its mirror rule (ADR-127).
   * Reissues the retained request metadata through the enabled mirror
   * rule for the deployment that served it. Raw request bodies and
   * credentials are not retained, so the mirror receives an empty body
   * and platform-owned replay metadata only. The returned invocation ID
   * can be polled for the comparison result. Plan-gated by
   * DebugTelemetryEnabled; requires ScopesDeployWriteSurface.
   *
   * @returns DebugReplayResponse Replay invocation queued for mirror execution.
   * @throws ApiError
   */
  public static replayAppDebugRequest({
    slug,
    reqId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Public x-faas-request-id to replay; internal telemetry row UUIDs remain accepted for compatibility.
     */
    reqId: string,
    requestBody?: DebugReplayRequest,
  }): CancelablePromise<DebugReplayResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/debug/requests/{req_id}/replay',
      path: {
        'slug': slug,
        'req_id': reqId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        404: `code: not_found`,
        409: `No enabled mirror rule targets the deployment that served the
        retained request. Returns \`debug_replay_unsupported\`.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read the production route removal policy.
   * Requires apps:read or admin and completed MFA. An unconfigured app reports revision 0 and mode report.
   * @returns RouteRemovalPolicy Current policy and durable production baseline.
   * @throws ApiError
   */
  public static getRouteRemovalPolicy({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<RouteRemovalPolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-removal/policy',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Configure server route removal enforcement.
   * Requires account admin authorization and completed MFA. expected_revision is mandatory. First configuration requires one production baseline at 100 percent, or no production deployments. Policy changes invalidate previous approvals. Quiet observation begins when a policy first adopts a baseline, resets on full cutover or capture changes, and survives policy mode changes.
   * @returns RouteRemovalPolicy Saved route retirement policy.
   * @throws ApiError
   */
  public static setRouteRemovalPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: SetRouteRemovalPolicyRequest,
  }): CancelablePromise<RouteRemovalPolicy> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/route-removal/policy',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List production lifecycle review history
   * Requires read authorization and completed MFA. Returns newest retained reviews first by immutable review ID. Successful production traffic increases and blocked application review attempts are retained; rolled-back successful reviews and direct SQL rejections are not retained. Captures and graph IDs are historical metadata. Approval status is evaluated at read time and is not a fresh rollout authorization. Older reviews may have no recorded binding evidence. Metadata is bounded to 20 approvals, 64 captures and 64 graph IDs per approval; truncated indicates omitted metadata. Full successor pins remain available through the approval receipt endpoint. Configuration snapshots, credentials and OpenAPI payloads are never returned.
   * @returns RouteLifecycleHistoryPage Retained production lifecycle reviews
   * @throws ApiError
   */
  public static listRouteLifecycleHistory({
    slug,
    limit = 10,
    before,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Maximum retained reviews to return.
     */
    limit?: number,
    /**
     * Retained app-owned review ID from next_cursor.
     */
    before?: string,
  }): CancelablePromise<RouteLifecycleHistoryPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-lifecycle/history',
      path: {
        'slug': slug,
      },
      query: {
        'limit': limit,
        'before': before,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Approve exact captured lifecycle successor changes.
   * Requires account admin authorization, owner or organization owner/admin identity, completed MFA and same-origin session protection. The server validates explicit authorized canonical or verified custom-domain HTTPS successor mappings against captured inline rooted OpenAPI operation contracts. Unknown or incompatible results fail closed. Receipt bindings include gate, saved requirements and removal policy revisions, configured policy fingerprint and authoritative capture hashes. Receipts expire after one hour and bind destination ownership, capture, hostname and production routing. Destination capture, domain, rule and routing changes invalidate receipts. All destination pins must be supplied together; project destinations require one active production graph member and verified frozen workload settings; ambiguous routing is unavailable for approval. Only successor-review findings on production traffic increases may be cleared; other lifecycle, removal and contract checks remain enforced. Workers also require the receipt database configuration binding to remain current.
   * @returns RouteLifecycleApproval Durable compatibility approval receipt.
   * @throws ApiError
   */
  public static approveRouteLifecycle({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: ApproveRouteLifecycleRequest,
  }): CancelablePromise<RouteLifecycleApproval> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/route-lifecycle/approvals',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Inspect an app-owned lifecycle approval receipt.
   * Requires read authorization and completed MFA. Returns the persisted review bindings and capture invalidation timestamp. An expired or policy-stale receipt remains readable and does not authorize a production traffic increase.
   * @returns RouteLifecycleApproval Persisted approval receipt.
   * @throws ApiError
   */
  public static getRouteLifecycleApproval({
    slug,
    approvalId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * App-owned lifecycle approval receipt identifier.
     */
    approvalId: string,
  }): CancelablePromise<RouteLifecycleApproval> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-lifecycle/approvals/{approval_id}',
      path: {
        'slug': slug,
        'approval_id': approvalId,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Store an authenticated approval for exact route removal.
   * Requires account admin authorization and completed MFA. Uses server-owned captures, canonical successor mappings and retained production telemetry. Requires a staged baseline exposing old and successor routes, compatible successors in the candidate, the configured quiet grace period and explicit observed-only acknowledgement. Approver and timestamps are derived from authentication and server time. Approval is recorded durably. Local attestations and uploaded readiness statuses cannot satisfy this authorization. An approval does not waive other deployment or contract gates.
   * @returns RouteRemovalApproval Durable approval receipt.
   * @throws ApiError
   */
  public static approveRouteRemoval({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: ApproveRouteRemovalRequest,
  }): CancelablePromise<RouteRemovalApproval> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/route-removal/approvals',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read authoritative route removal blockers.
   * Requires apps:read or admin and completed MFA. Uses the same evaluator as production traffic transitions. This read is advisory; enforcement repeats within the traffic transaction. Missing captures, stale approvals, changed policies or renewed old-route observations block enforce mode.
   * @returns RouteRemovalCheck Current policy, removed operations and blockers.
   * @throws ApiError
   */
  public static checkRouteRemoval({
    slug,
    deploymentId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Candidate deployment whose removed routes are evaluated.
     */
    deploymentId: string,
  }): CancelablePromise<RouteRemovalCheck> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-removal/check',
      path: {
        'slug': slug,
      },
      query: {
        'deployment_id': deploymentId,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read saved route requirements.
   * Read the current normalized version 2 route intent and its revision. Public exception rationale is replaced before storage. Requires apps:read or admin and completed MFA. This is the current record; previous revisions remain in customer version control.
   * @returns SavedRouteRequirements Current saved route intent.
   * @throws ApiError
   */
  public static getSavedRouteRequirements({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<SavedRouteRequirements> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-requirements',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Save route requirements with a revision check.
   * Save version 2 route groups or exact assignments after comparing expected_revision. Use 0 to create the first record. Identical normalized intent at the current revision is a no-op. Requires deploy:write or admin and completed MFA. Does not change gateway configuration or invoke application routes.
   * @returns SavedRouteRequirements Saved normalized route intent and current revision.
   * @throws ApiError
   */
  public static saveRouteRequirements({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: SaveRouteRequirementsRequest,
  }): CancelablePromise<SavedRouteRequirements> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/route-requirements',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid revision or requirements document.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Check saved intent against a captured deployment.
   * Read saved requirements, current account/app policy and the selected app-owned capture in one consistent snapshot. Requires apps:read or admin and completed MFA. Violated and unknown findings return a report with 200; unavailable inventory never passes. The result checks current configured policy rather than historical gateway behavior or runtime authorization. No report is persisted and no application request is sent.
   * @returns RouteRequirementsCheck Current policy coverage with saved revision and capture provenance.
   * @throws ApiError
   */
  public static checkRouteRequirements({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CheckRouteRequirementsRequest,
  }): CancelablePromise<RouteRequirementsCheck> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/route-requirements/check',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid deployment identity or expected revision.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read current production monitoring and recovery progress.
   * Requires app read access and completed MFA. Joins current default-scope production route evidence, metadata for the saved open incident, pending checked rollbacks across app scopes, and pending or failed restart handoffs. Reads do not wake workloads, change traffic, or declare incident recovery. Component availability and bounded-list truncation are explicit. Deployment smoke verification remains a separate launch-time result. Customer identities, request evidence, free-form rollback reasons and internal restart errors are omitted. No query parameters are accepted.
   * @returns AppOperationalSummary Independently observed operational facts; unavailable components remain explicit.
   * @throws ApiError
   */
  public static getAppOperationalSummary({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppOperationalSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/operational-summary',
      path: {
        'slug': slug,
      },
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
      },
    });
  }
  /**
   * Read advisory production route monitoring intent.
   * Defaults to disabled, revision zero and no routes. Requires app read access and completed MFA. Configuration is independent of the canary guard. customer_group_by optionally evaluates the same absolute budgets per request-time tenant or API consumer.
   * @returns RouteMonitorConfig Current advisory production monitor configuration.
   * @throws ApiError
   */
  public static getRouteMonitor({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<RouteMonitorConfig> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-monitor',
      path: {
        'slug': slug,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Save advisory production route budgets with a revision check.
   * Requires deployment write access and completed MFA. Enabling requires request telemetry entitlement and routes with absolute budgets. customer_group_by optionally evaluates the same budgets per request-time tenant or API consumer. Replacement intent requires expected_revision; identical intent is a no-op. Changed intent supersedes an open incident without claiming recovery and requires fresh windows. Disabled intent remains writable after a downgrade. Body limit is 16 KiB.
   * @returns RouteMonitorConfig Updated or unchanged production monitoring intent.
   * @throws ApiError
   */
  public static setRouteMonitor({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: SetRouteMonitorRequest,
  }): CancelablePromise<RouteMonitorConfig> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/route-monitor',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        402: `Enabling production monitoring requires request telemetry entitlement.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Monitor revision changed.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read observed route budgets for the fully serving production deployment.
   * Read-only evaluation of two closed UTC minute windows with a 30 second ingestion allowance. Selects the sole fully serving default-scope live deployment; split, incomplete, sparse or unavailable context is unknown. Errors require 20 represented requests and at least two errors to confirm a budget violation; latency requires 100 requests per window. Both windows must start after configuration and serving anchors. If customer_group_by is configured, the same budgets are evaluated per observed request-time identity and sustained cohort violations can make the overall result violated. Customer identities are redacted by default; customer_details=true explicitly includes observed tenant or consumer UUIDs. Coverage is observed_only, not an SLO or full capture. Does not create incidents or change traffic.
   * @returns RouteMonitorReport Current observed production route budget evaluation.
   * @throws ApiError
   */
  public static getRouteMonitorReport({
    slug,
    customerDetails = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Explicitly include request-time tenant or consumer UUIDs when customer_group_by is configured. Defaults to false.
     */
    customerDetails?: boolean,
  }): CancelablePromise<RouteMonitorReport> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-monitor/report',
      path: {
        'slug': slug,
      },
      query: {
        'customer_details': customerDetails,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Evaluate proposed route budgets against recent production traffic without saving them.
   * Read-only evaluation of proposed absolute budgets against the two latest closed UTC minute windows for the sole fully serving default-scope deployment. Requires app read access and completed MFA. Uses the deployment and rollout observation anchors, but not the saved monitor update anchor, because the proposal has not been saved. If the proposal groups by customer, customer IDs are redacted by default. Saving changed configuration resets its observation anchor, so this preview is current evidence and not a prediction of the first post-save report. Violated and unknown findings return 200; no configuration, incident or traffic state is changed. Body limit is 16 KiB.
   * @returns RouteMonitorPreview Read-only evaluation of the proposed budgets and saved revision used for comparison.
   * @throws ApiError
   */
  public static previewRouteMonitor({
    slug,
    requestBody,
    customerDetails = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: PreviewRouteMonitorRequest,
    /**
     * Explicitly include observed tenant or consumer UUIDs when customer_group_by is selected. Defaults to false.
     */
    customerDetails?: boolean,
  }): CancelablePromise<RouteMonitorPreview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/route-monitor/preview',
      path: {
        'slug': slug,
      },
      query: {
        'customer_details': customerDetails,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List bounded saved production route incidents.
   * Requires app read access, completed MFA and current request telemetry entitlement. Returns opening reports and bounded saved redacted debugger evidence. History retains the active incident plus the newest 100 closed incidents within 8 MiB. Pruned or foreign cursors return not found.
   * @returns RouteMonitorIncidentPage One bounded page of retained production route incidents.
   * @throws ApiError
   */
  public static listRouteMonitorIncidents({
    slug,
    limit = 5,
    before,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Maximum incidents to return.
     */
    limit?: number,
    /**
     * Page before this owned retained incident UUID.
     */
    before?: string,
  }): CancelablePromise<RouteMonitorIncidentPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-monitor/incidents',
      path: {
        'slug': slug,
      },
      query: {
        'limit': limit,
        'before': before,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        402: `Listing saved production incidents requires current telemetry entitlement.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read the opening evidence and closure of one saved production route incident.
   * Requires app read access, completed MFA and current request telemetry entitlement. Opening windows, deployment, commit, budgets, request references and dependency summaries are captured when the worker opens the incident. Recovered means comparable healthy windows for all selected budgets and all customers recorded as violating during the incident; superseded means context changed and never emits recovery. Customer identities are redacted by default; customer_details=true explicitly includes saved request-time tenant or consumer UUIDs. Debugger links recheck current retention and authorization.
   * @returns RouteMonitorIncident Saved production incident opening evidence and closure.
   * @throws ApiError
   */
  public static getRouteMonitorIncident({
    slug,
    incident,
    customerDetails = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Owned retained production route incident UUID.
     */
    incident: string,
    /**
     * Explicitly include saved request-time tenant or consumer UUIDs when customer_group_by is configured. Defaults to false.
     */
    customerDetails?: boolean,
  }): CancelablePromise<RouteMonitorIncident> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-monitor/incidents/{incident}',
      path: {
        'slug': slug,
        'incident': incident,
      },
      query: {
        'customer_details': customerDetails,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        402: `Inspecting this saved production incident requires current telemetry entitlement.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read critical telemetry routes, guard mode and revision.
   * Defaults to report mode with no selected routes and revision 0. Requires apps:read or admin and completed MFA. Applies to subsequent traffic increases of an existing canary.
   * @returns RouteHealthGate Current app canary route gate.
   * @throws ApiError
   */
  public static getRouteHealthGate({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<RouteHealthGate> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-health/gate',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Save exact route selectors and guard mode with a revision check.
   * Requires deploy:write or admin and completed MFA. Enforce mode requires traffic split and request telemetry entitlement, and at least one selected route. Maximum 20 distinct exact normalized telemetry method/path selectors. Optional max_p95_ms enables an absolute latency budget; check_latency enables the independent relative slowdown check. expected_revision is mandatory; use 0 initially. Identical configuration is a no-op after checking the revision. Changing selectors, latency checks or mode resets the observation anchor. Request body limit is 16 KiB.
   * @returns RouteHealthGate Updated or unchanged gate configuration.
   * @throws ApiError
   */
  public static setRouteHealthGate({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: SetRouteHealthGateRequest,
  }): CancelablePromise<RouteHealthGate> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/route-health/gate',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Configuration revision changed.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Compare observed critical route errors and optional p95 latency for candidate and stable deployments.
   * Requires apps:read or admin and completed MFA. Compares exact normalized telemetry labels and weighted counts in two consecutive closed UTC minute windows, behind a 30 second ingestion allowance. Error checks need 20 represented requests on each deployment per window. Selected latency checks need 100, with p95 estimates weighted by collapsed telemetry counts. A positive max_p95_ms is an absolute candidate budget; check_latency independently checks for at least 1.5 times stable p95 and at least 100 ms additional latency. Each signal is confirmed independently across both windows. Both windows must begin after the current stage and latest configuration update. Missing, sparse, ambiguous or unavailable evidence is unknown. Coverage is observed_only; full capture and requests dropped before storage cannot be established. Enforce mode holds subsequent advances unless every selected route is healthy. This read does not change traffic; the configured recovery action is evaluated separately by the canary worker. Stable deployment is the sole other live serving deployment in the same scope.
   * @returns RouteHealthReport Current observation evidence, identities and route verdicts.
   * @throws ApiError
   */
  public static getRouteHealthReport({
    slug,
    deployment,
    customers = false,
    customerGroupBy,
    customerDetails = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Owned canary deployment UUID whose current critical-route telemetry is compared with its serving predecessor.
     */
    deployment: string,
    /**
     * Include advisory customer health cohorts. Never changes aggregate health or rollout decisions.
     */
    customers?: boolean,
    /**
     * Request-time identity dimension. Requires customers=true. Tenant is the default; consumer groups API consumers independently.
     */
    customerGroupBy?: 'tenant' | 'consumer',
    /**
     * Include scoped customer UUIDs. Requires customers=true. IDs are omitted by default.
     */
    customerDetails?: boolean,
  }): CancelablePromise<RouteHealthReport> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-health/deployments/{deployment}',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      query: {
        'customers': customers,
        'customer_group_by': customerGroupBy,
        'customer_details': customerDetails,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Investigate route errors or latency using retained request evidence.
   * Requires app read access, completed MFA and request telemetry entitlement. Aggregate report, selected finding and bounded examples share one read-only repeatable-read snapshot, exact candidate/stable pair and closed health windows. The default errors signal selects all 5xx or a watched status code. Latency requires a configured latency check and status_code zero, includes successful responses, and compares retained dependency, guest and wake timings separately from full route p95. Optional customer selection uses recorded tenant or consumer attribution, including identities outside the customer report cap and revoked consumers. Customer UUID input explicitly includes the selected ID; other customer identities are excluded. Counts preserve publisher weights; examples are telemetry rows and may represent multiple requests. Trace links do not guarantee retained spans. This diagnostic read changes no rollout state and includes no payloads, credentials, request headers or raw URLs.
   * @returns RouteHealthInvestigation Current health evidence and bounded metadata references for the requested signal.
   * @throws ApiError
   */
  public static getRouteHealthInvestigation({
    slug,
    deployment,
    method,
    path,
    signal,
    statusCode = 0,
    customerGroupBy,
    customerId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Owned in-flight candidate deployment to investigate against its serving stable revision.
     */
    deployment: string,
    /**
     * Exact configured HTTP method for the normalized telemetry label.
     */
    method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS',
    /**
     * Exact configured normalized route path, without a method prefix or expanded parameters.
     */
    path: string,
    /**
     * Errors is the default. Latency requires check_latency or a positive max_p95_ms and cannot be combined with a nonzero status_code.
     */
    signal?: 'errors' | 'latency',
    /**
     * Zero compares all 5xx responses; a nonzero code must be selected in this route's watch_statuses.
     */
    statusCode?: 0 | 401 | 403 | 404 | 422 | 429,
    /**
     * Recorded identity dimension for a selected customer UUID. Requires customer_id; defaults to tenant when supplied.
     */
    customerGroupBy?: 'tenant' | 'consumer',
    /**
     * Canonical UUID of an owned tenant or app consumer. Selection explicitly exposes this UUID in the investigation.
     */
    customerId?: string,
  }): CancelablePromise<RouteHealthInvestigation> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-health/deployments/{deployment}/investigation',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      query: {
        'method': method,
        'path': path,
        'signal': signal,
        'status_code': statusCode,
        'customer_group_by': customerGroupBy,
        'customer_id': customerId,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Explain saved canary route health decisions with an immutable timeline.
   * Requires apps:read or admin and completed MFA. Available after plan downgrade and rollout completion. Only real advance evaluations with selected routes create snapshots; reads never create history. Identical retries reuse the original decision. Retains the newest 100 entries within 4 MiB per deployment, with each encoded entry bounded to 64 KiB. Pages are newest first by checked_at and id. A missing, foreign or pruned cursor returns 404. Historical evidence does not establish current health or continuous incident duration.
   * @returns RouteHealthHistoryPage Saved decision page; entries is empty when no evaluations have been saved.
   * @throws ApiError
   */
  public static listRouteHealthHistory({
    slug,
    deployment,
    limit = 5,
    before,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment UUID whose retained canary route-health decisions are listed.
     */
    deployment: string,
    /**
     * Maximum number of saved health decisions to return in this page; defaults to 5 and cannot exceed 10.
     */
    limit?: number,
    /**
     * Retained decision UUID from this deployment; excludes this entry and every newer entry.
     */
    before?: string,
  }): CancelablePromise<RouteHealthHistoryPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-health/deployments/{deployment}/history',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      query: {
        'limit': limit,
        'before': before,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read the exact observations and thresholds behind a retained rollout decision.
   * Requires apps:read or admin and completed MFA. Account, app and deployment scoped. Available after plan downgrade and rollout completion. Returns 404 for a missing, foreign or pruned decision. Reads never evaluate telemetry or change traffic. Allowed entries committed with the traffic transaction; blocked entries preserve only the held evaluation. Later failed traffic transactions leave no allowed snapshot.
   * @returns RouteHealthHistoryEntry Immutable saved decision, including error and latency evidence and observation context.
   * @throws ApiError
   */
  public static getRouteHealthHistoryEntry({
    slug,
    deployment,
    decisionId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment UUID that owns the requested saved canary health decision.
     */
    deployment: string,
    /**
     * Retained health decision UUID identifying the exact rollout evaluation evidence to retrieve.
     */
    decisionId: string,
  }): CancelablePromise<RouteHealthHistoryEntry> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-health/deployments/{deployment}/history/{decision_id}',
      path: {
        'slug': slug,
        'deployment': deployment,
        'decision_id': decisionId,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read canary route gate mode and revision.
   * Defaults to report mode and revision 0. Requires apps:read or admin and completed MFA. Gates route-requirements evidence on canary advances and lifecycle declarations on production traffic increases, including initial activation and ordinary cutovers. Dark staging, validated abort and automatic incident recovery remain available.
   * @returns CanaryRouteGate Current saved route-requirements gate mode and revision.
   * @throws ApiError
   */
  public static getCanaryRouteGate({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<CanaryRouteGate> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-requirements/gate',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Set report or enforce mode with a gate revision check.
   * Requires deploy:write or admin and completed MFA. Enforce mode requires saved route requirements and a plan with canaries and captured endpoint discovery. Report mode remains available after downgrade. expected_revision is mandatory; use 0 initially. Identical mode is a no-op after checking the revision.
   * @returns CanaryRouteGate Saved or unchanged canary route-requirements gate configuration.
   * @throws ApiError
   */
  public static setCanaryRouteGate({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: SetCanaryRouteGateRequest,
  }): CancelablePromise<CanaryRouteGate> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/route-requirements/gate',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Gate revision changed or route requirements are missing.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read the latest automatic route check and freshness.
   * Read queue state and the latest stored deployment verdict in a consistent snapshot. Freshness compares current intent revision/hash, capture hash/truncation and configuration hash. A historical satisfied result can be stale. Requires apps:read or admin, completed MFA and current captured endpoint discovery entitlement. Does not run a new check or gate deployment.
   * @returns AutomaticRouteCheck Latest stored verdict with current queue and freshness state.
   * @throws ApiError
   */
  public static getAutomaticRouteCheck({
    slug,
    deployment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * App-owned captured deployment UUID whose latest automatic requirement check and freshness are requested.
     */
    deployment: string,
  }): CancelablePromise<AutomaticRouteCheck> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-requirements/checks/{deployment}',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        402: `Current plan does not include captured endpoint discovery.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List retained route check history.
   * Read bounded completed evidence newest first. Retains at most 20 entries and 64 MiB of encoded history per deployment. A missing/pruned cursor returns 404. Requires app read access, completed MFA and current captured endpoint discovery entitlement. Historical satisfied evidence cannot satisfy a current deployment gate.
   * @returns RouteCheckHistoryPage Retained completed checks and optional next cursor.
   * @throws ApiError
   */
  public static listRouteCheckHistory({
    slug,
    deployment,
    limit = 5,
    before,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Captured deployment UUID whose retained route requirement check history is listed.
     */
    deployment: string,
    /**
     * Maximum number of completed requirement-check summaries in this page; defaults to 5 with a maximum of 10.
     */
    limit?: number,
    /**
     * Retained check UUID from this deployment; return only older entries and exclude the selected entry.
     */
    before?: string,
  }): CancelablePromise<RouteCheckHistoryPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-requirements/checks/{deployment}/history',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      query: {
        'limit': limit,
        'before': before,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        402: `Current plan lacks the captured endpoint discovery entitlement required to list route check history.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read one retained immutable route check.
   * Read the exact completion identified by a change notification. Missing or expired evidence returns 404. Requires app read access, completed MFA and current captured endpoint discovery entitlement. Historical evidence does not establish current safety.
   * @returns RouteCheckHistoryEntry Retained immutable check and finding changes.
   * @throws ApiError
   */
  public static getRouteCheckHistoryEntry({
    slug,
    deployment,
    checkId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Captured deployment UUID that owns the requested historical route requirement check.
     */
    deployment: string,
    /**
     * Retained completed check UUID selecting the exact immutable requirement and finding-change evidence.
     */
    checkId: string,
  }): CancelablePromise<RouteCheckHistoryEntry> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-requirements/checks/{deployment}/history/{check_id}',
      path: {
        'slug': slug,
        'deployment': deployment,
        'check_id': checkId,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        402: `Reading retained requirement-check evidence requires captured endpoint discovery on the current plan.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Queue a current route check for one deployment.
   * Accept an empty body and durably queue current saved intent, capture and policy evaluation. Coalesces with pending work and resets a failed retry. Requires apps:read or admin, completed MFA and captured endpoint discovery entitlement. This action changes no gateway policy and makes no application requests. Poll getAutomaticRouteCheck for completion.
   * @returns any Durable check queued or existing pending check retained.
   * @throws ApiError
   */
  public static refreshAutomaticRouteCheck({
    slug,
    deployment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * App-owned captured deployment UUID to queue for automatic route requirements reevaluation.
     */
    deployment: string,
  }): CancelablePromise<{
    app_id: string;
    deployment_id: string;
    status: 'queued';
  }> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/route-requirements/checks/{deployment}/refresh',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        402: `The current plan cannot queue route requirement reevaluation because captured endpoint discovery is unavailable.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * plan route policy.
   * Read a consistent app policy snapshot and propose throttle and budget changes for concrete requests or captured route groups. Group plans bind an app-owned captured deployment contract and report full inventory impact. Requires apps:read or admin and completed MFA. This POST only reads configuration.
   * @returns RoutePolicyPlan Consistent policy snapshot with proposed rule changes and before/after coverage.
   * @throws ApiError
   */
  public static planRoutePolicy({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: RoutePolicyPlanRequest,
  }): CancelablePromise<RoutePolicyPlan> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/route-policy/plan',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed. Invalid or oversized route requirements document.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * apply route policy.
   * Recompute the reviewed fingerprint under account, app, and rule locks, plus deployment and captured-contract locks for group plans. Commit all changes and a durable receipt in one transaction. Requires deploy:write or admin and completed MFA. The same idempotency key and request recover the original receipt after a lost response. Gateway state is a separate observation; converging or unknown does not undo a committed receipt.
   * @returns RoutePolicyApplyResponse Committed transaction receipt and gateway acknowledgment observation.
   * @throws ApiError
   */
  public static applyRoutePolicy({
    slug,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Stable retry key, retained with the receipt for the app lifetime. Reuse with a different request returns conflict.
     */
    idempotencyKey: string,
    requestBody: RoutePolicyApplyRequest,
  }): CancelablePromise<RoutePolicyApplyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/route-policy/apply',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed. Missing confirmation, invalid fingerprint, or invalid retry key.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * get route policyReceipt.
   * Recover the committed rule IDs and configuration verification for this owned app. The receipt is historical evidence, not a fresh live policy or gateway check.
   * @returns RoutePolicyReceipt Historical configuration verification and real rule IDs.
   * @throws ApiError
   */
  public static getRoutePolicyReceipt({
    slug,
    receiptId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Durable route policy receipt ID.
     */
    receiptId: string,
  }): CancelablePromise<RoutePolicyReceipt> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/route-policy/receipts/{receipt_id}',
      path: {
        'slug': slug,
        'receipt_id': receiptId,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Manually park all running instances.
   * @returns void
   * @throws ApiError
   */
  public static parkApp({
    slug,
    fresh = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * For an isolated preview, invalidate its snapshots after all instances drain so the next request cold-boots from the artifact. Production apps reject this option.
     */
    fresh?: boolean,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/park',
      path: {
        'slug': slug,
      },
      query: {
        'fresh': fresh,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Queue a durable instance pre-warm.
   * @returns AppWakeResponse The app already has a routable running instance; no wake is queued. wake_id is that instance's wake id.
   * @throws ApiError
   */
  public static wakeApp({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppWakeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/wake',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: admission_refused — the account's spend cap (accounts.overage_cap_cents) is met/exceeded by the current-month overage. Schedd refuses new wakes until the customer raises or clears the cap via POST /v1/account/overage-cap. The Limit / Observed fields carry the cap and current overage in integer cents so a script can compute "how much to raise" without parsing prose. No Retry-After: the cap is a deliberate customer budget, not back-pressure.`,
        404: `code: not_found`,
        429: `code: plan_limit_concurrency`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Restart an app.
   * Parks every live instance, captures a fresh snapshot, and queues one
   * replacement wake. Requests are single-flight per app; the returned
   * wake_id identifies the replacement wake in the wake timeline. With
   * `fresh=true`, destroys live instances without capturing process memory,
   * invalidates cached snapshots, and cold-boots with the latest environment
   * and secrets. The fresh path is durably queued.
   * For `fresh=true`, use the returned `wake_id` with
   * `GET /v1/apps/{slug}/runtime-config-restarts/{wake_id}` to inspect
   * queued, running, retrying, completed, or failed status and any safe
   * failure reason.
   *
   * @returns AppRestartResponse Restart accepted.
   * @throws ApiError
   */
  public static restartApp({
    slug,
    idempotencyKey,
    fresh = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
    /**
     * Cold-boot with current runtime configuration instead of capturing/restoring process memory.
     */
    fresh?: boolean,
  }): CancelablePromise<AppRestartResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/restart',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'fresh': fresh,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: admission_refused — the account's spend cap (accounts.overage_cap_cents) is met/exceeded by the current-month overage. Schedd refuses new wakes until the customer raises or clears the cap via POST /v1/account/overage-cap. The Limit / Observed fields carry the cap and current overage in integer cents so a script can compute "how much to raise" without parsing prose. No Retry-After: the cap is a deliberate customer budget, not back-pressure.`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
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
   * Get the status of a fresh runtime-configuration restart.
   * Returns the durable scheduler handoff state for a fresh restart.
   * `completed` means the replacement operation finished successfully;
   * `failed` means the durable handoff exhausted its retry budget. A
   * failure_reason is a stable, safe category and does not expose internal
   * error details.
   *
   * @returns RuntimeConfigRestartStatusResponse Current durable restart status.
   * @throws ApiError
   */
  public static getRuntimeConfigRestartStatus({
    slug,
    wakeId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Wake ID returned by POST /v1/apps/{slug}/restart?fresh=true.
     */
    wakeId: string,
  }): CancelablePromise<RuntimeConfigRestartStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/runtime-config-restarts/{wake_id}',
      path: {
        'slug': slug,
        'wake_id': wakeId,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Schedule capacity restoration ahead of a demand window.
   * Persists a temporary prewarm intent. schedd claims it during the
   * platform's lead time (currently 60 seconds before wake_at), restores
   * up to count instances through the normal admission gates, and never
   * changes the app's permanent min_instances floor.
   *
   * @returns PrewarmIntentResponse Prewarm intent accepted.
   * @throws ApiError
   */
  public static createPrewarm({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: PrewarmRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<PrewarmIntentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/prewarm',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * List scheduled and completed prewarm intents.
   * @returns PrewarmIntentResponse Prewarm intents ordered by demand-window start.
   * @throws ApiError
   */
  public static listPrewarms({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<PrewarmIntentResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/prewarms',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Cancel a pending prewarm intent.
   * @returns void
   * @throws ApiError
   */
  public static cancelPrewarm({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Prewarm intent UUID.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/prewarms/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: not_implemented — this optional capability is not enabled on the serving daemon.`,
      },
    });
  }
  /**
   * Purge cached responses for an app.
   * Records a durable response-cache purge request for every gateway and
   * the optional distributed cache tier. Gateways replay missed requests;
   * `GET /v1/apps/{slug}/policy/status` reports convergence in `response_cache`.
   * The optional path glob limits the purge to
   * matching normalized request paths. The optional tag limits it to
   * responses carrying that Cache-Tag. Path and tag are mutually exclusive;
   * omit both to purge the complete app cache.
   *
   * @returns void
   * @throws ApiError
   */
  public static purgeAppCache({
    slug,
    path,
    tag,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Optional normalized request path glob (for example `/products*`).
     */
    path?: string,
    /**
     * Optional cache tag (for example `product:42`); cannot be combined with path.
     */
    tag?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/cache',
      path: {
        'slug': slug,
      },
      query: {
        'path': path,
        'tag': tag,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Rename an app.
   * @returns AppResponse The renamed app.
   * @throws ApiError
   */
  public static renameApp({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Rename payload — the new slug. See RenameAppRequest.
     */
    requestBody: RenameAppRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/rename',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        409: `code: app_rename_failed — slug taken by another live app, or DB unique violation.`,
      },
    });
  }
  /**
   * Stream app logs (SSE).
   * Server-Sent Events stream of instance logs. NOTE: this endpoint is
   * currently mounted behind `s.authLimited` and is documented here for
   * reference; the dashboard and CLI also consume it directly.
   *
   * Two modes share this URL:
   *
   * - **Snapshot (default)** — `?follow=0` replays the retained entries
   * from each live instance and closes with `event: end`. This is the
   * mode used by `gregale logs` for a finite command that can be piped
   * into other tools.
   *
   * - **Live** — `?follow=1` holds the connection open and streams new
   * entries from the per-instance ring buffer. The stream terminates
   * with `event: end` when the backstop fires (10 minutes idle), the
   * schedd returns NotFound (parked app), or the connection closes.
   *
   * - **Archive (`?archive=1`)** — fetches a single day's
   * per-instance log batch from the S3 bucket the apid shipper
   * writes into. `?instance=<id>` selects the Firecracker instance
   * id; `?date=YYYY-MM-DD` selects the day. The response is the
   * same SSE shape as the live stream (`event: log` per line,
   * `event: end` terminal with `archive_complete` /
   * `archive_missing` / `archive_degraded` reasons) so the SDK
   * decoder treats the two paths interchangeably. Archive is
   * gated by `Plan.LogArchiveEnabled()` — Free customers receive a
   * one-day archive window. The per-plan retention cap (Free 1d /
   * Hobby 7d / Pro 30d / Scale 90d) refuses `?date=` values
   * outside the window with 403 + `log_archive_retention_exceeded`.
   *
   * Each `event: log` payload preserves the original `line`. When that
   * line is a valid JSON object with a recognized `level` or `severity`
   * field, the server adds a canonical `level` value (`info`, `warn`, or
   * `error`); plain-text and unclassified lines omit the field.
   *
   * @returns any A text/event-stream of structured log lines, terminated by an empty SSE frame when the connection closes.
   * @throws ApiError
   */
  public static streamAppLogs({
    slug,
    follow = 0,
    grep,
    since,
    level,
    archive = 0,
    instance,
    date,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * If 1, hold the connection open and stream new entries; if 0 (default), replay the retained page and close.
     */
    follow?: 0 | 1,
    /**
     * Substring filter applied to each log line.
     */
    grep?: string,
    /**
     * RFC3339 lower-bound on the line timestamp.
     */
    since?: string,
    /**
     * Exact match on the structured `level` field (info, warn, or error) surfaced in each matching log event. Empty = no level filter. The CLI and the apid handler both validate against the same enum (api.IsValidLogLevel in pkg/api/logs.go); an unknown value short-circuits with an SSE error frame carrying code invalid_level.
     *
     */
    level?: 'info' | 'warn' | 'error',
    /**
     * If 1, serve archived logs from S3 instead of the live ring buffer. Requires `instance=<id>` and `date=YYYY-MM-DD`. Gated by `Plan.LogArchiveEnabled()` — Free plans have a one-day archive window. The per-plan retention cap (Free 1d / Hobby 7d / Pro 30d / Scale 90d) refuses `date=` values outside the window.
     *
     */
    archive?: 0 | 1,
    /**
     * Required when `archive=1`. The Firecracker instance id to read archived logs from (matches the `instance_id` field in the live SSE frames).
     *
     */
    instance?: string,
    /**
     * Required when `archive=1`. The day to read in YYYY-MM-DD UTC. Must be inside the per-plan retention cap (Free 1d / Hobby 7d / Pro 30d / Scale 90d) — outside values return 403 + `log_archive_retention_exceeded`. Future dates are refused with the same code.
     *
     */
    date?: string,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/logs',
      path: {
        'slug': slug,
      },
      query: {
        'follow': follow,
        'grep': grep,
        'since': since,
        'level': level,
        'archive': archive,
        'instance': instance,
        'date': date,
      },
      errors: {
        401: `code: unauthorized`,
        402: `Plan does not include log archive read-back. This response is reserved for plans without archive entitlement.`,
        403: `Log archive retention cap exceeded; \`?date=\` is outside the per-plan window.`,
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
   * Account-wide per-app metrics rollup.
   * One call replaces N per-app `/v1/apps/{slug}/metrics` calls
   * (issue #393). Returns the same `AppMetricsResponse` shape
   * per app, keyed by `app_slug`, so the dashboard can render
   * all apps on a single page without a per-app fan-out.
   *
   * Range is the closed vocabulary from the per-app endpoint:
   * `5m` (default) | `15m` | `1h` | `6h` | `24h` | `7d` | `15d`.
   * Prometheus failure short-circuits the entire response
   * (never partial-populated) and emits `source:
   * "degraded: <reason>"` with zeroed `apps`, matching the
   * per-app contract exactly.
   *
   * PromQL cost: 6 round-trips regardless of N apps (vs. 7N
   * for the naive per-app loop) — see `pkg/promql.Client.QueryMap`
   * and `Client.QueryBuckets`. This rollup exposes the same
   * Hobby+ signals as the per-app endpoint, so Free accounts
   * receive 402.
   *
   * @returns AppsMetricsResponse The rollup.
   * @throws ApiError
   */
  public static getAppsMetrics({
    range = '5m',
  }: {
    /**
     * Time window applied to every per-app rollup row. Default `5m`.
     */
    range?: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d',
  }): CancelablePromise<AppsMetricsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/metrics',
      query: {
        'range': range,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_per_app_metrics_not_allowed — the account plan does not include per-app metrics or wake narratives; upgrade to Hobby or above.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List the canonical wake-timeline frames for one wake.
   * Oldest-first (forward narrative). Returns every typed
   * `wake.*` events row for the given wake_id: queue_accepted
   * → admitted → boot_started → boot_completed →
   * readiness_200 → proxy_first_byte. Build and deploy
   * failures (`wake.build_failed`, `wake.deploy_failed`,
   * `wake.boot_failed`) are joined in alongside the success
   * path so a single GET shows the whole lifecycle.
   *
   * When an attempted restore falls back to a successful cold boot
   * because the application's `after_restore` callback failed, the
   * `wake.boot_completed` payload includes
   * `data.restore_fallback_reason: after_restore_failed`. The field is
   * absent on other wakes. This reason never contains callback URLs,
   * response bodies, or raw error text.
   *
   * For `wake.proxy_first_byte`, `data.latency_ms` is measured from
   * request/queue acceptance through the first upstream byte. New rows
   * also include `data.proxy_latency_ms` for the final bridge hop. Rows
   * written before this contract correction contain the former
   * proxy-only value in `latency_ms` and omit `proxy_latency_ms`.
   * Gateway rows may also include `data.gateway_phases_ms`, a per-wake
   * map of integer-millisecond durations for `pre_admission`,
   * `scheduler_wake`, `target_publication`, `post_publication`, and
   * `internal_proxy`. This keeps phase attribution joinable to one
   * `wake_id` without adding wake IDs as metric labels.
   *
   * The endpoint is a sub-resource of `/v1/apps/{slug}`;
   * auth and rate-limit share the §12 per-app budget with
   * logs/metrics/wake. Cross-account access 404s the
   * same way unknown slugs do (forge-proof: every row's
   * `data.app_id` is verified to match the resolved app).
   * Wake narratives are a Hobby+ observability surface; Free
   * accounts receive 402 before slug or wake lookup.
   *
   * @returns WakeTimelineResponse Wake-timeline frames.
   * @throws ApiError
   */
  public static listWakeTimeline({
    slug,
    wakeId,
    since,
    limit = 200,
  }: {
    /**
     * App slug (lowercase, kebab-case; per-account unique).
     */
    slug: string,
    /**
     * The per-wake correlation handle minted by the schedd
     * engine (UUID v4 in production). The endpoint returns
     * every `wake.*` events row whose `data.wake_id`
     * matches — the partial index `events_wake_id_idx`
     * (migrations/00113) serves the read in O(frames)
     * regardless of the events table size.
     *
     */
    wakeId: string,
    /**
     * Only return rows with `at >= since` (RFC 3339).
     */
    since?: string,
    /**
     * Max frames to return. Silently capped at 1000.
     */
    limit?: number,
  }): CancelablePromise<WakeTimelineResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/wakes/{wake_id}/timeline',
      path: {
        'slug': slug,
        'wake_id': wakeId,
      },
      query: {
        'since': since,
        'limit': limit,
      },
      errors: {
        400: `Malformed query parameter on the wake-timeline read — \`since\` not RFC 3339 or \`limit\` out of range.`,
        401: `code: unauthorized`,
        402: `code: plan_per_app_metrics_not_allowed — the account plan does not include per-app metrics or wake narratives; upgrade to Hobby or above.`,
        404: `No such app (slug) or wake_id is unknown.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List the lifecycle timeline for one sidecar.
   * Oldest-first (forward narrative). Returns the sidecar's init-exit,
   * restart, and health-transition frames. The `latest` field is the
   * most recent `wake.sidecar_health` status (`starting`, `healthy`,
   * `unhealthy`, `restarting`, `failed`, `ready`, or `unready`) when one is available.
   *
   * The endpoint is a sub-resource of `/v1/apps/{slug}` and uses the
   * same MFA, scope, per-app rate-limit, and Hobby+ observability gates
   * as the wake timeline. Cross-account rows are dropped by verifying
   * every event's `data.app_id` against the slug's resolved app.
   *
   * @returns SidecarTimelineResponse Sidecar lifecycle frames and the latest health snapshot.
   * @throws ApiError
   */
  public static listSidecarTimeline({
    slug,
    sidecarName,
    since,
    limit = 200,
  }: {
    /**
     * App slug that owns this sidecar timeline (lowercase, kebab-case; per-account unique).
     */
    slug: string,
    /**
     * The sidecar name from the app deployment's sidecar set.
     */
    sidecarName: string,
    /**
     * Only return rows with `at > since` (RFC 3339).
     */
    since?: string,
    /**
     * Max frames to return. Values above 1000 are rejected.
     */
    limit?: number,
  }): CancelablePromise<SidecarTimelineResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/sidecars/{sidecar_name}/timeline',
      path: {
        'slug': slug,
        'sidecar_name': sidecarName,
      },
      query: {
        'since': since,
        'limit': limit,
      },
      errors: {
        400: `Malformed query parameter — \`since\` is not RFC 3339 or \`limit\` is out of range.`,
        401: `code: unauthorized`,
        402: `code: plan_per_app_metrics_not_allowed — the account plan does not include per-app metrics or wake narratives; upgrade to Hobby or above.`,
        404: `No such app (slug) or sidecar timeline is unknown.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
}
