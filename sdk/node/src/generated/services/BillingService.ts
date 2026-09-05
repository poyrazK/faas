/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingCancelResponse } from '../models/BillingCancelResponse.js';
import type { BillingPortalResponse } from '../models/BillingPortalResponse.js';
import type { BillingRetryResponse } from '../models/BillingRetryResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class BillingService {
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
