/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateEdgeRuleRequest } from '../models/CreateEdgeRuleRequest.js';
import type { EdgeRuleResponse } from '../models/EdgeRuleResponse.js';
import type { ThrottleSuggestionsResponse } from '../models/ThrottleSuggestionsResponse.js';
import type { UpdateEdgeRuleRequest } from '../models/UpdateEdgeRuleRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class EdgeRulesService {
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
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateEdgeRuleRequest,
  }): CancelablePromise<EdgeRuleResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/edge-rules',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Plan-kind or per-app quota rejected the rule.`,
        404: `code: not_found`,
        409: `code: edge_rule_conflict — duplicate or overlapping rule state rejected.`,
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
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: UpdateEdgeRuleRequest,
  }): CancelablePromise<EdgeRuleResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/edge-rules/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
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
   * Delete an edge rule.
   * @returns void
   * @throws ApiError
   */
  public static deleteEdgeRule({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
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
