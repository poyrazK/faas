/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AuditEventResponse } from '../models/AuditEventResponse.js';
import type { ListAuditEventsResponse } from '../models/ListAuditEventsResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class AuditService {
  /**
   * List the caller's auth audit events.
   * Newest-first. Returns the customer's own security event timeline:
   * logins, logouts, key mints/deletes, secret sets/deletes, plan
   * changes, and account deletion scheduling/restore.
   *
   * The events table is append-only (spec §5 / §6.1). The response
   * is bounded by `limit` (default 50, max 100). For a full-history
   * pull, use `GET /v1/account/export`, which unions these events
   * with the customer's GDPR-action rows.
   *
   * @returns ListAuditEventsResponse Newest-first audit events for the caller.
   * @throws ApiError
   */
  public static listAuditEvents({
    since,
    kindPrefix,
    limit = 50,
  }: {
    /**
     * Only return rows with `at >= since` (RFC 3339). Omit to read from the newest row.
     */
    since?: string,
    /**
     * Only return rows whose `kind` starts with this prefix (e.g. `key.` returns `key.created` and `key.deleted`).
     */
    kindPrefix?: string,
    /**
     * Max rows to return. Silently capped at 100.
     */
    limit?: number,
  }): CancelablePromise<ListAuditEventsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/audit-events',
      query: {
        'since': since,
        'kind_prefix': kindPrefix,
        'limit': limit,
      },
      errors: {
        400: `Bad request — malformed \`since\` or \`limit\`.`,
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Fetch one auth audit event by id.
   * Returns the single event with that id if and only if it
   * belongs to the caller's account. Cross-account access is
   * indistinguishable from "not found" — a customer cannot
   * enumerate other accounts' row counts by ID-probing.
   *
   * @returns AuditEventResponse The event.
   * @throws ApiError
   */
  public static getAuditEvent({
    id,
  }: {
    /**
     * Event row id (bigint as string).
     */
    id: string,
  }): CancelablePromise<AuditEventResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/audit-events/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `Bad request — id is not a positive integer.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
