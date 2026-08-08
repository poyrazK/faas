/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AuditEventResponse } from '../models/AuditEventResponse.js';
import type { ListAuditEventsResponse } from '../models/ListAuditEventsResponse.js';
import type { ListAuditLogResponse } from '../models/ListAuditLogResponse.js';
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
    includeAnonymous = false,
    appId,
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
    /**
     * Wave 0 PR-C / ADR-047: also surface rows with `subject =
     * NULL`. The defensive case where the customer's app row
     * was deleted between wake and the stateless-advisory
     * audit emit. Default `false` (the customer never sees
     * subject=NULL rows); operators can flip to `true` via
     * `?include_anonymous=true` for post-mortems.
     *
     */
    includeAnonymous?: boolean,
    /**
     * Wave 0 PR-C / ADR-047: filter the overscan window to
     * events whose `data.app_id` matches the given uuid. The
     * dashboard's `app_detail.html` "Stateless advisories"
     * link uses this combo with `kind_prefix=stateless.advisory`
     * to drill into a single app's advisories. Resolved
     * post-SQL on the bounded overscan window; the events
     * table is not indexed on `data`.
     *
     */
    appId?: string,
  }): CancelablePromise<ListAuditEventsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/audit-events',
      query: {
        'since': since,
        'kind_prefix': kindPrefix,
        'limit': limit,
        'include_anonymous': includeAnonymous,
        'app_id': appId,
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
  /**
   * List the caller's audit-log entries (post-deletion evidence).
   * Newest-first. Reads the FK-free `audit_log` table
   * (migrations/00163_audit_log.sql), distinct from
   * `/v1/audit-events` which reads the live `events` table. The
   * `audit_log` table is append-only by spec (ISO 27001 SoA
   * A.5.33 — retention forever) and a regulator / DPO can replay
   * post-deletion state from the row alone.
   *
   * Scope: session cookie (implicitly admin) or any API key
   * carrying `{admin, apps:read}` (`api.ScopesReadSurface`).
   * MFA-gated. Cross-account invisibility is enforced by pinning
   * `account_id` to the calling account's id inside the handler;
   * the SQL filter rejects `account_id IS NULL` rows by default
   * (a customer never sees anonymous rows).
   *
   * @returns ListAuditLogResponse Newest-first audit-log entries for the caller.
   * @throws ApiError
   */
  public static listAuditLog({
    since,
    kindPrefix,
    limit = 50,
  }: {
    /**
     * Audit-log rows with `received_at >= since` (RFC 3339) are returned. Omit to read from the newest row.
     */
    since?: string,
    /**
     * Only return rows whose `kind` starts with this prefix (e.g. `account.` returns `account.deleted`).
     */
    kindPrefix?: string,
    /**
     * Audit-log page size. Silently capped at 100.
     */
    limit?: number,
  }): CancelablePromise<ListAuditLogResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/audit-log',
      query: {
        'since': since,
        'kind_prefix': kindPrefix,
        'limit': limit,
      },
      errors: {
        400: `Bad request — malformed \`since\`, \`limit\`, or \`kind_prefix\` (each must be RFC 3339 / a positive integer / a non-empty string when present).`,
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Operator view of every audit-log entry (cross-account).
   * Admin-only read of the FK-free `audit_log` table. Reads
   * across accounts by default; pass `?account_id=<uuid>` to
   * restrict to one account. `?include_anonymous=true` surfaces
   * the `account_id IS NULL` rows emitted by background /
   * anonymous activity (the customer-scoped `/v1/audit-log`
   * endpoint never returns those rows).
   *
   * Scope: session cookie (implicitly admin) or an admin API
   * key (`api.ScopesAdminOnly` — `{admin}`). Not MFA-gated at
   * the handler level because admin sessions / admin keys are
   * already MFA-gated upstream at session / key issue time.
   *
   * @returns ListAuditLogResponse Newest-first audit-log entries across accounts.
   * @throws ApiError
   */
  public static listAuditLogAll({
    accountId,
    since,
    kindPrefix,
    limit = 50,
    includeAnonymous = false,
  }: {
    /**
     * Restrict the result to rows with this `account_id`. Omit to read across all accounts.
     */
    accountId?: string,
    /**
     * Operator-side audit-log rows with `received_at >= since` (RFC 3339) are returned. Omit to read from the newest row.
     */
    since?: string,
    /**
     * Only return rows whose `kind` starts with this prefix.
     */
    kindPrefix?: string,
    /**
     * Operator-side audit-log page size. Silently capped at 100.
     */
    limit?: number,
    /**
     * When `true`, also surface rows with `account_id IS NULL` (anonymous / background activity). Default `false`.
     */
    includeAnonymous?: boolean,
  }): CancelablePromise<ListAuditLogResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/audit-log/all',
      query: {
        'account_id': accountId,
        'since': since,
        'kind_prefix': kindPrefix,
        'limit': limit,
        'include_anonymous': includeAnonymous,
      },
      errors: {
        400: `Bad request — malformed \`since\`, \`limit\`, or \`account_id\`.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
