/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateEdgeRuleListRequest } from '../models/CreateEdgeRuleListRequest.js';
import type { CreateEdgeRuleRequest } from '../models/CreateEdgeRuleRequest.js';
import type { DeploymentRoutePolicySnapshotResponse } from '../models/DeploymentRoutePolicySnapshotResponse.js';
import type { EdgeAttackModeStatus } from '../models/EdgeAttackModeStatus.js';
import type { EdgeRuleEventsResponse } from '../models/EdgeRuleEventsResponse.js';
import type { EdgeRuleListResponse } from '../models/EdgeRuleListResponse.js';
import type { EdgeRuleRecipeSuggestionsResponse } from '../models/EdgeRuleRecipeSuggestionsResponse.js';
import type { EdgeRuleResponse } from '../models/EdgeRuleResponse.js';
import type { EdgeRuleSetVersionResponse } from '../models/EdgeRuleSetVersionResponse.js';
import type { EdgeRuleStatsResponse } from '../models/EdgeRuleStatsResponse.js';
import type { EdgeWAFExclusionsRequest } from '../models/EdgeWAFExclusionsRequest.js';
import type { EdgeWAFReviewResponse } from '../models/EdgeWAFReviewResponse.js';
import type { EnableEdgeAttackModeRequest } from '../models/EnableEdgeAttackModeRequest.js';
import type { ListEdgeRuleListsResponse } from '../models/ListEdgeRuleListsResponse.js';
import type { RollbackEdgeRulesRequest } from '../models/RollbackEdgeRulesRequest.js';
import type { ThrottleSuggestionsResponse } from '../models/ThrottleSuggestionsResponse.js';
import type { UpdateEdgeRuleListRequest } from '../models/UpdateEdgeRuleListRequest.js';
import type { UpdateEdgeRuleRequest } from '../models/UpdateEdgeRuleRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class EdgeRulesService {
  /**
   * Read an app's attack mode.
   * @returns EdgeAttackModeStatus The app's attack mode.
   * @throws ApiError
   */
  public static getAppEdgeAttackMode({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<EdgeAttackModeStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-protection/attack-mode',
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
   * Switch attack mode on, or extend it.
   * Challenges every request to every host of the app until it ends. Available on every plan, outside the plan's
   * edge-rule quotas. The response returns once the serving gateways
   * have applied it; `X-Faas-Edge-Rules-State: converging` with 503 means it
   * was saved but not yet acknowledged. An empty body applies the
   * default duration of one hour.
   *
   * @returns EdgeAttackModeStatus Attack mode is on.
   * @throws ApiError
   */
  public static enableAppEdgeAttackMode({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody?: EnableEdgeAttackModeRequest,
  }): CancelablePromise<EdgeAttackModeStatus> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/edge-protection/attack-mode',
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
   * Switch attack mode off.
   * Removes the attack-mode rule. Succeeds when attack mode is already off.
   * @returns EdgeAttackModeStatus Attack mode is off.
   * @throws ApiError
   */
  public static disableAppEdgeAttackMode({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<EdgeAttackModeStatus> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/edge-protection/attack-mode',
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List the account's reusable edge-rule lists.
   * Items are omitted here; fetch one list for its items.
   * referenced_by names the rules whose match conditions use the list.
   *
   * @returns ListEdgeRuleListsResponse The account's lists, by name.
   * @throws ApiError
   */
  public static listEdgeRuleLists(): CancelablePromise<ListEdgeRuleListsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/edge-rule-lists',
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
   * Create a reusable edge-rule list.
   * Items are validated for the list kind and stored
   * canonicalized, deduplicated and sorted. Lists per account and items
   * per list are plan limits.
   *
   * @returns EdgeRuleListResponse The created list.
   * @throws ApiError
   */
  public static createEdgeRuleList({
    requestBody,
  }: {
    requestBody: CreateEdgeRuleListRequest,
  }): CancelablePromise<EdgeRuleListResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/edge-rule-lists',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_edge_rule_lists | plan_limit_edge_rule_list_items`,
        409: `code: edge_rule_list_exists`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Get one edge-rule list with its items.
   * @returns EdgeRuleListResponse The list.
   * @throws ApiError
   */
  public static getEdgeRuleList({
    name,
  }: {
    /**
     * Edge-rule list name, unique within the account.
     */
    name: string,
  }): CancelablePromise<EdgeRuleListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/edge-rule-lists/{name}',
      path: {
        'name': name,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: edge_rule_list_not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Edit an edge-rule list.
   * `items` replaces the list; `add` and `remove` edit it in place
   * (remove applies after add) and cannot be combined with `items`.
   * Gateways pick up the change for every referencing rule within a few
   * seconds; no rule-set version is recorded.
   *
   * @returns EdgeRuleListResponse The updated list.
   * @throws ApiError
   */
  public static updateEdgeRuleList({
    name,
    requestBody,
  }: {
    /**
     * Edge-rule list name, unique within the account.
     */
    name: string,
    requestBody: UpdateEdgeRuleListRequest,
  }): CancelablePromise<EdgeRuleListResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/edge-rule-lists/{name}',
      path: {
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_edge_rule_list_items`,
        404: `code: edge_rule_list_not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Delete an unreferenced edge-rule list.
   * @returns void
   * @throws ApiError
   */
  public static deleteEdgeRuleList({
    name,
  }: {
    /**
     * Edge-rule list name, unique within the account.
     */
    name: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/edge-rule-lists/{name}',
      path: {
        'name': name,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: edge_rule_list_not_found`,
        409: `code: edge_rule_list_in_use (rules still reference the list)`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Partial-update an edge rule.
   * Every field is optional. `kind` is NOT patchable — rotating
   * kind mid-life would break the action union. To change kind,
   * delete and recreate. `action` replaces the jsonb column
   * whole (no partial-update shape). The app's attack-mode rule
   * (`managed_by: attack_mode`) is refused with 409; change it through
   * `/v1/apps/{slug}/edge-protection/attack-mode`.
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
        409: `code: edge_rule_conflict — duplicate or overlapping rule state rejected.`,
        412: `If-Match no longer names the app's latest edge-rule set version (edge_rules_version_mismatch). The response ETag carries the current version.`,
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Add or remove CRS rule IDs on a kind=waf rule's exclusions.
   * Changes exclude_rule_ids and keeps the rest of
   * the rule's action and its penalty, so silencing a false positive does
   * not mean restating the whole action. Recorded as a new rule-set
   * version like any PATCH.
   *
   * @returns EdgeRuleResponse The updated rule.
   * @throws ApiError
   */
  public static updateEdgeWafExclusions({
    id,
    requestBody,
    ifMatch,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: EdgeWAFExclusionsRequest,
    /**
     * Optional edge-rule set version (the ETag of the app's rule listing). The mutation is refused with 412 when it is no longer the app's latest version.
     */
    ifMatch?: string,
  }): CancelablePromise<EdgeRuleResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/edge-rules/{id}/waf-exclusions',
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
        409: `code: edge_rule_conflict — duplicate or overlapping rule state rejected.`,
        412: `If-Match no longer names the app's latest edge-rule set version (edge_rules_version_mismatch). The response ETag carries the current version.`,
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
   * List recorded versions of an app's edge-rule set, newest first.
   * Every committed change to an app's edge rules records the whole rule
   * set as a new version. Up to the 50 newest versions are
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    /**
     * Rule-set version number, from the versions listing.
     */
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Sampled requests each edge rule matched, newest first.
   * Gateways keep the first 10 matches of each rule per minute
   * as full events (request ID, method, host, path without query string,
   * trusted client IP, country, user agent); hit counts (stats) cover
   * every match. Events are kept 7 days; how far back a plan can read is
   * a plan limit (Free 24 h, Hobby 72 h, Pro and Scale 7 days), and a
   * longer since is clamped (the response reports the effective start).
   *
   * @returns EdgeRuleEventsResponse One page of events.
   * @throws ApiError
   */
  public static listEdgeRuleEvents({
    slug,
    rule,
    outcome,
    requestId,
    since = '24h',
    limit = 50,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Only events for this edge rule id.
     */
    rule?: string,
    /**
     * Only events with this outcome; detected, warned and blocked are kind=waf detections; challenged, rejected, solved and failed are kind=challenge outcomes; boxed is a client put in a penalty box.
     */
    outcome?: 'matched' | 'logged' | 'detected' | 'warned' | 'blocked' | 'challenged' | 'rejected' | 'solved' | 'failed' | 'boxed',
    /**
     * Only events for this request ID, such as the reference a visitor without JavaScript sees on the challenge page.
     */
    requestId?: string,
    /**
     * Duration such as 1h, 24h or 7d.
     */
    since?: string,
    /**
     * Events per page.
     */
    limit?: number,
    /**
     * next_cursor from the previous page.
     */
    cursor?: string,
  }): CancelablePromise<EdgeRuleEventsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules/events',
      path: {
        'slug': slug,
      },
      query: {
        'rule': rule,
        'outcome': outcome,
        'request_id': requestId,
        'since': since,
        'limit': limit,
        'cursor': cursor,
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
   * The app's WAF detections grouped for a false-positive review.
   * Reads the app's sampled kind=waf detections
   * (detected, warned and blocked events) and groups
   * them by edge rule, CRS rule and path, with id-like path segments read
   * as {id}. Each group is assessed: likely_false_positive (a documented
   * false positive, or at least 3 clients flagged on the path with none
   * sending most of the detections), likely_attack (most detections from
   * clients flagged on 5 or more paths), excluded (the rule already
   * excludes the CRS rule) or review. The newest 5,000 detections of each
   * outcome are read and at most 50 groups returned, largest first.
   * Detections are sampled, so counts are lower bounds. The window
   * defaults to 7 days and is clamped to the plan's event window.
   *
   * @returns EdgeWAFReviewResponse The review.
   * @throws ApiError
   */
  public static getEdgeWafReview({
    slug,
    rule,
    since = '7d',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Only detections of this kind=waf edge rule id.
     */
    rule?: string,
    /**
     * Duration such as 24h or 7d.
     */
    since?: string,
  }): CancelablePromise<EdgeWAFReviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules/waf-review',
      path: {
        'slug': slug,
      },
      query: {
        'rule': rule,
        'since': since,
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
   * Per-rule match counts for an app over a window.
   * Gateways count each rule's matches (matched for enforced
   * rules, logged for log-mode rules), at most once per rule per request,
   * and flush them into hourly buckets once a minute; buckets are kept
   * for 14 days. Rules with no matches in the window are omitted.
   *
   * @returns EdgeRuleStatsResponse Per-rule counts.
   * @throws ApiError
   */
  public static getEdgeRuleStats({
    slug,
    window = '24h',
    series = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * How far back to total hit counts.
     */
    window?: '1h' | '24h' | '7d',
    /**
     * Also return each rule's hourly totals.
     */
    series?: boolean,
  }): CancelablePromise<EdgeRuleStatsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules/stats',
      path: {
        'slug': slug,
      },
      query: {
        'window': window,
        'series': series,
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
   * Protection recipes suggested from an app's own traffic.
   * Reads the app's request telemetry over the last 7 days (or
   * the plan's telemetry retention when shorter) and recommends
   * protection recipes with the parameters to add them with:
   * blocking unverified bots when bots are a noticeable share of
   * requests, rate-limiting busy or failing sign-in paths, and limiting
   * /admin to known IPs. Recipes already added and paths an enabled rule
   * of the same kind covers are skipped. Read-only; apply a suggestion
   * with the recipe (dashboard or `gregale edge-rules recipe`). Plans
   * without request analytics get 402.
   *
   * @returns EdgeRuleRecipeSuggestionsResponse Ranked suggestions, possibly empty.
   * @throws ApiError
   */
  public static getEdgeRuleSuggestions({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<EdgeRuleRecipeSuggestionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/edge-rules/suggestions',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: feature_not_allowed — request targets a feature the plan does not entitle (async_invoke / queues / delayed_tasks on Free).`,
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Per-route throttle recommender. Read-only: returns a suggested rps/burst per
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
     * App slug (lowercase, kebab-case; per-account unique). The recommender walks the per-route rate() for this app's the gateway reader.
     */
    slug: string,
    /**
     * Prometheus window. Closed vocabulary. Defaults to 5m.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
}
