/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
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
   * @returns any Stored.
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
  }): CancelablePromise<{
    app_id: string;
    source: 'manual_import';
    openapi_version: string;
    endpoint_count: number;
    byte_size: number;
    captured_at: string;
    updated_at: string;
  }> {
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
   * @returns any Dry-run suggestions.
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
  }): CancelablePromise<{
    app_id: string;
    openapi_version: string;
    endpoint_count: number;
    suggestions: Array<{
      path: string;
      methods: Array<'get' | 'post' | 'put' | 'patch' | 'delete'>;
      kind: 'validate';
      action: Record<string, any>;
    }>;
  }> {
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
}
