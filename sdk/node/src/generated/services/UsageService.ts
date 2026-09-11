/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountUsageResponse } from '../models/AccountUsageResponse.js';
import type { DailyUsageListResponse } from '../models/DailyUsageListResponse.js';
import type { InvoiceListResponse } from '../models/InvoiceListResponse.js';
import type { Problem } from '../models/Problem.js';
import type { StorageUsageListResponse } from '../models/StorageUsageListResponse.js';
import type { UsageResponse } from '../models/UsageResponse.js';
import type { UsageSummaryResponse } from '../models/UsageSummaryResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class UsageService {
  /**
   * Read the account-wide usage projection
   * Requires usage read scope. Returns the compute usage summary together
   * with optional object-storage and managed-PostgreSQL usage views when
   * those services are configured for the account. Each service keeps its
   * existing freshness and guardrail semantics; omitted optional fields
   * mean that service is not enabled for the account. The optional service
   * views are included only for the current UTC month; historical requests
   * return the compute projection alone.
   *
   * @returns AccountUsageResponse Account-wide usage projection; Cache-Control no-store
   * @returns Problem Authentication or usage accounting error
   * @throws ApiError
   */
  public static getAccountUsage({
    month,
  }: {
    /**
     * UTC calendar month for compute usage, in YYYY-MM form. Defaults to the current month.
     */
    month?: string,
  }): CancelablePromise<AccountUsageResponse | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/usage',
      query: {
        'month': month,
      },
    });
  }
  /**
   * Per-app monthly usage.
   * @returns UsageResponse Per-app usage rows for the month.
   * @throws ApiError
   */
  public static getUsage({
    month,
  }: {
    /**
     * Billing month in `YYYY-MM` form (UTC). Defaults to the current month.
     */
    month?: string,
  }): CancelablePromise<Array<UsageResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/usage',
      query: {
        'month': month,
      },
      errors: {
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Monthly roll-up with overage math.
   * @returns UsageSummaryResponse The monthly roll-up with overage math.
   * @throws ApiError
   */
  public static usageSummary({
    month,
  }: {
    /**
     * Billing month in `YYYY-MM` form (UTC). Defaults to the current month for the roll-up.
     */
    month?: string,
  }): CancelablePromise<UsageSummaryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/usage/summary',
      query: {
        'month': month,
      },
      errors: {
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Per-app daily rollup (informational).
   * @returns DailyUsageListResponse Per-app daily rollup rows for the requested day.
   * @throws ApiError
   */
  public static usageDaily({
    day,
  }: {
    /**
     * Calendar day in `YYYY-MM-DD` form (UTC). Required.
     */
    day: string,
  }): CancelablePromise<DailyUsageListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/usage/daily',
      query: {
        'day': day,
      },
      errors: {
        400: `Missing or malformed \`day\` query parameter.`,
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Per-app daily storage rollup (informational).
   * @returns StorageUsageListResponse Per-app storage rollup rows for the requested day.
   * @throws ApiError
   */
  public static usageStorage({
    day,
  }: {
    /**
     * Calendar day in `YYYY-MM-DD` form (UTC). Required for the storage rollup.
     */
    day: string,
  }): CancelablePromise<StorageUsageListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/usage/storage',
      query: {
        'day': day,
      },
      errors: {
        400: `Missing or malformed \`day\` query parameter on the storage rollup surface.`,
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List the authenticated account's billing invoices.
   * @returns InvoiceListResponse One page of invoices, newest first.
   * @throws ApiError
   */
  public static listInvoices({
    month,
    before,
    limit = 25,
  }: {
    /**
     * Optional `YYYY-MM` filter (UTC half-open range on period_end).
     */
    month?: string,
    /**
     * Cursor (RFC3339Nano) for the next older page. Omit for the first page.
     */
    before?: string,
    /**
     * Page size; server clamps to 1..100.
     */
    limit?: number,
  }): CancelablePromise<InvoiceListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/invoices',
      query: {
        'month': month,
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
