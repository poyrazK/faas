/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FeatureFlagVersion } from '../models/FeatureFlagVersion.js';
import type { FlagDecision } from '../models/FlagDecision.js';
import type { FlagEvidencePage } from '../models/FlagEvidencePage.js';
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
    return __request(OpenAPI, {
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
    return __request(OpenAPI, {
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
    return __request(OpenAPI, {
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
    return __request(OpenAPI, {
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
    return __request(OpenAPI, {
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
    return __request(OpenAPI, {
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
    return __request(OpenAPI, {
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
    return __request(OpenAPI, {
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Read the live workload’s project-environment flag bundle.
   * Requires an RS256 workload identity token with audience gregale:flags from an active app instance. Project and environment are derived from the deployment; query parameters cannot select another scope.
   * @returns FlagsBundle Runtime configuration for the authenticated workload environment.
   * @throws ApiError
   */
  public static getRuntimeFlags(): CancelablePromise<FlagsBundle> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/runtime/flags',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
