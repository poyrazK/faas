/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplyAppOpenAPIPolicyRequest } from '../models/ApplyAppOpenAPIPolicyRequest.js';
import type { AppOpenAPIImportDryRunResponse } from '../models/AppOpenAPIImportDryRunResponse.js';
import type { AppOpenAPIImportResponse } from '../models/AppOpenAPIImportResponse.js';
import type { AppOpenAPIPolicyApplyResponse } from '../models/AppOpenAPIPolicyApplyResponse.js';
import type { AppOpenAPIPolicyPreviewResponse } from '../models/AppOpenAPIPolicyPreviewResponse.js';
import type { OpenAPIContractDiffResponse } from '../models/OpenAPIContractDiffResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class OpenapiImportService {
  /**
   * Read the imported (or auto-generated) OpenAPI document for an app.
   * Two source modes (selected via `?source=`):
   *
   * - `manual_import` (default): returns the customer's uploaded
   * doc verbatim. Mirrors item #1's /deployments/{dep}/openapi
   * but on the app-keyed `app_openapi_docs` table (ADR-126 D1).
   *
   * - `auto`: runs pkg/openapidiff.GenerateFromApp with the
   * imported doc + observed routes (ADR-093 bridge) +
   * existing edge rules; the merged spec is cached for 5 min
   * per (app_id, sha(doc), sha(routes), sha(rules)). Cache
   * headers: X-Faas-Cache: hit|miss, X-OpenAPI-Doc-Source:
   * "auto" | "degraded: routes_unavailable" |
   * "degraded: rules_unavailable" | "empty: no_import_no_rules".
   *
   * Limits are abuse-surface, not plan-tier — every plan
   * including Free can import. Per-account row cap is
   * Plan.OpenAPIImportsPerAccount (Free 100, Hobby 1000,
   * Pro 10000, Scale 10000). Plan-tier gate is intentionally
   * absent on this surface (ADR-126 D6).
   *
   * @returns any The OpenAPI document (imported or auto-generated).
   * @throws ApiError
   */
  public static getAppOpenApi({
    slug,
    source = 'manual_import',
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Source mode. `manual_import` (default) returns the persisted customer doc verbatim. `auto` returns the platform-merged spec (imported doc ∪ observed routes ∪ existing edge rules).
     */
    source?: 'manual_import' | 'auto',
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/openapi',
      path: {
        'slug': slug,
      },
      query: {
        'source': source,
      },
      errors: {
        400: `code: invalid_source. \`source\` query value is not in the enum.`,
        401: `code: unauthorized`,
        404: `code: not_found. No imported doc exists for this app (manual_import mode), or the app slug is cross-tenant.`,
        405: `code: dry_run_requires_post. \`?source=dry_run\` is GET-only 405; dry-run is POST-only.`,
      },
    });
  }
  /**
   * Import an OpenAPI document for an app.
   * Customer-facing import (ADR-126 / issue #975 item #2).
   * Reads the body, validates via
   * pkg/openapiimport.ValidateImport (structural-minimum
   * OpenAPI 3.0 / 3.1 check), enforces size + endpoint
   * caps, persists via UpsertAppOpenAPIDoc, emits
   * app.openapi_import.replaced audit + pg_notify on
   * NotifyAppOpenAPIDocChanged. The auto-gen cache
   * (NotifyAppOpenAPIDocChanged + NotifyEdgeRuleChanged
   * fan-in) is flushed per-app so the next `?source=auto`
   * read recomputes.
   *
   * Limits (abuse-surface, not plan-tier): body cap
   * state.OpenAPIImportMaxDocBytes (256 KiB), endpoint
   * cap state.OpenAPIImportMaxEndpoints (50). Per-account
   * row cap is Plan.OpenAPIImportsPerAccount.
   *
   * @returns AppOpenAPIImportResponse Stored.
   * @throws ApiError
   */
  public static importAppOpenApi({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: {
      /**
       * OpenAPI version (3.0.x / 3.1.x).
       */
      openapi: string;
      info: Record<string, any>;
      paths: Record<string, any>;
    },
  }): CancelablePromise<AppOpenAPIImportResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/openapi',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: empty_body. Body is zero bytes.`,
        401: `code: unauthorized`,
        403: `code: openapi_import_quota_reached. Plan.OpenAPIImportsPerAccount() cap reached.`,
        413: `code: openapi_import_too_large. Body exceeds state.OpenAPIImportMaxDocBytes (256 KiB) on the import endpoint.`,
        422: `code: openapi_import_invalid or openapi_import_too_many_endpoints. Doc fails the structural-minimum validator or declares more than state.OpenAPIImportMaxEndpoints (50) endpoints on the import endpoint.`,
      },
    });
  }
  /**
   * Delete the imported OpenAPI document for an app.
   * Idempotent: returns 204 even if no row existed.
   * Emits app.openapi_import.deleted audit + pg_notify
   * on NotifyAppOpenAPIDocChanged so the auto-gen cache
   * flushes.
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteAppOpenApi({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/openapi',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
      },
    });
  }
  /**
   * Read-only preview of edge-rule suggestions for an imported doc.
   * POST-only (the body IS the import body). Same auth chain
   * as the GET surface minus the MFA requirement (read-only).
   * Validates the doc and walks paths, emitting one
   * EdgeRuleSuggestion per (path, method) pair NOT already
   * covered by an existing validate edge rule. Empty array
   * when the doc is fully covered.
   *
   * Customer pastes each suggestion's Path + Methods + Kind
   * + Action back into the existing create-edge-rule endpoint
   * (item #2 D3). Does NOT persist; does NOT emit pg_notify.
   *
   * @returns AppOpenAPIImportDryRunResponse Dry-run suggestions.
   * @throws ApiError
   */
  public static dryRunAppOpenApi({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: {
      openapi: string;
      info: Record<string, any>;
      paths: Record<string, any>;
    },
  }): CancelablePromise<AppOpenAPIImportDryRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/openapi/dry-run',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: empty_body.`,
        401: `code: unauthorized`,
        413: `code: openapi_import_too_large. Body exceeds state.OpenAPIImportMaxDocBytes (256 KiB) on the dry-run endpoint.`,
        422: `code: openapi_import_invalid or openapi_import_too_many_endpoints. Doc fails the structural-minimum validator or declares more than state.OpenAPIImportMaxEndpoints (50) endpoints on the dry-run endpoint.`,
      },
    });
  }
  /**
   * Preview declared routes, observed routes, and matching edge policies.
   * Read-only route-policy preview for API-hosting roadmap item 11.
   * Joins the persisted OpenAPI declaration with gatewayd's observed
   * route labels and the app's edge rules. Each route is classified as
   * `matched`, `declared_only`, or `observed_only`; `covered` is true
   * when at least one enabled edge rule matches the path and method.
   * When the gateway bridge is unavailable the response remains useful,
   * sets `observed_available` to false, and reports
   * `source=degraded: routes_unavailable`. No policy or document writes
   * occur on this endpoint.
   *
   * @returns AppOpenAPIPolicyPreviewResponse Declared-vs-observed route and policy preview.
   * @throws ApiError
   */
  public static previewAppOpenApiPolicy({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppOpenAPIPolicyPreviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/openapi/preview',
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
   * Plan or explicitly apply generated OpenAPI validation rules.
   * The default request is a read-only plan. It returns a deterministic
   * `preview_sha256` approval token and the validation rules that would be
   * created for uncovered OpenAPI operations. To mutate policy, repeat the
   * request with `confirm=true` and the exact token. If the document or
   * edge rules changed in the meantime, the server returns 409
   * `openapi_policy_stale` and no writes occur. Applying an already-covered
   * document is an idempotent no-op. A missing match_host defaults to the
   * app's platform hostname. Requires MFA and deploy-write scope.
   *
   * @returns AppOpenAPIPolicyApplyResponse Policy plan or applied rules.
   * @throws ApiError
   */
  public static applyAppOpenApiPolicy({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody?: ApplyAppOpenAPIPolicyRequest,
  }): CancelablePromise<AppOpenAPIPolicyApplyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/openapi/apply',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation or openapi_policy_confirmation_required.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: openapi_policy_stale. The approval token no longer matches the current plan.`,
        422: `code: validation. A generated rule failed edge-rule validation.`,
        500: `Internal server error.`,
      },
    });
  }
  /**
   * Preview the production OpenAPI contract gate.
   * Read-only ADR-121 contract diff. Compares the current projected
   * OpenAPI surface against the latest captured live snapshot in the
   * requested scope (default `prod`). `blocking=true` means a production
   * promotion would be rejected while `FAAS_API_CONTRACT_DIFF_ENABLED`
   * is enabled. The route remains registered while the flag is off and
   * returns 503 `api_contract_diff_disabled`.
   *
   * @returns OpenAPIContractDiffResponse Contract diff result.
   * @throws ApiError
   */
  public static diffAppOpenApiContract({
    slug,
    scope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment scope to compare; defaults to `prod`.
     */
    scope?: string,
  }): CancelablePromise<OpenAPIContractDiffResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/openapi/diff',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `code: api_contract_diff_disabled.`,
      },
    });
  }
}
