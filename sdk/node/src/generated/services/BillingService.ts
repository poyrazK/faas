/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingCancelResponse } from '../models/BillingCancelResponse.js';
import type { BillingPortalResponse } from '../models/BillingPortalResponse.js';
import type { BillingRetryResponse } from '../models/BillingRetryResponse.js';
import type { BillingStatusResponse } from '../models/BillingStatusResponse.js';
import type { InvoiceHistoryBackfillResponse } from '../models/InvoiceHistoryBackfillResponse.js';
import type { InvoiceRefreshResponse } from '../models/InvoiceRefreshResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class BillingService {
  /**
   * Refresh provider facts for an existing account invoice.
   * Requires usage:read and the invoice-history session MFA gate. Fetches
   * the configured provider's invoice, transaction, or order using the
   * account's provider-qualified customer identity. Accepts no body or query
   * parameters. Only invoice details and their lifecycle history change;
   * monetary values, payment state, refunds, credits, and plan are preserved.
   * Provider identity, currency, total, and tax must match the captured local
   * invoice. A concurrent invoice update returns 409, allowing a fresh retry.
   * Stripe paginates all lines and resolves opaque price/plan IDs. Unknown
   * facts and classifications remain source gaps. The operation is bounded
   * to 32 provider reads, 1,000 items, 4 MiB per response, and two minutes.
   * It enriches known invoices; it does not discover missing provider history.
   *
   * @returns InvoiceRefreshResponse Refreshed source coverage; invoice financial fields are unchanged.
   * @throws ApiError
   */
  public static refreshInvoiceFacts({
    id,
  }: {
    /**
     * Local invoice UUID from GET /v1/invoices.
     */
    id: string,
  }): CancelablePromise<InvoiceRefreshResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/invoices/{id}/refresh',
      path: {
        'id': id,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        501: `Configured provider does not implement invoice refresh.`,
        503: `code: capacity_unavailable — no host headroom (alerting; should be near-impossible).`,
      },
    });
  }
  /**
   * Import one bounded page of missing provider invoices.
   * Requires usage:read and invoice-history session MFA. Scans at most 25
   * invoices for the authenticated account's provider-qualified customer.
   * The opaque next_cursor resumes the next page for the same provider
   * customer. Existing natural-key matches are skipped without modifying
   * webhook data. Imported documents preserve provider financial facts and
   * carry an unknown historical Gregale plan, so credit proration fails
   * closed until that plan is independently established. Unsupported
   * currencies, statuses, incomplete totals, and absent billing periods are
   * counted as skipped. has_more reports provider pagination at request time;
   * it is not a stable snapshot guarantee. Each page commits atomically.
   *
   * @returns InvoiceHistoryBackfillResponse One imported provider-history page.
   * @throws ApiError
   */
  public static backfillInvoiceHistory({
    cursor,
    limit = 25,
  }: {
    /**
     * Provider and customer bound token emitted by the preceding response.
     */
    cursor?: string,
    /**
     * Maximum provider records to scan in this page.
     */
    limit?: number,
  }): CancelablePromise<InvoiceHistoryBackfillResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/invoices/backfill',
      query: {
        'cursor': cursor,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        501: `Configured provider does not implement invoice history discovery.`,
        503: `code: capacity_unavailable — no host headroom (alerting; should be near-impossible).`,
      },
    });
  }
  /**
   * Download a partial FOCUS 1.4 Invoice Detail projection.
   * Requires usage:read and the same session MFA gate as invoice history.
   * Includes only the authenticated account's locally persisted invoices
   * whose period_end falls in the requested UTC month. Draft and void
   * invoices are excluded and counted in metadata. The default ZIP binds
   * CSV and metadata to one store snapshot; separate downloads may see
   * newer webhooks. Amounts use exact two-decimal ISO currencies and split
   * non-tax charges from tax. No current plan prices are substituted.
   *
   * This projection remains partial. Stored provider line items are used
   * only when complete, classified, and exactly reconciled to invoice totals;
   * other invoices retain aggregate rows with metadata fallback reasons.
   * Supplied payment terms, issue/due dates, and issuer names are exported.
   * MissingRequiredFields and SourceCoverage describe missing facts. Complete
   * provider history, correction/credit/refund documents, conditional FX/PO
   * fields, and the Cost and Usage dataset remain unavailable. See
   * /docs/billing#focus-invoice-export and the metadata limitations.
   *
   * At most 1000 stored invoices are read per month (including excluded
   * invoices), with at most 10000 output rows and 3 MiB per artifact.
   * Exceeding any bound returns 422 without a truncated artifact. Invalid
   * stored amounts, currencies, identifiers, or dates return 409 before
   * any artifact bytes are sent.
   *
   * @returns binary Complete local invoice projection for the requested account and month; partial FOCUS support.
   * @throws ApiError
   */
  public static exportFocusInvoices({
    month,
    format = 'zip',
  }: {
    /**
     * Required YYYY-MM; selects by invoice period_end, not issue date or usage month.
     */
    month: string,
    /**
     * ZIP contains CSV plus metadata.json; metadata returns the metadata JSON file independently.
     */
    format?: 'zip' | 'csv' | 'metadata',
  }): CancelablePromise<Blob> {
    return __request(OpenAPI, {
      responseType: 'blob',
      method: 'GET',
      url: '/v1/billing/focus',
      query: {
        'month': month,
        'format': format,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `code: conflict`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity_unavailable — no host headroom (alerting; should be near-impossible).`,
      },
    });
  }
  /**
   * Get the authenticated customer's billing status.
   * Returns a provider-independent deployment and account billing
   * projection. This customer endpoint requires usage:read and never
   * calls an operator catalog surface.
   *
   * @returns BillingStatusResponse Current deployment billing mode and customer attachment status.
   * @throws ApiError
   */
  public static getBillingStatus(): CancelablePromise<BillingStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/billing/status',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Get a provider billing portal URL (and the card-on-file summary).
   * Returns the URL the customer should be sent to in order to
   * manage their subscription (update card, view invoices,
   * download receipts, cancel). When the active provider exposes
   * customer sessions, the server creates a short-lived authenticated
   * portal URL. Otherwise it renders the operator's
   * `FAAS_BILLING_PORTAL_URL` template.
   *
   * The response also carries a `payment_method` block (issue
   * #242) — the card-on-file summary (brand, last-4, expiry).
   * The CLI's `faas billing payment-method` renders from this
   * field; the dashboard's billing page does the same. The
   * field is omitempty so no-card-on-file responses stay clean.
   *
   * @returns BillingPortalResponse Portal URL + payment-method summary (any field may be empty when the box has no portal configured).
   * @throws ApiError
   */
  public static getBillingPortal(): CancelablePromise<BillingPortalResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/billing/portal',
      errors: {
        401: `code: unauthorized`,
        403: `code: email_verification_required — verify the account email before deploying code or changing billing settings.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Retry the latest unpaid invoice / transaction for this account.
   * The configured provider is asked to retry the most recent unpaid
   * charge. The Idempotency-Key header (auto UUIDv4 if absent) is
   * forwarded where the provider supports it. Providers without a
   * direct retry API return 501 and the response includes a portal URL
   * for payment-method recovery.
   *
   * @returns BillingRetryResponse New charge attempt created. The CLI prints attempt + provider reference IDs.
   * @throws ApiError
   */
  public static retryLatestCharge(): CancelablePromise<BillingRetryResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/billing/retry',
      errors: {
        401: `code: unauthorized`,
        403: `code: email_verification_required — verify the account email before deploying code or changing billing settings.`,
        404: `No open charge to retry — the account is in good standing, or the operator has not configured a billing provider.`,
        501: `The provider has no direct saved-card retry API; use the billing portal URL in the response.`,
        502: `Provider-side failure. The CLI surfaces this as 'retry failed'.`,
      },
    });
  }
  /**
   * Set cancel_at_period_end on the active subscription; keep the account active until period end.
   * Stripe: `Subscriptions.Update(cancel_at_period_end=true)`.
   * Paddle: `Customer.Update(scheduled_change=cancel)` on the
   * customer's stored object.
   *
   * Returns the effective cancel timestamp (`current_period_end`
   * on Stripe; the next month-rollover instant on Paddle) in
   * RFC 3339 so the CLI can print "your apps will stop on …".
   *
   * @returns BillingCancelResponse Cancellation scheduled; account remains on the plan until period end.
   * @throws ApiError
   */
  public static cancelAtPeriodEnd(): CancelablePromise<BillingCancelResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/billing/cancel',
      errors: {
        401: `code: unauthorized`,
        403: `code: email_verification_required — verify the account email before deploying code or changing billing settings.`,
        409: `Already cancelled. CLI renders a friendly hint.`,
        502: `Provider-side failure.`,
      },
    });
  }
}
