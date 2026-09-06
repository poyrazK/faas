/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataUpstreamHistoryResponse } from '../models/DataUpstreamHistoryResponse.js';
import type { DataUpstreamListResponse } from '../models/DataUpstreamListResponse.js';
import type { DataUpstreamResponse } from '../models/DataUpstreamResponse.js';
import type { PutDataUpstreamRequest } from '../models/PutDataUpstreamRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class UpstreamsService {
  /**
   * List data upstreams on an app.
   * Returns every captured (host_redacted_hash, kind, port) tuple
   * on the app. The plaintext host NEVER appears in the response —
   * only the SHA-256 hash of (salt||host). See spec §11.
   *
   * The list is bounded by the per-plan `api.DataPlacementHintsPerApp`
   * quota (Free=0, Hobby=3, Pro=10, Scale=50). When FAAS_DATA_PLACEMENT
   * is on the classifier derives entries on env mutation; when OFF
   * the table stays empty and the response is `[]`.
   *
   * ADR-098 amendment (issue #954): `?deployment_scope=` narrows
   * the list to one deployment so the dashboard can render
   * staging-vs-prod independently. Omitted = "all deployments".
   *
   * @returns DataUpstreamListResponse Captured upstreams on the app (plaintext never returned).
   * @throws ApiError
   */
  public static listAppDataUpstreams({
    slug,
    deploymentScope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * ADR-098 amendment (issue #954). Optional server-side filter
     * that narrows the list to one deployment. Omitted = return
     * all deployments for the app. Same shape as `scope` (3..40
     * chars, lowercase alnum + dash); empty string is treated as
     * "no filter".
     *
     */
    deploymentScope?: string,
  }): CancelablePromise<DataUpstreamListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/upstreams',
      path: {
        'slug': slug,
      },
      query: {
        'deployment_scope': deploymentScope,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Add a data upstream to an app.
   * Captures a (host, kind, port) tuple so the meterd probe loop
   * can dial it (PR-C) and schedd can bias wake placement by the
   * probed RTT (PR-D). Plaintext host is hashed via
   * `sha256(HostHashSalt||host)` before insert; the response
   * returns the hashed form only (§11 invariant).
   *
   * **Plan limits.** Free plan returns 402
   * `plan_data_upstreams_not_allowed`. Hobby/Pro/Scale hit their
   * per-app cap (3/10/50) before the request body is parsed —
   * server returns 403 `plan_limit_data_upstreams` with the
   * observed count. Invalid inputs return 400
   * `upstream_invalid_{kind,host,port}`.
   *
   * @returns DataUpstreamResponse The stored upstream envelope.
   * @throws ApiError
   */
  public static createAppDataUpstream({
    slug,
    requestBody,
    deploymentScope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: PutDataUpstreamRequest,
    /**
     * ADR-098 amendment (issue #954). Optional server-side filter
     * that narrows the list to one deployment. Omitted = return
     * all deployments for the app. Same shape as `scope` (3..40
     * chars, lowercase alnum + dash); empty string is treated as
     * "no filter".
     *
     */
    deploymentScope?: string,
  }): CancelablePromise<DataUpstreamResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/upstreams',
      path: {
        'slug': slug,
      },
      query: {
        'deployment_scope': deploymentScope,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `400 — any of {upstream_invalid_kind, upstream_invalid_host, upstream_invalid_port}.`,
        401: `code: unauthorized`,
        402: `402 — plan_data_upstreams_not_allowed (Free plan).`,
        403: `403 — plan_limit_data_upstreams (per-app cap exceeded).`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Get historical data-upstream probe metrics.
   * Returns one time series per captured upstream and probe region.
   * Each bucket contains p50/p95 successful RTTs and the total number
   * of probes, including failures. The query is bounded to the probe
   * retention window and is aggregated server-side; raw probe rows and
   * plaintext hosts are never returned.
   *
   * @returns DataUpstreamHistoryResponse Bucketed upstream probe history.
   * @throws ApiError
   */
  public static getAppDataUpstreamHistory({
    slug,
    from,
    to,
    bucket = '5m',
    region,
    deploymentScope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Inclusive RFC3339 window start. Defaults to 24 hours before `to`; the maximum window is 30 days.
     */
    from?: string,
    /**
     * Exclusive RFC3339 window end. Defaults to the current time.
     */
    to?: string,
    /**
     * Aggregation bucket duration, from 1m through 24h. The result is capped at 1000 buckets.
     */
    bucket?: string,
    /**
     * Optional probe region filter. Omitted returns every region with samples.
     */
    region?: string,
    /**
     * Optional deployment scope filter from the ADR-098 issue #954 overlay.
     */
    deploymentScope?: string,
  }): CancelablePromise<Array<DataUpstreamHistoryResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/upstreams/history',
      path: {
        'slug': slug,
      },
      query: {
        'from': from,
        'to': to,
        'bucket': bucket,
        'region': region,
        'deployment_scope': deploymentScope,
      },
      errors: {
        400: `Invalid time window, bucket, or region filter.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Get one data upstream.
   * Returns the single upstream row by id. Plaintext host NEVER
   * appears in the response (§11 invariant).
   *
   * @returns DataUpstreamResponse The upstream envelope.
   * @throws ApiError
   */
  public static getAppDataUpstream({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Upstream id (UUID).
     */
    id: string,
  }): CancelablePromise<DataUpstreamResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/upstreams/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Delete a data upstream.
   * Removes one row by id. Cascades into the probe set
   * (next probe tick no longer dials the host).
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteAppDataUpstream({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Upstream id (UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/upstreams/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `404 — upstream_not_found.`,
      },
    });
  }
}
