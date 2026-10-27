/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppBindingInventory } from '../models/AppBindingInventory.js';
import type { AppHealthHistoryPage } from '../models/AppHealthHistoryPage.js';
import type { AppHealthResponse } from '../models/AppHealthResponse.js';
import type { AppMetricsResponse } from '../models/AppMetricsResponse.js';
import type { AppResponse } from '../models/AppResponse.js';
import type { AppRestartResponse } from '../models/AppRestartResponse.js';
import type { AppsMetricsResponse } from '../models/AppsMetricsResponse.js';
import type { AppStreamingStatus } from '../models/AppStreamingStatus.js';
import type { AppUsageSummaryResponse } from '../models/AppUsageSummaryResponse.js';
import type { AppWakeResponse } from '../models/AppWakeResponse.js';
import type { AppWakeTimelineResponse } from '../models/AppWakeTimelineResponse.js';
import type { BindingReleasePolicy } from '../models/BindingReleasePolicy.js';
import type { BindingVerificationReview } from '../models/BindingVerificationReview.js';
import type { BindingVerificationRun } from '../models/BindingVerificationRun.js';
import type { BindingVerificationRunDiagnostics } from '../models/BindingVerificationRunDiagnostics.js';
import type { BindingVerificationRunList } from '../models/BindingVerificationRunList.js';
import type { BindingVerificationRunStep } from '../models/BindingVerificationRunStep.js';
import type { CreateAppRequest } from '../models/CreateAppRequest.js';
import type { CreateBindingVerificationRunRequest } from '../models/CreateBindingVerificationRunRequest.js';
import type { CreateDeployTokenRequest } from '../models/CreateDeployTokenRequest.js';
import type { CustomMetricListResponse } from '../models/CustomMetricListResponse.js';
import type { CustomMetricRequest } from '../models/CustomMetricRequest.js';
import type { DebugCoverageResponse } from '../models/DebugCoverageResponse.js';
import type { DebugDependencyLatencyResponse } from '../models/DebugDependencyLatencyResponse.js';
import type { DebugRegressionActionRequest } from '../models/DebugRegressionActionRequest.js';
import type { DebugRegressionActionResponse } from '../models/DebugRegressionActionResponse.js';
import type { DebugRegressionReview } from '../models/DebugRegressionReview.js';
import type { DebugRegressionsResponse } from '../models/DebugRegressionsResponse.js';
import type { DebugRunningResponse } from '../models/DebugRunningResponse.js';
import type { DeletedAppListResponse } from '../models/DeletedAppListResponse.js';
import type { DeletedAppSummary } from '../models/DeletedAppSummary.js';
import type { DeployTokenResponse } from '../models/DeployTokenResponse.js';
import type { EdgeProtectionResponse } from '../models/EdgeProtectionResponse.js';
import type { GuardedDebugRegressionActionRequest } from '../models/GuardedDebugRegressionActionRequest.js';
import type { ListDeployTokensResponse } from '../models/ListDeployTokensResponse.js';
import type { PreAuthObservationsResponse } from '../models/PreAuthObservationsResponse.js';
import type { PrewarmIntentResponse } from '../models/PrewarmIntentResponse.js';
import type { PrewarmRequest } from '../models/PrewarmRequest.js';
import type { Problem } from '../models/Problem.js';
import type { RenameAppRequest } from '../models/RenameAppRequest.js';
import type { RequestAnalyticsResponse } from '../models/RequestAnalyticsResponse.js';
import type { RequestAnalyticsTimeseriesResponse } from '../models/RequestAnalyticsTimeseriesResponse.js';
import type { RequestAuditListResponse } from '../models/RequestAuditListResponse.js';
import type { ReviewBindingVerificationRequest } from '../models/ReviewBindingVerificationRequest.js';
import type { RotateDeployTokenRequest } from '../models/RotateDeployTokenRequest.js';
import type { RotateDeployTokenResponse } from '../models/RotateDeployTokenResponse.js';
import type { RouteCustomerUsageResponse } from '../models/RouteCustomerUsageResponse.js';
import type { RuntimeConfigRestartStatusResponse } from '../models/RuntimeConfigRestartStatusResponse.js';
import type { RuntimePolicyStatusResponse } from '../models/RuntimePolicyStatusResponse.js';
import type { ServiceBindingExplanation } from '../models/ServiceBindingExplanation.js';
import type { ServiceWakeAheadResponse } from '../models/ServiceWakeAheadResponse.js';
import type { SetBindingReleasePolicyRequest } from '../models/SetBindingReleasePolicyRequest.js';
import type { SetServiceWakeAheadRequest } from '../models/SetServiceWakeAheadRequest.js';
import type { SourceReviewRequest } from '../models/SourceReviewRequest.js';
import type { SourceReviewResponse } from '../models/SourceReviewResponse.js';
import type { UpdateAppRequest } from '../models/UpdateAppRequest.js';
import type { WakeTimelineResponse } from '../models/WakeTimelineResponse.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
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
   * until they support binding fences. Policies with storage requirements must use
   * release-policy-with-object-storage for updates, including explicit clearing.
   * Policies with queue requirements must use release-policy-with-queues and
   * explicitly retain or clear every queue and storage requirement.
   * To recover without evidence, explicitly
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Explain service authorization for an exact caller deployment.
   * Read-only policy and routing observation using the gateway app authorizer,
   * managed environment binding resolver and project release resolver. Requires
   * apps:read or admin and completed MFA. The deployment must belong to the
   * selected app. Its environment is inferred; scope, when supplied, asserts
   * that environment. Managed deployments never fall back to global app names.
   * Target identities are returned only within the selected account. Policy
   * denials and incomplete contexts return 200 with result denied or
   * indeterminate. Missing or foreign caller deployments return 404. Previews,
   * qualification guests and development bridges are not evaluated. No tasks,
   * guest requests, writes or provider calls occur. Separate metadata reads
   * are not an atomic snapshot, caller authentication, runtime adoption or
   * connectivity evidence. Unpinned routes do not select a target deployment.
   * Responses use Cache-Control: no-store.
   *
   * @returns ServiceBindingExplanation Sanitized policy and routing explanation, without connectivity claims.
   * @throws ApiError
   */
  public static explainAppServiceBinding({
    slug,
    service,
    deploymentId,
    scope,
    method = 'GET',
    path = '/',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Logical service workload for managed deployments, otherwise target app slug.
     */
    service: string,
    /**
     * Exact app-owned caller deployment UUID. This selector does not establish live caller identity.
     */
    deploymentId: string,
    /**
     * Optional assertion of the selected deployment environment. A mismatch produces a denied explanation.
     */
    scope?: string,
    /**
     * HTTP method to evaluate against the target scope.
     */
    method?: string,
    /**
     * Origin-form request path. Ambiguous paths are rejected; query values are omitted from the report.
     */
    path?: string,
  }): CancelablePromise<ServiceBindingExplanation> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/bindings/services/{service}/explanation',
      path: {
        'slug': slug,
        'service': service,
      },
      query: {
        'deployment_id': deploymentId,
        'scope': scope,
        'method': method,
        'path': path,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Inspect all runtime resource bindings attached to an app.
   * Service entries use the selected deployment's frozen environment graph
   * when managed; without deployment_id the current live deployment is selected.
   * Logical service names remain distinct from physical target app identities.
   * service_context reports the reviewed route and a context revision, never
   * connectivity evidence. Ordinary task-probe verification of managed services
   * is unsupported and app-scoped receipts are excluded. Managed target routes
   * cannot be overridden through service_targets. Invalid frozen context or
   * unreadable routes make complete=false without falling back to app settings.
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
    serviceTargets,
    scope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Select the service namespace and evidence for this exact app-owned, materialized live deployment, including zero-traffic candidates. No fallback to evidence from another deployment. Omit to use the manual-task selection.
     */
    deploymentId?: string,
    /**
     * JSON object mapping declared service names to exact live deployment UUIDs in the caller scope. Requires deployment_id. Target evidence is isolated from ordinary any-replica evidence. Does not change routes or grant access.
     */
    serviceTargets?: string,
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
        'service_targets': serviceTargets,
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
        409: `Conflict; deploy_settings_conflict means the guarded app, deployment or targeted settings changed and no settings were updated.`,
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
   * the scheduler if a notification is missed. Scheduler scaling `active` means
   * the owning scheduler loaded the policy, not that the replica target was
   * reached. This does not attest host-level firewall policy or guest
   * configuration.
   * `unverified` means no revision or no relevant serving fleet can be observed.
   * Gateway policy acknowledgements belong to the current process generation
   * and require a fresh serving report. Replacement clears convergence until
   * the new consumers replay. `traffic_runtime` reports compute gateway wiring
   * separately; fresh wiring does not prove request enforcement. The optional
   * `wait` applies to policy convergence only.
   *
   * @returns RuntimePolicyStatusResponse Current application state; pending remains possible after wait expires.
   * @throws ApiError
   */
  public static getRuntimePolicyStatus({
    slug,
    wait,
    edgeRulesScope = 'app',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Optional bounded wait for active state, up to 10s.
     */
    wait?: string,
    /**
     * Account scope covers all owned app rules, including wildcards and a conservative revision floor for unknown historical owners. Fleet scope includes all mutations.
     */
    edgeRulesScope?: 'app' | 'account' | 'fleet',
  }): CancelablePromise<RuntimePolicyStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/policy/status',
      path: {
        'slug': slug,
      },
      query: {
        'wait': wait,
        'edge_rules_scope': edgeRulesScope,
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
   * List account-owned retained deleted apps.
   * Bounded lifecycle inventory; never includes another account's tombstones.
   * Eligibility does not reserve quota or capacity. Use the returned deadline
   * and evaluation time, not a deadline inferred from the browser clock.
   *
   * @returns DeletedAppListResponse One bounded account-owned page.
   * @throws ApiError
   */
  public static listDeletedApps({
    limit = 25,
    cursor,
  }: {
    /**
     * Maximum records in this account-owned page.
     */
    limit?: number,
    /**
     * Opaque cursor from this account and inventory order. Restart when rejected.
     */
    cursor?: string,
  }): CancelablePromise<DeletedAppListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deleted-apps',
      query: {
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read one account-owned deleted app before restore review.
   * Returns current lifecycle evidence for the exact owned retained ID; foreign and restored IDs are absent.
   * @returns DeletedAppSummary Current lifecycle evidence; active or foreign apps return 404.
   * @throws ApiError
   */
  public static getDeletedApp({
    id,
  }: {
    /**
     * Exact deleted-app ID returned by this account's inventory.
     */
    id: string,
  }): CancelablePromise<DeletedAppSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/deleted-apps/{id}',
      path: {
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Restore an identified app within its retained deletion window.
   * Requires deploy-write, MFA completion and trusted browser origin for cookie
   * authentication. Rechecks identity, deadline, purge claim, quota and capacity.
   * This operation has no idempotent replay receipt. After an uncertain response,
   * reconcile exact-ID reads; never automatically retry or fall back to slug restore.
   * Success acknowledges restoration, not execution health.
   *
   * @returns AppResponse The restored exact app.
   * @throws ApiError
   */
  public static restoreDeletedApp({
    id,
    requestBody,
  }: {
    /**
     * Exact account-owned deleted-app ID; never resolved to a replacement slug.
     */
    id: string,
    /**
     * Optional empty object; no dry-run or other fields are accepted.
     */
    requestBody?: Record<string, any>,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/deleted-apps/{id}/restore',
      path: {
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Restore an app during its deletion grace window.
   * Bodyless legacy callers remain supported. An optional expected_app_id
   * fences the selected identity on updated servers. For mixed-version
   * safety, use the dedicated deleted-app ID route instead of relying on
   * legacy body handling. This route has no idempotent replay receipt.
   *
   * @returns AppResponse The restored app.
   * @throws ApiError
   */
  public static restoreApp({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Optional reviewed app identity on updated servers; bodyless callers remain compatible.
     */
    requestBody?: {
      expected_app_id?: string;
    },
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/restore',
      path: {
        'slug': slug,
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
   * List the app's pushed custom metrics
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
   * Push a custom application metric
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
   * Delete a custom application metric
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
   * Summarize what the edge rejected for an app.
   * Read-only security telemetry from the per-app gateway counters that
   * the edge security alert presets evaluate: pre-auth source-limit
   * decisions, kind=validate mismatches by mode, and requests answered by
   * the other edge gates (JWT, IP, geo, ingress, body limits, throttles)
   * by gate and status. On Prometheus failure, source starts with
   * `degraded:` and counts are zero.
   *
   * @returns EdgeProtectionResponse Edge rejection counts for the range.
   * @throws ApiError
   */
  public static getAppEdgeProtection({
    slug,
    range = '1h',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Time window. Defaults to one hour.
     */
    range?: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d',
  }): CancelablePromise<EdgeProtectionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-protection',
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
   * Read-only assessment of selected-scope HTTP serving deployments,
   * replica readiness, node evidence and the last 5 minutes of request
   * telemetry scoped to current traffic-bearing releases in the selected scope. It never wakes
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
    environment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Registered app environment whose serving health is observed; omit for its default environment.
     */
    environment?: string,
  }): CancelablePromise<AppHealthResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/health',
      path: {
        'slug': slug,
      },
      query: {
        'environment': environment,
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
   * Per-app request metrics.
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
   * HTML page (`/dashboard/apps/{slug}/wake-timeline`).
   * The HTML page keeps its pre-rendered HTML chips; this endpoint
   * emits the same data as JSON so a separate frontend agent can
   * render without re-parsing HTML.
   *
   * Legacy requests select 100 recent candidates before time filtering, joined
   * against the events table's `wake.boot_started` rows for the
   * per-row telemetry (Trigger, QueuedCount, ConcurrencyAtAdmit,
   * AtCapacity, ReadyInMS). The aggregation math is shared with
   * the HTML page:
   *
   * - 24h cutoff descending-break: the moment a row's
   * `started_at` falls before the trailing-24h instant, the
   * loop breaks (no further iteration). Older
   * rows with no `started_at` are not eligible for the break
   * (always in scope).
   * - Two-denominator rule for `at_capacity_pct`: numerator is
   * the count of rows where the events join succeeded AND
   * the at_capacity flag is true; denominator is the count
   * of rows where the events join succeeded
   * (`wake_count_with_meta`). Older rows contribute
   * to `wake_count_24h` but not the denominator — same
   * posture as the HTML page.
   *
   * Opt-in keyset_v1 filters app and inclusive since/until before limiting
   * and seeking in descending (started_at, instance ID) order. Missing
   * start times are excluded. Bounds normalize to PostgreSQL microseconds;
   * default until resolves once to now and must not be in the future.
   * since must precede until. Repeat the original bounds/limit on every
   * page. Boot and completion metadata are app-scoped. Source failures
   * return errors, not successful empty/partial results. Page metadata
   * echoes selection and cursor; counts/histograms describe this page
   * only. Complete marks the end of this live retained read, not complete
   * capture or a snapshot. Inserts, retention and metadata can affect pages.
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
    pagination,
    limit = 20,
    cursor,
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
    /**
     * Opt in to window-first keyset paging. Legacy reads select 100 recent instance candidates before time filtering.
     */
    pagination?: 'keyset_v1',
    /**
     * Page size in keyset_v1 mode only; legacy requests ignore this parameter.
     */
    limit?: number,
    /**
     * Opaque page.next_cursor. Repeat original normalized since/until and limit with keyset_v1. Bound to account and immutable app; grants no authority.
     */
    cursor?: string,
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
        'pagination': pagination,
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
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
   * `now` snapped down, `since` defaults to `until - 30d`. The
   * handler clamps `since` to `until - 90d` so a customer cannot
   * unbounded-scan `usage_minutes` (retention is 30d; the
   * 90d ceiling is a forward-compatibility ceiling for when
   * `usage_daily` lands).
   *
   * Overage computation: `overage_gb_hours = max(0, gb_hours -
   * plan_included_gb_hours)`. The included band is echoed from
   * `acct.Plan.PlanIncludedGBHours`; the overage figure is the
   * integer-rounded float the Stripe pusher bills at $0.01/GB-h.
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
   * Per-app streaming classification probe.
   * Returns the streaming-status enum (one of `streaming`,
   * `accept-json-downgrade`, `flag-disabled`, `plan-disallows`,
   * `operator-disabled`, `upgrade-bypass`) the gateway handler
   * would stamp on the `Streaming-Status` response header for a
   * representative request to this app, plus the effective
   * response-body cap (in bytes) and the per-gate flags.
   *
   * With no query parameters the probe is a pure read against the
   * API cache (the per-account `Plan` and the per-app
   * `streaming_enabled` flag). Supplying `host`, `path`, and `method`
   * together performs a bounded loopback read of the gateway's compiled
   * kind=limit rules and reports a matching streaming response cap with
   * `cap_kind="endpoint-rule"`. If the gateway is unavailable or no rule
   * matches, the response falls back to the plan cap.
   *
   * The operator opt-in (`FAAS_GATEWAY_STREAMING` env) remains
   * gateway-side state, so the canonical signal is the
   * `Streaming-Status` response header on a real request, not this probe.
   * A customer evaluating "will my next request stream?" must
   * consider the operator-side flag separately.
   *
   * `status=plan-disallows` means the customer's plan tier
   * forbids `streaming_enabled=true`; the CreateApp gate
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
   * Observed debugger signal coverage.
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
   * Active regression observations.
   * Returns regression observations written by the
   * debug_regression_observations table — surfaces per-route
   * p95 regressions detected by the regression cron. Ordered by
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
        503: `code: debug_regressions_unavailable — the debugger regression table or read query is unavailable; check database migrations and readiness, then retry.`,
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
   * Use the dedicated checked actions endpoint for mandatory app/version
   * guards. Legacy PATCH clients retain unguarded workflow transitions.
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
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: debug_regressions_unavailable — the debugger regression table or read query is unavailable; check database migrations and readiness, then retry.`,
      },
    });
  }
  /**
   * Apply a checked regression triage action.
   * Atomically requires the exact app identity and observation version from
   * the review endpoint. A changed or missing observation returns 409
   * regression_changed. Requires app write permissions, MFA and debugger
   * entitlement. Records the review reason in native audit metadata. Retry
   * with the exact same body after an ambiguous response to recover the
   * original receipt. The observation update, reason and receipt commit in
   * one database transaction. Operation identity binds the complete checked
   * body and account, independently of the caller's Idempotency-Key header.
   * Native replay uses the existing 24-hour cache retention. This dedicated route fails closed on
   * older API nodes; it never falls back to the legacy unguarded PATCH.
   * Changes triage metadata only, without changing traffic or proving health.
   *
   * @returns DebugRegressionActionResponse Checked triage receipt and updated observation.
   * @throws ApiError
   */
  public static applyAppDebugRegressionAction({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: GuardedDebugRegressionActionRequest,
  }): CancelablePromise<DebugRegressionActionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/debug/regressions/actions',
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
        503: `code: debug_regressions_unavailable — the debugger regression table or read query is unavailable; check database migrations and readiness, then retry.`,
      },
    });
  }
  /**
   * Review one exact regression workflow observation.
   * Includes acknowledged, dismissed and resolved observations without inbox
   * visibility filters. The opaque version changes on every database update,
   * including detector refreshes and legacy triage actions. Supply app_id as
   * expected_app_id and version as expected_version on the dedicated checked actions endpoint.
   * Requires ordinary app read permissions, MFA and debugger entitlement.
   * Read-only; no collection, traffic changes or health assessment.
   *
   * @returns DebugRegressionReview Exact versioned workflow observation.
   * @throws ApiError
   */
  public static getAppDebugRegressionReview({
    slug,
    deploymentId,
    route,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Exact retained observation deployment identity.
     */
    deploymentId: string,
    /**
     * Exact stored route label, including method when present.
     */
    route: string,
  }): CancelablePromise<DebugRegressionReview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/debug/regressions/review',
      path: {
        'slug': slug,
      },
      query: {
        'deployment_id': deploymentId,
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
        503: `code: debug_regressions_unavailable — the debugger regression table or read query is unavailable; check database migrations and readiness, then retry.`,
      },
    });
  }
  /**
   * Resolve a connected GitHub branch or commit and review target release configuration without building.
   * @returns SourceReviewResponse Immutable source and configuration review, excluding credentials.
   * @throws ApiError
   */
  public static reviewSourceRef({
    slug,
    requestBody,
  }: {
    /**
     * Application slug for Git source resolution.
     */
    slug: string,
    requestBody: SourceReviewRequest,
  }): CancelablePromise<SourceReviewResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/source-review',
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
   * Read an app's service wake-ahead opt-in.
   * Service wake-ahead is off by default. When it is on and this
   * app starts a cold wake, the gateway also starts restoring the services
   * it has measured this app calling soon after it wakes, so their restores
   * overlap instead of running one after another. Measurement is per
   * gateway and needs at least 20 observed wakes of this app before any
   * service is woken ahead. Woken services are billed like any other
   * running instance. Requires app read access and completed MFA.
   *
   * @returns ServiceWakeAheadResponse The app's wake-ahead opt-in.
   * @throws ApiError
   */
  public static getServiceWakeAhead({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ServiceWakeAheadResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/service-wake-ahead',
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
   * Turn an app's service wake-ahead on or off.
   * Sets the opt-in. Gateways apply the change within 30 seconds.
   * Wake-ahead never starts while fleet residency is at or above 60% of the
   * admission ceiling, never parks other instances to make room, and goes
   * through the same admission and plan concurrency limits as a request.
   * Requires deploy write access and completed MFA. Body limit is 1 KiB.
   *
   * @returns ServiceWakeAheadResponse The updated opt-in.
   * @throws ApiError
   */
  public static setServiceWakeAhead({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: SetServiceWakeAheadRequest,
  }): CancelablePromise<ServiceWakeAheadResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/service-wake-ahead',
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
   * Manually park all running instances.
   * @returns void
   * @throws ApiError
   */
  public static parkApp({
    slug,
    fresh = false,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * For isolated previews, invalidate snapshots after drain so the next request cold-boots from the artifact. Production apps reject fresh parking.
     */
    fresh?: boolean,
    requestBody?: {
      /**
       * The deployment UUID to compare atomically before parking. A stale or absent current deployment returns 409 without parking; reuse this UUID on drain retries.
       */
      expected_deployment_id?: string;
    },
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
   * Park instances only if the latest deployment matches.
   * Unsupported control planes reject this route without parking. Clients must never fall back to unconditional parking.
   * @returns void
   * @throws ApiError
   */
  public static parkAppIfDeployment({
    slug,
    requestBody,
    fresh = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: {
      /**
       * Compare the latest app deployment atomically with parking. A changed or missing deployment returns 409 without parking; resend the same guard on drain retries.
       */
      expected_deployment_id: string;
    },
    /**
     * For an isolated preview, invalidate its snapshots after all instances drain so the next request cold-boots from the artifact. Production apps reject this option.
     */
    fresh?: boolean,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/park/conditional',
      path: {
        'slug': slug,
      },
      query: {
        'fresh': fresh,
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
   * Persists a temporary prewarm intent. The scheduler claims it during the
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
   * scheduler returns NotFound (parked app), or the connection closes.
   *
   * - **Archive (`?archive=1`)** — fetches a single day's
   * per-instance log batch from the S3 bucket the API shipper
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
     * Exact match on the structured `level` field (info, warn, or error) surfaced in each matching log event. Empty = no level filter. The CLI and the API handler both validate against the same enum (api.IsValidLogLevel); an unknown value short-circuits with an SSE error frame carrying code invalid_level.
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
   * One call replaces N per-app `/v1/apps/{slug}/metrics` calls. Returns the same `AppMetricsResponse` shape
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
   * for the naive per-app loop)
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
   * auth and rate-limit share the per-app budget with
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
    pagination,
    cursor,
  }: {
    /**
     * App slug (lowercase, kebab-case; per-account unique).
     */
    slug: string,
    /**
     * The per-wake correlation handle minted by the scheduler
     * engine (UUID v4 in production). The endpoint returns
     * every `wake.*` events row whose `data.wake_id`
     * matches — the partial index `events_wake_id_idx` serves the read in O(frames)
     * regardless of the events table size.
     *
     */
    wakeId: string,
    /**
     * Exclusive at > since lower bound (RFC3339). Omission reads from retained history. keyset_v1 normalizes to UTC/PostgreSQL microseconds.
     */
    since?: string,
    /**
     * Max frames to return. Values outside 1..1000 are rejected.
     */
    limit?: number,
    /**
     * Opt in to app-scoped timestamp/event-ID keyset pagination. Omit for the unchanged legacy timestamp-hint contract.
     */
    pagination?: 'keyset_v1',
    /**
     * Opaque page.next_cursor from keyset_v1. Requires pagination=keyset_v1 and the same account, immutable app, wake, normalized since and limit. Selection data, not authorization; pages are live retained reads, not a snapshot.
     */
    cursor?: string,
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
        'pagination': pagination,
        'cursor': cursor,
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
   * Explain blockers and recovery for an app verification receipt
   * @returns BindingVerificationRunDiagnostics App receipt diagnostics. Read-only public observations and suggested recovery; grants no admission or activation authority.
   * @throws ApiError
   */
  public static getBindingVerificationRunDiagnostics({
    slug,
    run,
  }: {
    /**
     * Slug of the app for diagnosing an app binding verification receipt.
     */
    slug: string,
    /**
     * Verification receipt UUID for diagnosing an app binding verification receipt.
     */
    run: string,
  }): CancelablePromise<BindingVerificationRunDiagnostics> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/bindings/verification-runs/{run}/diagnostics',
      path: {
        'slug': slug,
        'run': run,
      },
      errors: {
        400: `App receipt diagnostics. Invalid receipt UUID.`,
        403: `App receipt diagnostics. Read access to every reviewed binding domain is required.`,
        404: `App receipt diagnostics. No owned receipt was found.`,
        503: `App receipt diagnostics. Receipt metadata could not be read.`,
      },
    });
  }
  /**
   * Review app binding verification selection and consent without admitting work
   * @returns BindingVerificationReview App verification review. Server plan, blockers, required consent and opaque review digests. Ready means the plan is available; it is not verification evidence.
   * @throws ApiError
   */
  public static reviewBindingVerification({
    slug,
    requestBody,
  }: {
    /**
     * Slug of the app for reviewing an app binding plan before admitting work.
     */
    slug: string,
    requestBody: ReviewBindingVerificationRequest,
  }): CancelablePromise<BindingVerificationReview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/bindings/verification-runs/review',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        403: `App verification review. Read access to every reviewed binding domain is required.`,
        409: `App verification review. The selected deployment, policy or prepared manifest changed.`,
        503: `App verification review. A complete review could not be read.`,
      },
    });
  }
  /**
   * Atomically admit one frozen step or return its previously recorded resource.
   * @returns BindingVerificationRunStep Durable receipt with original identity and deadline.
   * @throws ApiError
   */
  public static admitBindingVerificationRunStep({
    slug,
    run,
    step,
  }: {
    /**
     * Slug of the app for admitting one frozen app verification step.
     */
    slug: string,
    /**
     * Verification receipt UUID for admitting one frozen app verification step.
     */
    run: string,
    /**
     * Frozen step index selected for admission within this app receipt.
     */
    step: number,
  }): CancelablePromise<BindingVerificationRunStep> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/bindings/verification-runs/{run}/steps/{step}/admit',
      path: {
        'slug': slug,
        'run': run,
        'step': step,
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
      },
    });
  }
  /**
   * Complete only after fresh server-side policy preflight.
   * @returns BindingVerificationRun App receipt completion. Durable receipt with original identity and deadline.
   * @throws ApiError
   */
  public static completeBindingVerificationRun({
    slug,
    run,
  }: {
    /**
     * Slug of the app for finalizing an app verification receipt.
     */
    slug: string,
    /**
     * Verification receipt UUID for finalizing an app verification receipt.
     */
    run: string,
  }): CancelablePromise<BindingVerificationRun> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/bindings/verification-runs/{run}/complete',
      path: {
        'slug': slug,
        'run': run,
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
      },
    });
  }
  /**
   * Confirm the original review without extending deadlines or replacing work.
   * @returns BindingVerificationRun App receipt reconciliation. Durable receipt with original identity and deadline.
   * @throws ApiError
   */
  public static resumeBindingVerificationRun({
    slug,
    run,
  }: {
    /**
     * Slug of the app for reconciling unfinished app verification work.
     */
    slug: string,
    /**
     * Verification receipt UUID for reconciling unfinished app verification work.
     */
    run: string,
  }): CancelablePromise<BindingVerificationRun> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/bindings/verification-runs/{run}/resume',
      path: {
        'slug': slug,
        'run': run,
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
      },
    });
  }
  /**
   * Observe the original plan and current owned or reused resource status.
   * @returns BindingVerificationRun App receipt status. Durable receipt with original identity and deadline.
   * @throws ApiError
   */
  public static getBindingVerificationRun({
    slug,
    run,
  }: {
    /**
     * Slug of the app for inspecting or cancelling an app verification receipt.
     */
    slug: string,
    /**
     * Verification receipt UUID for inspecting or cancelling an app verification receipt.
     */
    run: string,
  }): CancelablePromise<BindingVerificationRun> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/bindings/verification-runs/{run}',
      path: {
        'slug': slug,
        'run': run,
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
      },
    });
  }
  /**
   * Block admissions and cancel only unfinished work owned by this run.
   * @returns BindingVerificationRun App receipt cancellation. Durable receipt with original identity and deadline.
   * @throws ApiError
   */
  public static cancelBindingVerificationRun({
    slug,
    run,
  }: {
    /**
     * Slug of the app for inspecting or cancelling an app verification receipt.
     */
    slug: string,
    /**
     * Verification receipt UUID for inspecting or cancelling an app verification receipt.
     */
    run: string,
  }): CancelablePromise<BindingVerificationRun> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/bindings/verification-runs/{run}',
      path: {
        'slug': slug,
        'run': run,
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
      },
    });
  }
  /**
   * List retained app binding verification receipt summaries
   * App receipt discovery. Newest first with creation-time and UUID keyset pagination. Reuse filters with next_cursor. observed_at freezes the expiry cutoff; recorded state may change between pages. Listings grant no admission or release authority.
   * @returns BindingVerificationRunList App receipt discovery. Bounded retained receipt summaries; not a permanent history or proof of current qualification.
   * @throws ApiError
   */
  public static listBindingVerificationRuns({
    slug,
    limit = 20,
    cursor,
    deploymentId,
    status,
    scope,
    includeMembers = false,
  }: {
    /**
     * Slug of the app for discovering or creating app verification receipts.
     */
    slug: string,
    /**
     * Maximum retained app verification receipts returned on this page.
     */
    limit?: number,
    /**
     * Opaque continuation cursor from the previous app verification receipt page.
     */
    cursor?: string,
    /**
     * Restrict retained app verification receipts to this exact deployment UUID.
     */
    deploymentId?: string,
    /**
     * Restrict app verification receipt discovery to this effective retained status.
     */
    status?: 'pending' | 'passed' | 'cancelled' | 'expired',
    /**
     * Only app receipts whose frozen binding scope matches this value are returned.
     */
    scope?: string,
    /**
     * Include app receipts created as members of project verification graphs.
     */
    includeMembers?: boolean,
  }): CancelablePromise<BindingVerificationRunList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/bindings/verification-runs',
      path: {
        'slug': slug,
      },
      query: {
        'limit': limit,
        'cursor': cursor,
        'deployment_id': deploymentId,
        'status': status,
        'scope': scope,
        'include_members': includeMembers,
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
      },
    });
  }
  /**
   * Create a reviewed fixed-deadline single-app verification receipt.
   * @returns BindingVerificationRun App receipt creation. Durable receipt with original identity and deadline.
   * @throws ApiError
   */
  public static createBindingVerificationRun({
    slug,
    requestBody,
  }: {
    /**
     * Slug of the app for discovering or creating app verification receipts.
     */
    slug: string,
    requestBody: CreateBindingVerificationRunRequest,
  }): CancelablePromise<BindingVerificationRun> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/bindings/verification-runs',
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
      },
    });
  }
}
