/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingCancelResponse } from '../models/BillingCancelResponse.js';
import type { BillingPortalResponse } from '../models/BillingPortalResponse.js';
import type { BillingRetryResponse } from '../models/BillingRetryResponse.js';
import type { BillingStatusResponse } from '../models/BillingStatusResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class BillingService {
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
   * This projection is partial: required PaymentTerms is empty because
   * provider payment terms are not persisted. It does not claim complete
   * FOCUS conformance or expose the Cost and Usage dataset. Issue and due
   * dates, payment-currency conversions, purchase orders, provider line
   * items, and separate credit/refund documents are unavailable. See
   * /docs/billing#focus-invoice-export and the metadata limitations.
   *
   * At most 1000 stored invoices are read per month (including excluded
   * invoices); a larger set returns 422 without a truncated artifact.
   * Invalid stored amounts, currencies, identifiers, or dates return 409
   * before any artifact bytes are sent. Each artifact is at most 3 MiB.
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
