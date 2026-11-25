/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FeatureFlagKillResponse } from '../models/FeatureFlagKillResponse.js';
import type { FeatureFlagVersion } from '../models/FeatureFlagVersion.js';
import type { FlagDecision } from '../models/FlagDecision.js';
import type { FlagEventsRequest } from '../models/FlagEventsRequest.js';
import type { FlagEventsResponse } from '../models/FlagEventsResponse.js';
import type { FlagEvidencePage } from '../models/FlagEvidencePage.js';
import type { FlagExperiment } from '../models/FlagExperiment.js';
import type { FlagExperimentCreateRequest } from '../models/FlagExperimentCreateRequest.js';
import type { FlagExperimentHoldout } from '../models/FlagExperimentHoldout.js';
import type { FlagExperimentHoldoutReport } from '../models/FlagExperimentHoldoutReport.js';
import type { FlagExperimentLayerList } from '../models/FlagExperimentLayerList.js';
import type { FlagExperimentLayerRequest } from '../models/FlagExperimentLayerRequest.js';
import type { FlagExperimentList } from '../models/FlagExperimentList.js';
import type { FlagExperimentResults } from '../models/FlagExperimentResults.js';
import type { FlagExperimentShipRequest } from '../models/FlagExperimentShipRequest.js';
import type { FlagExperimentShipResponse } from '../models/FlagExperimentShipResponse.js';
import type { FlagLifecycleReport } from '../models/FlagLifecycleReport.js';
import type { FlagNotificationSettings } from '../models/FlagNotificationSettings.js';
import type { FlagNotificationSettingsRequest } from '../models/FlagNotificationSettingsRequest.js';
import type { FlagOutcomesResponse } from '../models/FlagOutcomesResponse.js';
import type { FlagRolloutPromotion } from '../models/FlagRolloutPromotion.js';
import type { FlagRolloutPromotionRequest } from '../models/FlagRolloutPromotionRequest.js';
import type { FlagsBundle } from '../models/FlagsBundle.js';
import type { InsightCohort } from '../models/InsightCohort.js';
import type { InsightCohortList } from '../models/InsightCohortList.js';
import type { InsightCohortRequest } from '../models/InsightCohortRequest.js';
import type { InsightEventSeries } from '../models/InsightEventSeries.js';
import type { InsightFunnel } from '../models/InsightFunnel.js';
import type { InsightFunnelRequest } from '../models/InsightFunnelRequest.js';
import type { InsightRequests } from '../models/InsightRequests.js';
import type { InsightRetention } from '../models/InsightRetention.js';
import type { InsightRetentionRequest } from '../models/InsightRetentionRequest.js';
import type { InspectFeatureFlagRequest } from '../models/InspectFeatureFlagRequest.js';
import type { RollbackFeatureFlagsRequest } from '../models/RollbackFeatureFlagsRequest.js';
import type { SavedInsight } from '../models/SavedInsight.js';
import type { SavedInsightList } from '../models/SavedInsightList.js';
import type { SavedInsightRequest } from '../models/SavedInsightRequest.js';
import type { SavedInsightResult } from '../models/SavedInsightResult.js';
import type { UpdateFeatureFlagsRequest } from '../models/UpdateFeatureFlagsRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class FlagsService {
  protected static readonly clientConfig?: typeof OpenAPI;
  /**
   * Read current or historical feature flag configuration.
   * Requires operator enablement via FAAS_FLAGS_ENABLED. Account-scoped read credentials only; app deploy tokens cannot read project-wide targeting lists.
   * @returns FeatureFlagVersion Current or selected historical configuration publication.
   * @throws ApiError
   */
  public static getProjectFlags({
    slug,
    environment,
    version,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Historical version; omit for current.
     */
    version?: number,
  }): CancelablePromise<FeatureFlagVersion> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/flags',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'version': version,
      },
      errors: {
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
   * Publish an atomic feature flag configuration.
   * Requires deploy-write scope and MFA when configured. expected_version zero creates the first version; conflicting writes return 409. Customers must belong to the account. Allocation seeds are immutable.
   * @returns FeatureFlagVersion Newly committed configuration with its assigned version.
   * @throws ApiError
   */
  public static publishProjectFlags({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    requestBody: UpdateFeatureFlagsRequest,
  }): CancelablePromise<FeatureFlagVersion> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/projects/{slug}/environments/{environment}/flags',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
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
   * List up to 100 immutable configuration versions.
   * Returns publication audit metadata in descending order; use before_version to continue history.
   * @returns FeatureFlagVersion Configuration history in descending version order.
   * @throws ApiError
   */
  public static listProjectFlagVersions({
    slug,
    environment,
    beforeVersion,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Exclusive version boundary for older history pages.
     */
    beforeVersion?: number,
  }): CancelablePromise<Array<FeatureFlagVersion>> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/flags/versions',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'before_version': beforeVersion,
      },
      errors: {
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
   * Report each flag's owner, expiry, last change and use.
   * Flags whose attention list is not empty need a decision: expired, expires_soon (within 7 days), stale (serves one value and unchanged for 14 days), never_evaluated (enabled, no decision in 7 days of retained request telemetry) or no_owner. An hourly check also records one flags.lifecycle_attention audit event per flag, reason and configuration version.
   * @returns FlagLifecycleReport Lifecycle report for the current configuration.
   * @throws ApiError
   */
  public static getProjectFlagLifecycle({
    slug,
    environment,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
  }): CancelablePromise<FlagLifecycleReport> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/flags/lifecycle',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Read the channels an environment's flag notifications go to.
   * Experiment decisions (ship a variant, keep control) and problems (guardrail rollback, sample ratio mismatch) of experiments without their own channels, a weekly digest of flags that need a decision and flag expiries go to these notification channels. Needs the flag_notifications release.
   * @returns FlagNotificationSettings The environment's flag notification channels; empty when off.
   * @throws ApiError
   */
  public static getProjectFlagNotifications({
    slug,
    environment,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
  }): CancelablePromise<FlagNotificationSettings> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/flags/notifications',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Replace the channels an environment's flag notifications go to.
   * Up to 5 of the account's notification channels; an empty list turns the environment's flag notifications off. Queued notifications keep the channels they were queued with.
   * @returns FlagNotificationSettings The environment's new flag notification channels.
   * @throws ApiError
   */
  public static setProjectFlagNotifications({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    requestBody: FlagNotificationSettingsRequest,
  }): CancelablePromise<FlagNotificationSettings> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/projects/{slug}/environments/{environment}/flags/notifications',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | env_var_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Publish a previous flag configuration as a new version.
   * Requires the current expected_version and preserves allocation seeds; history remains immutable.
   * @returns FeatureFlagVersion New publication restored from the requested historical configuration.
   * @throws ApiError
   */
  public static rollbackProjectFlags({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    requestBody: RollbackFeatureFlagsRequest,
  }): CancelablePromise<FeatureFlagVersion> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/flags/rollback',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
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
   * Explain a current or historical customer decision without exposure.
   * Simulates ordered customer rules against the selected immutable configuration; this call records no application exposure.
   * @returns FlagDecision Evaluation explanation for the supplied customer context.
   * @throws ApiError
   */
  public static inspectProjectFlag({
    slug,
    environment,
    key,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Application flag key to evaluate or filter retained evidence.
     */
    key: string,
    requestBody: InspectFeatureFlagRequest,
  }): CancelablePromise<FlagDecision> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/flags/{key}/inspect',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
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
   * Inspect retained requests with application-reported flag evidence.
   * Debugger entitlement and retention apply. Counts weight collapsed telemetry rows; rows with different decisions remain separate. These are operational observations, not exactly-once exposure counts or causal experiment results.
   * @returns FlagEvidencePage Retained request cohort page with stable pagination window.
   * @throws ApiError
   */
  public static listProjectFlagRequests({
    slug,
    environment,
    key,
    customerId,
    value,
    variant,
    used,
    since = '24h',
    cursor,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Application flag key to evaluate or filter retained evidence.
     */
    key: string,
    /**
     * Verified platform tenant UUID.
     */
    customerId?: string,
    /**
     * Filter the evaluated boolean.
     */
    value?: boolean,
    /**
     * Filter the selected named variant; mutually exclusive with value.
     */
    variant?: string,
    /**
     * Filter application-reported exposure.
     */
    used?: boolean,
    /**
     * Lookback clamped to plan retention.
     */
    since?: string,
    /**
     * Cursor from the preceding page; preserve filters.
     */
    cursor?: string,
  }): CancelablePromise<FlagEvidencePage> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/flags/{key}/requests',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      query: {
        'customer_id': customerId,
        'value': value,
        'variant': variant,
        'used': used,
        'since': since,
        'cursor': cursor,
      },
      errors: {
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
   * Compare retained request outcomes by evaluated flag value.
   * Debugger entitlement and retention apply. Counts weight collapsed telemetry rows; used is application-reported. Optional rule and configuration-version filters isolate a targeting decision. HTTP 5xx rates and request-weighted latency percentiles are operational observations, not causal experiment results. Latencies use conservative bucket upper bounds.
   * @returns FlagOutcomesResponse Bounded retained request outcomes grouped by decision type and value.
   * @throws ApiError
   */
  public static getProjectFlagOutcomes({
    slug,
    environment,
    key,
    customerId,
    ruleId,
    configVersion,
    since = '24h',
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Application flag key to evaluate or filter retained evidence.
     */
    key: string,
    /**
     * Restrict the rollup to one gateway-attributed customer UUID.
     */
    customerId?: string,
    /**
     * Restrict the rollup to decisions matched by one targeting rule.
     */
    ruleId?: string,
    /**
     * Restrict the rollup to one immutable flag configuration version.
     */
    configVersion?: number,
    /**
     * Aggregation duration capped by the account's debugger retention.
     */
    since?: string,
  }): CancelablePromise<FlagOutcomesResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/flags/{key}/outcomes',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      query: {
        'customer_id': customerId,
        'rule_id': ruleId,
        'config_version': configVersion,
        'since': since,
      },
      errors: {
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
   * Promote one progressive flag rollout stage when its health gates pass.
   * Requires deploy-write scope and MFA. Evidence is isolated to the target rule and its configured window. A held result is a successful evaluation with no configuration change; latency is the conservative upper bound of retained telemetry buckets.
   * @returns FlagRolloutPromotion Stage promotion, health hold, or completed rollout with evidence summary.
   * @throws ApiError
   */
  public static promoteProjectFlagRollout({
    slug,
    environment,
    key,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Application flag key to evaluate or filter retained evidence.
     */
    key: string,
    requestBody: FlagRolloutPromotionRequest,
  }): CancelablePromise<FlagRolloutPromotion> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/flags/{key}/rollout/promote',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
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
   * Kill one flag on the latest configuration.
   * Requires deploy-write scope and MFA. Sets the flag's kill on the latest published configuration, so every evaluation serves the application's fallback. Takes no expected version, re-reads the configuration when another publish wins the race, and is idempotent; changed is false when the flag was already killed. Unlike other flag writes, an agent connection needs no person's approval for it. Revive a flag by publishing the configuration.
   * @returns FeatureFlagKillResponse The flag is killed.
   * @throws ApiError
   */
  public static killProjectFlag({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Application flag key to evaluate or filter retained evidence.
     */
    key: string,
  }): CancelablePromise<FeatureFlagKillResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/flags/{key}/kill',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      errors: {
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
   * Read the live workload’s project-environment flag bundle.
   * Requires an RS256 workload identity token with audience gregale:flags from an active app instance. Project and environment are derived from the deployment; query parameters cannot select another scope. Clients advertise runtime evaluation capabilities; a bundle using a capability is withheld from older clients that do not advertise it.
   * @returns FlagsBundle Runtime configuration for the authenticated workload environment.
   * @throws ApiError
   */
  public static getRuntimeFlags({
    xFaasFlagsCapabilities,
  }: {
    /**
     * Comma-separated optional runtime SDK capabilities. Subject-aware SDKs advertise subject-targeting-v1.
     */
    xFaasFlagsCapabilities?: string,
  }): CancelablePromise<FlagsBundle> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/runtime/flags',
      headers: {
        'X-Faas-Flags-Capabilities': xFaasFlagsCapabilities,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        426: `Runtime SDK does not support a capability used by the current flag bundle.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List an environment's flag experiments.
   * Experiments compare the variants of a variant flag's weighted rule by the share of exposed customers that later send a conversion event.
   * @returns FlagExperimentList The environment's experiments, newest first.
   * @throws ApiError
   */
  public static listProjectFlagExperiments({
    slug,
    environment,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
  }): CancelablePromise<FlagExperimentList> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/experiments',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Create, and optionally start, a flag experiment.
   * The rule must be a weighted rule of an enabled variant flag that allocates by customer; control must be one of its variants. One experiment runs per rule; FlagExperimentsMaxRunning run per environment.
   * @returns FlagExperiment The created experiment, running when start was set.
   * @throws ApiError
   */
  public static createProjectFlagExperiment({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    requestBody: FlagExperimentCreateRequest,
  }): CancelablePromise<FlagExperiment> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/experiments',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Another experiment uses the key or already runs on the rule, or the environment is at its experiment limit.`,
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
   * Read one flag experiment.
   * @returns FlagExperiment The experiment.
   * @throws ApiError
   */
  public static getProjectFlagExperiment({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Experiment key, unique within the environment.
     */
    key: string,
  }): CancelablePromise<FlagExperiment> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/experiments/{key}',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
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
      },
    });
  }
  /**
   * Delete a flag experiment that is not running, with its units.
   * @returns void
   * @throws ApiError
   */
  public static deleteProjectFlagExperiment({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Experiment key, unique within the environment.
     */
    key: string,
  }): CancelablePromise<void> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/environments/{environment}/experiments/{key}',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `The experiment is running; stop it first.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Start a draft flag experiment.
   * @returns FlagExperiment The running experiment.
   * @throws ApiError
   */
  public static startProjectFlagExperiment({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Experiment key, unique within the environment.
     */
    key: string,
  }): CancelablePromise<FlagExperiment> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/experiments/{key}/start',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `Another experiment already runs on the rule, or the environment is at its running limit.`,
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
   * Stop a running flag experiment and freeze its results.
   * @returns FlagExperiment The stopped experiment.
   * @throws ApiError
   */
  public static stopProjectFlagExperiment({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Experiment key, unique within the environment.
     */
    key: string,
  }): CancelablePromise<FlagExperiment> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/experiments/{key}/stop',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
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
      },
    });
  }
  /**
   * Serve one variant to every customer of an experiment's rule.
   * Publishes the flag configuration with the experiment's rule serving the variant, and stops the experiment if it is still running. The previous configuration version can be restored.
   * @returns FlagExperimentShipResponse The experiment and the published configuration version.
   * @throws ApiError
   */
  public static shipProjectFlagExperiment({
    slug,
    environment,
    key,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Experiment key, unique within the environment.
     */
    key: string,
    requestBody: FlagExperimentShipRequest,
  }): CancelablePromise<FlagExperimentShipResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/experiments/{key}/ship',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | env_var_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read a flag experiment's sequential results.
   * Each variant is compared with control by an always-valid mixture sequential probability ratio test, so results may be read at any time. A sample ratio mismatch against the rule's weights is reported first. Ratio metrics use the delta method; percentile metrics a fixed-horizon bootstrap; bandits report each arm's best-arm probability and expected loss instead of p-values.
   * @returns FlagExperimentResults Per-variant conversion, lift, confidence sequence and status.
   * @throws ApiError
   */
  public static getProjectFlagExperimentResults({
    slug,
    environment,
    key,
    segment,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Experiment key, unique within the environment.
     */
    key: string,
    /**
     * Cohort key: split a binary customer experiment's results by the cohort's members.
     */
    segment?: string,
  }): CancelablePromise<FlagExperimentResults> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/experiments/{key}/results',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      query: {
        'segment': segment,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | env_var_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List the environment's experiment layers.
   * Each layer with the slices its running experiments hold and the basis points still free.
   * @returns FlagExperimentLayerList The environment's layers.
   * @throws ApiError
   */
  public static listProjectFlagExperimentLayers({
    slug,
    environment,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
  }): CancelablePromise<FlagExperimentLayerList> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/experiment-layers',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Create an experiment layer.
   * Publishes a flag configuration with the new layer and a random seed. At most 10 layers per environment (409 flag_experiment_layer_limit).
   * @returns FlagExperimentLayerList The environment's layers with the new one.
   * @throws ApiError
   */
  public static createProjectFlagExperimentLayer({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    requestBody: FlagExperimentLayerRequest,
  }): CancelablePromise<FlagExperimentLayerList> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/experiment-layers',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | env_var_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Delete an experiment layer.
   * Refused with 409 flag_experiment_running while a running experiment holds a slice of it.
   * @returns void
   * @throws ApiError
   */
  public static deleteProjectFlagExperimentLayer({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Key of the layer to delete.
     */
    key: string,
  }): CancelablePromise<void> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/environments/{environment}/experiment-layers/{key}',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
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
      },
    });
  }
  /**
   * Read the environment holdout.
   * The basis points of customers that see every running experiment's control.
   * @returns FlagExperimentHoldout The holdout.
   * @throws ApiError
   */
  public static getProjectFlagExperimentHoldout({
    slug,
    environment,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
  }): CancelablePromise<FlagExperimentHoldout> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/experiment-holdout',
      path: {
        'slug': slug,
        'environment': environment,
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
      },
    });
  }
  /**
   * Set the environment holdout.
   * 0 to 2000 basis points; 0 turns it off. Refused with 409 flag_experiment_running while an experiment runs, so no experiment's population changes under it.
   * @returns FlagExperimentHoldout The holdout as set.
   * @throws ApiError
   */
  public static setProjectFlagExperimentHoldout({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    requestBody: FlagExperimentHoldout,
  }): CancelablePromise<FlagExperimentHoldout> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/projects/{slug}/environments/{environment}/experiment-holdout',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | env_var_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Compare held-out customers with everyone else.
   * The cumulative effect of experimentation: the share of held-out and other active customers that sent the event in the range (default the last 30 days). Requires product analytics.
   * @returns FlagExperimentHoldoutReport The holdout report.
   * @throws ApiError
   */
  public static getProjectFlagExperimentHoldoutReport({
    slug,
    environment,
    event,
    from,
    until,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * The event whose senders count as converted.
     */
    event: string,
    /**
     * Report start (RFC 3339); defaults to 30 days ago.
     */
    from?: string,
    /**
     * Report end (RFC 3339); defaults to the request time.
     */
    until?: string,
  }): CancelablePromise<FlagExperimentHoldoutReport> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/experiment-holdout/report',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'event': event,
        'from': from,
        'until': until,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | env_var_invalid_key`,
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
   * Read a product analytics event series.
   * Counts, distinct units and summed value of one track() event per time bucket, optionally filtered by properties and broken down by a flag's served value or a property. Requires the product_analytics platform flag and a plan with analytics retention. The range defaults to the last 7 days and is bounded by the plan's analytics retention and AnalyticsQueryMaxDays.
   * @returns InsightEventSeries The event series.
   * @throws ApiError
   */
  public static getProjectInsightEvents({
    slug,
    environment,
    event,
    interval = 'day',
    unit = 'customer',
    by,
    where,
    from,
    until,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Event name.
     */
    event: string,
    /**
     * Bucket size.
     */
    interval?: 'hour' | 'day',
    /**
     * What Units counts.
     */
    unit?: 'customer' | 'subject',
    /**
     * Breakdown: flag:KEY or property:KEY.
     */
    by?: string,
    /**
     * Repeatable KEY:VALUE property filter.
     */
    where?: Array<string>,
    /**
     * Range start (RFC 3339).
     */
    from?: string,
    /**
     * Range end (RFC 3339); defaults to now.
     */
    until?: string,
  }): CancelablePromise<InsightEventSeries> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/insights/events',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'event': event,
        'interval': interval,
        'unit': unit,
        'by': by,
        'where': where,
        'from': from,
        'until': until,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | env_var_invalid_key`,
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
   * Compute a product analytics funnel.
   * Read-only. Units that did the steps in order, each step after the previous one, within the window from the first step. A flag breakdown uses the flag value carried by the unit's first-step event.
   * @returns InsightFunnel Units and conversion per step, overall and per breakdown value.
   * @throws ApiError
   */
  public static postProjectInsightFunnel({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    requestBody: InsightFunnelRequest,
  }): CancelablePromise<InsightFunnel> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/insights/funnel',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | env_var_invalid_key`,
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
   * Compute product analytics retention.
   * Read-only. Cohorts units by the period of their first start event and reports which sent the return event in each following period; only ended periods count. The range defaults to the last 28 days.
   * @returns InsightRetention Overall, per-cohort and per-breakdown retention.
   * @throws ApiError
   */
  public static postProjectInsightRetention({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    requestBody: InsightRetentionRequest,
  }): CancelablePromise<InsightRetention> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/insights/retention',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | env_var_invalid_key`,
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
   * Read request telemetry by flag value.
   * Requests, server errors and p95 latency per served value of one flag, from request telemetry and within its retention. The range defaults to the last day.
   * @returns InsightRequests Request telemetry per flag value.
   * @throws ApiError
   */
  public static getProjectInsightRequests({
    slug,
    environment,
    flag,
    from,
    until,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Flag key.
     */
    flag: string,
    /**
     * Start of the request telemetry range (RFC 3339); defaults to one day ago.
     */
    from?: string,
    /**
     * End of the request telemetry range (RFC 3339); defaults to now.
     */
    until?: string,
  }): CancelablePromise<InsightRequests> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/insights/requests',
      path: {
        'slug': slug,
        'environment': environment,
      },
      query: {
        'flag': flag,
        'from': from,
        'until': until,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | env_var_invalid_key`,
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
   * List the environment's saved insights.
   * Saved insights with their query, schedule, last scheduled value and alert state. Anyone who can read the project lists them. Requires the saved_insights and product_analytics platform flags.
   * @returns SavedInsightList The environment's saved insights.
   * @throws ApiError
   */
  public static listProjectSavedInsights({
    slug,
    environment,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
  }): CancelablePromise<SavedInsightList> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/insights/saved',
      path: {
        'slug': slug,
        'environment': environment,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Save an insight.
   * Saves an events, funnel or retention query over the last range_days days, with an optional daily or weekly digest and threshold alert sent to the account's notification channels. At most 50 per environment (409 saved_insight_limit).
   * @returns SavedInsight The created saved insight.
   * @throws ApiError
   */
  public static createProjectSavedInsight({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    requestBody: SavedInsightRequest,
  }): CancelablePromise<SavedInsight> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/insights/saved',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | env_var_invalid_key`,
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
   * Read one saved insight.
   * A saved insight's query, schedule, last scheduled value, alert state and last error.
   * @returns SavedInsight The saved insight.
   * @throws ApiError
   */
  public static getProjectSavedInsight({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Saved insight key.
     */
    key: string,
  }): CancelablePromise<SavedInsight> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/insights/saved/{key}',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Replace a saved insight.
   * Replaces the query and schedule. Changing the query, range or alert resets the alert state to ok.
   * @returns SavedInsight The replaced saved insight.
   * @throws ApiError
   */
  public static replaceProjectSavedInsight({
    slug,
    environment,
    key,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Key of the saved insight to replace.
     */
    key: string,
    requestBody: SavedInsightRequest,
  }): CancelablePromise<SavedInsight> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/projects/{slug}/environments/{environment}/insights/saved/{key}',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | env_var_invalid_key`,
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
   * Delete a saved insight.
   * Its digests and alert stop.
   * @returns void
   * @throws ApiError
   */
  public static deleteProjectSavedInsight({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Key of the saved insight to delete.
     */
    key: string,
  }): CancelablePromise<void> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/environments/{environment}/insights/saved/{key}',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Run a saved insight now.
   * Read-only. Runs the saved query over its last range_days days, clamped to the plan's analytics retention, and returns its value and result. format=csv returns a CSV attachment instead: one row per series point, funnel step or retention period.
   * @returns SavedInsightResult The saved insight's value and result.
   * @throws ApiError
   */
  public static runProjectSavedInsight({
    slug,
    environment,
    key,
    format,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Key of the saved insight to run.
     */
    key: string,
    /**
     * json (default) or csv.
     */
    format?: 'json' | 'csv',
  }): CancelablePromise<SavedInsightResult> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/insights/saved/{key}/run',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
      },
      query: {
        'format': format,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | env_var_invalid_key`,
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
   * List the environment's cohorts.
   * Saved cohorts with their definition, member count, the plan's member bound and the last refresh. Flag rules target a cohort with the gregale.cohort attribute. Requires product analytics.
   * @returns InsightCohortList The environment's cohorts.
   * @throws ApiError
   */
  public static listProjectInsightCohorts({
    slug,
    environment,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
  }): CancelablePromise<InsightCohortList> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/cohorts',
      path: {
        'slug': slug,
        'environment': environment,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Save a cohort.
   * Members are computed within a minute, then hourly. At most 20 cohorts per environment (409 analytics_cohort_limit); members per cohort are bounded by plan (Hobby 1,000, Pro 10,000, Scale 100,000).
   * @returns InsightCohort The saved cohort, before its first refresh.
   * @throws ApiError
   */
  public static createProjectInsightCohort({
    slug,
    environment,
    requestBody,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    requestBody: InsightCohortRequest,
  }): CancelablePromise<InsightCohort> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/cohorts',
      path: {
        'slug': slug,
        'environment': environment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | env_var_invalid_key`,
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
   * Read one cohort.
   * A cohort's definition, member count and last refresh; a failed refresh keeps the previous members and reports refresh_error.
   * @returns InsightCohort The cohort.
   * @throws ApiError
   */
  public static getProjectInsightCohort({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Cohort key.
     */
    key: string,
  }): CancelablePromise<InsightCohort> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/cohorts/{key}',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Delete a cohort.
   * Rules that target the cohort stop matching its customers within a minute.
   * @returns void
   * @throws ApiError
   */
  public static deleteProjectInsightCohort({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Key of the cohort to delete.
     */
    key: string,
  }): CancelablePromise<void> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/projects/{slug}/environments/{environment}/cohorts/{key}',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Recompute a cohort now.
   * Asks for the cohort's members to be recomputed on the next reconciler pass instead of at the hourly refresh.
   * @returns InsightCohort The refresh is requested.
   * @throws ApiError
   */
  public static refreshProjectInsightCohort({
    slug,
    environment,
    key,
  }: {
    /**
     * Project that owns this environment flag configuration.
     */
    slug: string,
    /**
     * Named environment whose flag configuration is selected.
     */
    environment: string,
    /**
     * Key of the cohort to recompute.
     */
    key: string,
  }): CancelablePromise<InsightCohort> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/projects/{slug}/environments/{environment}/cohorts/{key}/refresh',
      path: {
        'slug': slug,
        'environment': environment,
        'key': key,
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Report conversion events for flag experiments.
   * Requires the same workload identity as getRuntimeFlags. Account, app and environment come from the token. Each event names the verified customer of the request it belongs to; events for customers that are not active platform tenants of the account are dropped. Accepted events count against the plan's monthly event quota (FlagEventsPerMonth); events over quota are dropped and counted, never billed.
   * @returns FlagEventsResponse What happened to each event of the batch, and the month's usage.
   * @throws ApiError
   */
  public static postRuntimeFlagEvents({
    requestBody,
    xFaasFlagsCapabilities,
  }: {
    requestBody: FlagEventsRequest,
    /**
     * Comma-separated optional runtime SDK capabilities. SDKs that report events advertise track-v1; SDKs that send subject, properties and flags also advertise track-v2.
     */
    xFaasFlagsCapabilities?: string,
  }): CancelablePromise<FlagEventsResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/runtime/flags/events',
      headers: {
        'X-Faas-Flags-Capabilities': xFaasFlagsCapabilities,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
}
