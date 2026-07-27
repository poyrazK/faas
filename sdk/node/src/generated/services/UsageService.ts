/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InvoiceListResponse } from '../models/InvoiceListResponse.js';
import type { UsageResponse } from '../models/UsageResponse.js';
import type { UsageSummaryResponse } from '../models/UsageSummaryResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class UsageService {
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
