/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InstanceResponse } from '../models/InstanceResponse.js';
import type { ListInstancesResponse } from '../models/ListInstancesResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class InstancesService {
  /**
   * Read-only instance list for an app.
   * By default this returns only resident and in-flight instances, bounded
   * by the app's effective concurrency limit. Set `history=true` to return
   * the newest 100 retained lifecycle rows. The history view is not a
   * complete audit log: PARKED wake rows expire after the configured
   * instance-retention window (30 days by default), and only the newest
   * 100 retained rows are returned. Snapshots are durable deployment
   * artifacts with their own lifecycle and are not removed with PARKED
   * instance history.
   *
   * @returns InstanceResponse Current instances, or the bounded retained history when `history=true`.
   * @throws ApiError
   */
  public static listInstances({
    slug,
    history = false,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * When true, include the newest 100 retained lifecycle rows; PARKED rows expire after 30 days by default.
     */
    history?: boolean,
  }): CancelablePromise<Array<InstanceResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/instances',
      path: {
        'slug': slug,
      },
      query: {
        'history': history,
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
   * List every live instance across the caller's account.
   * Replaces the per-app fan-out from `/v1/apps/{slug}/instances`
   * with one account-scoped read (issue #393). Each instance carries
   * its `app_id`; cross-account isolation is enforced by the SQL
   * `apps.account_id = $1` join (test: see
   * `TestListInstancesForAccount_CrossAccountIsolation`).
   *
   * Cursor: `?before=<id>` (the instances.id UUIDv7). Defaults to 25
   * per page; capped at 100; invalid `limit` returns 400
   * `code_validation` with `limit=100` and the observed value
   * (RFC 7807 strict mode, matching `/v1/invoices`).
   *
   * Rate-limit tiering: one call now replaces N per-app calls,
   * so per-page-load token spend drops from N to 1. The
   * per-account bucket (ADR-040) still applies at the gatewayd
   * edge; this route charges 1 token via the apid authLimited
   * middleware, same as every other `/v1*` route.
   *
   * @returns ListInstancesResponse One page of instances, newest first.
   * @throws ApiError
   */
  public static listInstancesForAccount({
    before,
    limit = 25,
  }: {
    /**
     * Cursor (instances.id UUIDv7). Omit for the most recent page.
     */
    before?: string,
    /**
     * Page size; server clamps to 1..100, returns 400 with `limit=100` and the observed value on bad input.
     */
    limit?: number,
  }): CancelablePromise<ListInstancesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/instances',
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
