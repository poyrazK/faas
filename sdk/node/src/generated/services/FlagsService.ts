/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FeatureFlagVersion } from '../models/FeatureFlagVersion.js';
import type { FlagDecision } from '../models/FlagDecision.js';
import type { FlagEventsRequest } from '../models/FlagEventsRequest.js';
import type { FlagEventsResponse } from '../models/FlagEventsResponse.js';
import type { FlagEvidencePage } from '../models/FlagEvidencePage.js';
import type { FlagExperiment } from '../models/FlagExperiment.js';
import type { FlagExperimentCreateRequest } from '../models/FlagExperimentCreateRequest.js';
import type { FlagExperimentList } from '../models/FlagExperimentList.js';
import type { FlagExperimentResults } from '../models/FlagExperimentResults.js';
import type { FlagLifecycleReport } from '../models/FlagLifecycleReport.js';
import type { FlagOutcomesResponse } from '../models/FlagOutcomesResponse.js';
import type { FlagRolloutPromotion } from '../models/FlagRolloutPromotion.js';
import type { FlagRolloutPromotionRequest } from '../models/FlagRolloutPromotionRequest.js';
import type { FlagsBundle } from '../models/FlagsBundle.js';
import type { InspectFeatureFlagRequest } from '../models/InspectFeatureFlagRequest.js';
import type { RollbackFeatureFlagsRequest } from '../models/RollbackFeatureFlagsRequest.js';
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
   * Read a flag experiment's sequential results.
   * Each variant is compared with control by an always-valid mixture sequential probability ratio test, so results may be read at any time. A sample ratio mismatch against the rule's weights is reported first.
   * @returns FlagExperimentResults Per-variant conversion, lift, confidence sequence and status.
   * @throws ApiError
   */
  public static getProjectFlagExperimentResults({
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
  }): CancelablePromise<FlagExperimentResults> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/projects/{slug}/environments/{environment}/experiments/{key}/results',
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
     * Comma-separated optional runtime SDK capabilities. SDKs that report events advertise track-v1.
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
