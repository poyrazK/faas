/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingCancelResponse } from '../models/BillingCancelResponse.js';
import type { BillingPortalResponse } from '../models/BillingPortalResponse.js';
import type { BillingRetryResponse } from '../models/BillingRetryResponse.js';
import type { BillingStatusResponse } from '../models/BillingStatusResponse.js';
import type { CreateFinancialBudgetRequest } from '../models/CreateFinancialBudgetRequest.js';
import type { DeleteFinancialBudgetRequest } from '../models/DeleteFinancialBudgetRequest.js';
import type { FinancialBudgetHistoryResponse } from '../models/FinancialBudgetHistoryResponse.js';
import type { FinancialBudgetListResponse } from '../models/FinancialBudgetListResponse.js';
import type { FinancialBudgetPreviewRequest } from '../models/FinancialBudgetPreviewRequest.js';
import type { FinancialBudgetPreviewResponse } from '../models/FinancialBudgetPreviewResponse.js';
import type { FinancialBudgetResponse } from '../models/FinancialBudgetResponse.js';
import type { FinancialCostsResponse } from '../models/FinancialCostsResponse.js';
import type { FinancialForecastResponse } from '../models/FinancialForecastResponse.js';
import type { InvoiceHistoryBackfillResponse } from '../models/InvoiceHistoryBackfillResponse.js';
import type { InvoiceRefreshResponse } from '../models/InvoiceRefreshResponse.js';
import type { UpdateFinancialBudgetRequest } from '../models/UpdateFinancialBudgetRequest.js';
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `Configured provider does not implement invoice refresh.`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `Configured provider does not implement invoice history discovery.`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Read attributable usage costs and historical price contracts.
   * Requires usage:read and session MFA. Uses retained account-owned evidence
   * and immutable price versions. Applies a single shared monthly allowance;
   * when the plan changes, the largest recorded grant is retained and shared
   * proportionally across versions. Known usage amounts use integer millicents.
   * Missing samples and historical prices are explicit coverage gaps.
   * The reported compute/interface-egress scope excludes other bill components.
   * Stored provider invoices are separate facts and are not automatically
   * reconciled to this usage ledger. The UTC usage month and provider invoice
   * periods can differ. At most 10000 allocations are returned; larger reports
   * fail without returning truncated totals. This endpoint has no writes.
   *
   * @returns FinancialCostsResponse Account-owned cost breakdown, coverage, forecasts, and separate invoice facts.
   * @throws ApiError
   */
  public static getFinancialCosts({
    month,
  }: {
    /**
     * Current or historical UTC usage month; defaults to the current month.
     */
    month?: string,
  }): CancelablePromise<FinancialCostsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/billing/costs',
      query: {
        'month': month,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read usage cost forecasts with coverage and method.
   * Requires usage:read and session MFA. Elapsed-time quantity forecasts apply
   * the recorded price after the shared allowance. At least one complete day,
   * fresh evidence, and unchanged pricing are required. Unavailable forecasts
   * contain a reason and omit projected amounts. The overall invoice forecast
   * remains unavailable until all bill components have authoritative coverage.
   * This endpoint is read-only and never changes workload admission.
   *
   * @returns FinancialForecastResponse Meter forecasts and explicit missing bill components.
   * @throws ApiError
   */
  public static getFinancialForecast({
    month,
  }: {
    /**
     * UTC usage month for the projection; defaults to the current month.
     */
    month?: string,
  }): CancelablePromise<FinancialForecastResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/billing/forecast',
      query: {
        'month': month,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List account budget policies and their enforcement readiness.
   * Requires usage:read and session MFA. Deleted policies are omitted. Drafts do not enforce limits.
   * @returns FinancialBudgetListResponse Account-owned policies, bounded to 128.
   * @throws ApiError
   */
  public static listFinancialBudgets(): CancelablePromise<FinancialBudgetListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/billing/budgets',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Save an account-owned budget draft with atomic revision audit.
   * Requires admin scope and session MFA. Idempotency-Key is required (1..255 bytes).
   * It fixes creation identity beyond replay-cache retention. Reusing an operation
   * identity cannot overwrite a changed or deleted policy. Set enabled=false:
   * activation currently returns 422 financial_budget_activation_unavailable.
   * Saving a draft creates no holds, decisions, notifications or workload changes.
   *
   * @returns FinancialBudgetResponse Saved draft with revision 1; retries retain its identity.
   * @throws ApiError
   */
  public static createFinancialBudget({
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Stable creation operation identity, required for retries across replay-cache retention.
     */
    idempotencyKey: string,
    requestBody: CreateFinancialBudgetRequest,
  }): CancelablePromise<FinancialBudgetResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/billing/budgets',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `Invalid policy count or financial_budget_activation_unavailable; no policy mutation occurred.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Read an account-owned budget, including its deletion tombstone.
   * Requires usage:read and session MFA. Foreign or missing policies return 404.
   * @returns FinancialBudgetResponse Saved intent and explicit enforcement readiness.
   * @throws ApiError
   */
  public static getFinancialBudget({
    id,
  }: {
    /**
     * Stable identity of an account-owned budget policy.
     */
    id: string,
  }): CancelablePromise<FinancialBudgetResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/billing/budgets/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Replace a budget draft at its expected revision.
   * Requires admin scope, session MFA and Idempotency-Key. expected_revision
   * must match the current policy; stale edits return 409. Set enabled=false:
   * activation remains unavailable. Scope ownership and action eligibility are
   * revalidated. Payment, security and user holds are independent of this intent.
   *
   * @returns FinancialBudgetResponse Updated draft and incremented revision.
   * @throws ApiError
   */
  public static updateFinancialBudget({
    id,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Stable identity of an account-owned budget policy.
     */
    id: string,
    /**
     * Retry key for this replacement at its expected policy revision.
     */
    idempotencyKey: string,
    requestBody: UpdateFinancialBudgetRequest,
  }): CancelablePromise<FinancialBudgetResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/billing/budgets/{id}',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        422: `financial_budget_activation_unavailable; the existing policy revision is unchanged.`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Tombstone a policy while retaining its immutable revision history.
   * Requires admin scope, session MFA and Idempotency-Key. expected_revision prevents stale deletion. Does not change account status or unrelated holds.
   * @returns FinancialBudgetResponse Tombstone and incremented revision.
   * @throws ApiError
   */
  public static deleteFinancialBudget({
    id,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Stable identity of an account-owned budget policy.
     */
    id: string,
    /**
     * Retry identity for this policy tombstone operation.
     */
    idempotencyKey: string,
    requestBody: DeleteFinancialBudgetRequest,
  }): CancelablePromise<FinancialBudgetResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/billing/budgets/{id}',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Page through immutable budget revisions, including deleted policies.
   * Requires usage:read and session MFA. Ownership is checked before history is read. Use next_revision as after_revision for continuation; a final full page may be followed by an empty page.
   * @returns FinancialBudgetHistoryResponse Revision page in ascending order.
   * @throws ApiError
   */
  public static listFinancialBudgetRevisions({
    id,
    afterRevision,
    limit = 100,
  }: {
    /**
     * Budget identity whose immutable revision audit is being listed.
     */
    id: string,
    /**
     * Exclusive revision cursor; use the previous response's next_revision.
     */
    afterRevision?: number,
    /**
     * Maximum number of immutable revision records in this page.
     */
    limit?: number,
  }): CancelablePromise<FinancialBudgetHistoryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/billing/budgets/{id}/revisions',
      path: {
        'id': id,
      },
      query: {
        'after_revision': afterRevision,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Preview a scoped budget and its workload consequences without writes.
   * Requires usage:read and session MFA. Uses the current UTC usage period.
   * Validates account ownership and authoritative workload eligibility.
   * Reports known attributed cost, coverage, affected and continuing targets.
   * Net usage shares the account allowance once; strict scoped policies use
   * gross compute. Rejecting traffic does not stop background or idle compute.
   * A broad strict policy must suspend every covered workload; selective
   * preview/background actions cannot cap continuing production spending.
   * Environment targets are discovered from current live deployment scopes.
   * No policies, holds, decisions, dispatches or instances are written.
   * Enforcement remains unavailable while owner integrations and native
   * lifecycle acceptance are pending, as reported by enforcement_ready=false.
   *
   * @returns FinancialBudgetPreviewResponse Financial observation, workload consequences and enforcement readiness.
   * @throws ApiError
   */
  public static previewFinancialBudget({
    requestBody,
  }: {
    requestBody: FinancialBudgetPreviewRequest,
  }): CancelablePromise<FinancialBudgetPreviewResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/billing/budgets/preview',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
