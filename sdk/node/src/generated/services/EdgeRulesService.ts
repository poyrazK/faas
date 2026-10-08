/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateEdgeRuleRequest } from '../models/CreateEdgeRuleRequest.js';
import type { DeploymentRoutePolicySnapshotResponse } from '../models/DeploymentRoutePolicySnapshotResponse.js';
import type { EdgeRuleResponse } from '../models/EdgeRuleResponse.js';
import type { EdgeRuleSetVersionResponse } from '../models/EdgeRuleSetVersionResponse.js';
import type { RollbackEdgeRulesRequest } from '../models/RollbackEdgeRulesRequest.js';
import type { ThrottleSuggestionsResponse } from '../models/ThrottleSuggestionsResponse.js';
import type { UpdateEdgeRuleRequest } from '../models/UpdateEdgeRuleRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class EdgeRulesService {
  /**
   * Read the gateway policy captured with a deployment.
   * Returns the immutable edge-rule set captured atomically when this
   * deployment first became live. Rules use the same owner-scoped shape as
   * the current app edge-rule endpoint. Older deployments without a
   * snapshot return 404 so callers can report historical policy as unknown.
   *
   * @returns DeploymentRoutePolicySnapshotResponse Captured gateway route policy and fingerprint.
   * @throws ApiError
   */
  public static getDeploymentRoutePolicySnapshot({
    slug,
    deployment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * UUID of the deployment whose gateway policy snapshot is requested.
     */
    deployment: string,
  }): CancelablePromise<DeploymentRoutePolicySnapshotResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployments/{deployment}/route-policy',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      errors: {
        401: `code: unauthorized`,
        404: `Deployment is not owned by the caller or has no captured route policy.`,
      },
    });
  }
  /**
   * List every edge rule owned by the caller.
   * Account-wide listing. The dashboard overview pane uses this;
   * the CLI uses it for `gregale edge-rules list`. Free plans only
   * see rule kinds their plan unlocks — the server still lists
   * them.
   *
   * @returns EdgeRuleResponse Every edge rule owned by the caller.
   * @throws ApiError
   */
  public static listEdgeRules(): CancelablePromise<Array<EdgeRuleResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/edge-rules',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List every edge rule bound to one app.
   * @returns EdgeRuleResponse Edge rules for this app, ordered by priority ASC.
   * @throws ApiError
   */
  public static listEdgeRulesForApp({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<EdgeRuleResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Create an edge rule on an app.
   * Kind is one of {route, rewrite, redirect, headers, cors, jwt, ip,
   * validate, geo, async}. `action` is a kind-tagged jsonb body — the per-kind
   * shape is documented under components/schemas. Plan-kind gate:
   * jwt/ip return 402 plan_edge_rule_kind_not_allowed on Free; geo
   * is allowed on Free with a tighter per-app quota. Per-app
   * quota returns 402 plan_limit_edge_rules once EdgeRulesPerApp
   * is reached.
   *
   * @returns EdgeRuleResponse Rule created.
   * @throws ApiError
   */
  public static createEdgeRule({
    slug,
    requestBody,
    ifMatch,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateEdgeRuleRequest,
    /**
     * Optional edge-rule set version (the ETag of the app's rule listing). The mutation is refused with 412 when it is no longer the app's latest version.
     */
    ifMatch?: string,
  }): CancelablePromise<EdgeRuleResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/edge-rules',
      path: {
        'slug': slug,
      },
      headers: {
        'If-Match': ifMatch,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Plan-kind or per-app quota rejected the rule.`,
        404: `code: not_found`,
        409: `code: edge_rule_conflict — duplicate or overlapping rule state rejected.`,
        412: `If-Match no longer names the app's latest edge-rule set version (edge_rules_version_mismatch). The response ETag carries the current version.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Fetch one edge rule by id.
   * @returns EdgeRuleResponse The edge rule.
   * @throws ApiError
   */
  public static getEdgeRule({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<EdgeRuleResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/edge-rules/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Partial-update an edge rule.
   * Every field is optional. `kind` is NOT patchable — rotating
   * kind mid-life would break the action union. To change kind,
   * delete and recreate. `action` replaces the jsonb column
   * whole (no partial-update shape).
   *
   * @returns EdgeRuleResponse Updated rule.
   * @throws ApiError
   */
  public static updateEdgeRule({
    id,
    requestBody,
    ifMatch,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: UpdateEdgeRuleRequest,
    /**
     * Optional edge-rule set version (the ETag of the app's rule listing). The mutation is refused with 412 when it is no longer the app's latest version.
     */
    ifMatch?: string,
  }): CancelablePromise<EdgeRuleResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/edge-rules/{id}',
      path: {
        'id': id,
      },
      headers: {
        'If-Match': ifMatch,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        412: `If-Match no longer names the app's latest edge-rule set version (edge_rules_version_mismatch). The response ETag carries the current version.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Delete an edge rule.
   * @returns void
   * @throws ApiError
   */
  public static deleteEdgeRule({
    id,
    ifMatch,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Optional edge-rule set version (the ETag of the app's rule listing). The mutation is refused with 412 when it is no longer the app's latest version.
     */
    ifMatch?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/edge-rules/{id}',
      path: {
        'id': id,
      },
      headers: {
        'If-Match': ifMatch,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        412: `If-Match no longer names the app's latest edge-rule set version (edge_rules_version_mismatch). The response ETag carries the current version.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List recorded versions of an app's edge-rule set, newest first.
   * Every committed change to an app's edge rules records the whole rule
   * set as a new version (ADR-732). Up to the 50 newest versions are
   * returned, without rule bodies; the newest 100 are retained.
   *
   * @returns EdgeRuleSetVersionResponse Versions, newest first.
   * @throws ApiError
   */
  public static listEdgeRuleSetVersions({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<EdgeRuleSetVersionResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules/versions',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Fetch one recorded edge-rule set version with its rules.
   * @returns EdgeRuleSetVersionResponse The version and the rules it recorded.
   * @throws ApiError
   */
  public static getEdgeRuleSetVersion({
    slug,
    version,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    version: number,
  }): CancelablePromise<EdgeRuleSetVersionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules/versions/{version}',
      path: {
        'slug': slug,
        'version': version,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Restore an app's edge rules to a recorded version.
   * Replaces every current rule with the version's rules in one
   * transaction, preserving rule IDs, behind fleet convergence. The
   * restore is recorded as a new version. Refused when the version
   * exceeds the current plan's edge-rule quotas or references a deleted
   * CORS preset.
   *
   * @returns EdgeRuleResponse The rules now in force.
   * @throws ApiError
   */
  public static rollbackEdgeRules({
    slug,
    requestBody,
    ifMatch,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: RollbackEdgeRulesRequest,
    /**
     * Optional edge-rule set version (the ETag of the app's rule listing). The mutation is refused with 412 when it is no longer the app's latest version.
     */
    ifMatch?: string,
  }): CancelablePromise<Array<EdgeRuleResponse>> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/edge-rules/rollback',
      path: {
        'slug': slug,
      },
      headers: {
        'If-Match': ifMatch,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `The version references a CORS preset that no longer exists.`,
        412: `If-Match no longer names the app's latest edge-rule set version (edge_rules_version_mismatch). The response ETag carries the current version.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Per-route throttle recommender (ADR-091 D20.5 amendment,
   * issue #881). Read-only: returns a suggested rps/burst per
   * route over the window, clamped to the customer's plan
   * ceiling so the suggestion is always settable.
   *
   * The recommender is ADVICE-ONLY — it never auto-applies.
   * Customers confirm via POST /v1/apps/{slug}/edge-rules.
   *
   * @returns ThrottleSuggestionsResponse Suggestion payload. Source is `prometheus` on success
   * or `degraded: <reason>` on Prometheus failure
   * (response is still 200 with empty Suggestions — the
   * dashboard's empty-state branch handles it).
   *
   * @throws ApiError
   */
  public static getAppThrottleSuggestions({
    slug,
    range = '5m',
  }: {
    /**
     * App slug (lowercase, kebab-case; per-account unique). The recommender walks the per-route rate() for this app's gatewayd-internal reader.
     */
    slug: string,
    /**
     * Prometheus window. Closed vocabulary (see
     * `pkg/appmetrics.Ranges`). Defaults to 5m.
     *
     */
    range?: string,
  }): CancelablePromise<ThrottleSuggestionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/throttle-suggestions',
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
