/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountDeadLetterEventsResponse } from '../models/AccountDeadLetterEventsResponse.js';
import type { AccountDeadLetterPurgeResponse } from '../models/AccountDeadLetterPurgeResponse.js';
import type { AccountDeadLetterReplayAllResponse } from '../models/AccountDeadLetterReplayAllResponse.js';
import type { AccountDeletionResponse } from '../models/AccountDeletionResponse.js';
import type { AccountEgressAllowlistExtraResponse } from '../models/AccountEgressAllowlistExtraResponse.js';
import type { AccountExportResponse } from '../models/AccountExportResponse.js';
import type { AccountOverageCapResponse } from '../models/AccountOverageCapResponse.js';
import type { AccountRateLimitsResponse } from '../models/AccountRateLimitsResponse.js';
import type { AccountResponse } from '../models/AccountResponse.js';
import type { AccountSLOResponse } from '../models/AccountSLOResponse.js';
import type { AccountTraceLookupResponse } from '../models/AccountTraceLookupResponse.js';
import type { BillingConnectionResponse } from '../models/BillingConnectionResponse.js';
import type { BillingProvider } from '../models/BillingProvider.js';
import type { BillingRunItemListResponse } from '../models/BillingRunItemListResponse.js';
import type { BillingRunListResponse } from '../models/BillingRunListResponse.js';
import type { BillingRunResponse } from '../models/BillingRunResponse.js';
import type { BillingRunScheduleResponse } from '../models/BillingRunScheduleResponse.js';
import type { CapabilitiesResponse } from '../models/CapabilitiesResponse.js';
import type { ChangePlanRequest } from '../models/ChangePlanRequest.js';
import type { CreateBillingRunRequest } from '../models/CreateBillingRunRequest.js';
import type { DeadLetterEvent } from '../models/DeadLetterEvent.js';
import type { PutBillingConnectionRequest } from '../models/PutBillingConnectionRequest.js';
import type { PutBillingRunScheduleRequest } from '../models/PutBillingRunScheduleRequest.js';
import type { RaiseOverageCapRequest } from '../models/RaiseOverageCapRequest.js';
import type { SetAccountEgressAllowlistExtraRequest } from '../models/SetAccountEgressAllowlistExtraRequest.js';
import type { StatementInvoiceListResponse } from '../models/StatementInvoiceListResponse.js';
import type { UpdateAccountBillingInfoRequest } from '../models/UpdateAccountBillingInfoRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class AccountService {
  /**
   * Return feature maturity and plan availability.
   * Returns the canonical, account-scoped capability registry. `maturity`
   * describes the product lifecycle state; `plans` lists entitled plans
   * and `enabled` resolves that list for the caller. The registry is read-only and
   * is suitable for CLIs, dashboards, and deployment preflight checks.
   *
   * @returns CapabilitiesResponse Customer-visible capability registry.
   * @throws ApiError
   */
  public static getCapabilities(): CancelablePromise<CapabilitiesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/capabilities',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Capability registry unavailable; retry with backoff.`,
      },
    });
  }
  /**
   * Whoami.
   * Returns the calling account, its plan, and quota limits.
   * @returns AccountResponse The calling account: id, plan, limits snapshot, current-month usage, and total app count.
   * @throws ApiError
   */
  public static getAccount(): CancelablePromise<AccountResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account',
      errors: {
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Stage account deletion (30-day grace).
   * Stages deletion. The account becomes `deleted_pending` for 30 days
   * during which the customer can call `POST /v1/account/restore`. After
   * the grace period, all rows are GC'd.
   *
   * @returns AccountDeletionResponse Staged.
   * @throws ApiError
   */
  public static deleteAccount({
    idempotencyKey,
  }: {
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AccountDeletionResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/account',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        409: `code: account_deletion_confirm_required | account_deletion_pending | account_not_restorable`,
      },
    });
  }
  /**
   * List dead-letter events for the account.
   * @returns AccountDeadLetterEventsResponse Account-wide unified dead-letter events, newest first.
   * @throws ApiError
   */
  public static listAccountDeadLetterEvents({
    limit = 20,
    before,
  }: {
    /**
     * Maximum number of rows to return; capped at 200.
     */
    limit?: number,
    /**
     * Cursor — the last id from the previous page (omit for the first page).
     */
    before?: string,
  }): CancelablePromise<AccountDeadLetterEventsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/dlq',
      query: {
        'limit': limit,
        'before': before,
      },
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Purge dead-letter events from the account ledger.
   * @returns AccountDeadLetterPurgeResponse Number of account-wide ledger events purged.
   * @throws ApiError
   */
  public static purgeAccountDeadLetterEvents({
    limit = 20,
    idempotencyKey,
  }: {
    /**
     * Maximum number of rows to return; capped at 200.
     */
    limit?: number,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AccountDeadLetterPurgeResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/account/dlq',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Replay pending dead-letter events for the account.
   * @returns AccountDeadLetterReplayAllResponse Number of account-wide events accepted for replay.
   * @throws ApiError
   */
  public static replayAllAccountDeadLetterEvents({
    limit = 20,
    idempotencyKey,
  }: {
    /**
     * Maximum number of rows to return; capped at 200.
     */
    limit?: number,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AccountDeadLetterReplayAllResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/dlq:replay_all',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Get one dead-letter event for the account.
   * @returns DeadLetterEvent The account-owned dead-letter event.
   * @throws ApiError
   */
  public static getAccountDeadLetterEvent({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<DeadLetterEvent> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/dlq/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Purge one account-owned dead-letter event.
   * @returns void
   * @throws ApiError
   */
  public static deleteAccountDeadLetterEvent({
    id,
    idempotencyKey,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/account/dlq/{id}',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Replay one account-owned dead-letter event atomically.
   * @returns DeadLetterEvent Account dead-letter replay accepted.
   * @throws ApiError
   */
  public static replayAccountDeadLetterEvent({
    id,
    idempotencyKey,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<DeadLetterEvent> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/dlq/{id}/replay',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read the account's current deploy rate window.
   * Returns the durable account-wide deploy count, plan limit, remaining admissions, and fixed-window reset time.
   * @returns AccountRateLimitsResponse Current account rate limits.
   * @throws ApiError
   */
  public static getAccountRateLimits(): CancelablePromise<AccountRateLimitsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/rate-limits',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Look up one distributed trace across the account.
   * Returns retained request telemetry, bounded span evidence, and
   * durable invocation lifecycle rows linked by the platform trace id. Request
   * payloads and arbitrary invocation headers are never returned.
   *
   * @returns AccountTraceLookupResponse Account-scoped trace correlation result.
   * @throws ApiError
   */
  public static getAccountTrace({
    traceId,
    limit = 100,
  }: {
    /**
     * Canonical 32-character lowercase W3C trace identifier.
     */
    traceId: string,
    /**
     * Maximum durable invocation rows to include in the lookup.
     */
    limit?: number,
  }): CancelablePromise<AccountTraceLookupResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/traces/{trace_id}',
      path: {
        'trace_id': traceId,
      },
      query: {
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Change billing plan after provider confirmation.
   * Switch the account between `free`, `hobby`, `pro`, and `scale`. The
   * local account moves to a paid tier only after the configured billing
   * provider confirms payment. If a new subscription is required, the
   * `payment_required` response includes `checkout_url`; if an existing
   * subscription must be changed, it includes `billing_portal_url`.
   *
   * @returns AccountResponse The updated account profile after the plan change.
   * @throws ApiError
   */
  public static changePlan({
    requestBody,
  }: {
    /**
     * Plan change payload.
     */
    requestBody: ChangePlanRequest,
  }): CancelablePromise<AccountResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/account/plan',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        403: `code: email_verification_required — verify the account email before deploying code or changing billing settings.`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Update the account's invoice billing identity.
   * Updates the legal business name, billing address, and tax identifier
   * used for future provider-issued invoices. Omitted fields are kept;
   * an explicitly empty string clears a field. The audit event records
   * only changed field names, never the submitted values.
   *
   * @returns AccountResponse Updated account profile.
   * @throws ApiError
   */
  public static updateAccountBillingInfo({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: UpdateAccountBillingInfoRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AccountResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/account/billing',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
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
   * Account-wide SLO rollup (issue
   * Flat scalar SLO rollup for the authenticated account. The
   * same wire shape as the per-app endpoint without the
   * `app_id` / `app_slug` fields — the rollup is sum-based
   * across every app the account owns. `instance_hours` and
   * `gb_hours` are summed from `usage_minutes` over the
   * window; the per-app equivalent is the explicit
   * `/v1/apps/{slug}/slo` endpoint.
   *
   * `window` is the same closed vocabulary as the per-app
   * endpoint: `1h` | `24h` (default) | `7d`. Auth chain:
   * `usage:read` scope + MFA. The SLO rollup is a Hobby+
   * observability surface; Free accounts receive 402.
   *
   * On Prometheus failure the endpoint returns 200 with
   * zeroed fields and `source: "degraded: <reason>"`. When
   * Postgres is down but the PromQL pass succeeded, only
   * `instance_hours` / `gb_hours` are zeroed and `source` is
   * `"degraded: postgres unavailable"`.
   *
   * @returns AccountSLOResponse The account-wide SLO rollup.
   * @throws ApiError
   */
  public static getAccountSlo({
    window = '24h',
  }: {
    /**
     * Window for the account-wide SLO rollup. Default `24h`.
     */
    window?: '1h' | '24h' | '7d',
  }): CancelablePromise<AccountSLOResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/slo',
      query: {
        'window': window,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: plan_per_app_metrics_not_allowed — the account plan does not include per-app metrics or wake narratives; upgrade to Hobby or above.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Export full account data (GDPR).
   * Returns a JSON bundle containing every resource the account owns
   * (apps, deployments, builds, instances, usage, domains, crons, API
   * keys, app secrets) plus the GDPR audit trail. Available to
   * `deleted_pending` accounts so the customer can take a final export
   * during the 30-day grace window.
   *
   * @returns AccountExportResponse A bundled JSON document: the account itself plus every owned app, deployment, build, instance, usage record, domain, cron, API key, sealed-secret envelope, and the audit trail.
   * @throws ApiError
   */
  public static exportAccount({
    includeSecrets = true,
  }: {
    /**
     * If true (default), include the sealed-secret envelopes.
     */
    includeSecrets?: boolean,
  }): CancelablePromise<AccountExportResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/export',
      query: {
        'include_secrets': includeSecrets,
      },
      errors: {
        401: `code: unauthorized`,
      },
    });
  }
  /**
   * Restore a `deleted_pending` account.
   * Cancels a staged deletion. Returns 409 `account_not_restorable` if the 30-day grace has elapsed.
   * @returns AccountResponse Restored.
   * @throws ApiError
   */
  public static restoreAccount(): CancelablePromise<AccountResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/restore',
      errors: {
        401: `code: unauthorized`,
        409: `code: account_deletion_confirm_required | account_deletion_pending | account_not_restorable`,
      },
    });
  }
  /**
   * Describe the account's key for one billing provider.
   * @returns BillingConnectionResponse The connection; the key itself is never returned.
   * @throws ApiError
   */
  public static getBillingConnection({
    provider,
  }: {
    /**
     * Billing provider statements export to (ADR-1507).
     */
    provider: BillingProvider,
  }): CancelablePromise<BillingConnectionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/billing-connections/{provider}',
      path: {
        'provider': provider,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Store or replace the account's key for one billing provider.
   * Checks the key against the provider, then seals it at rest. Stripe
   * takes a restricted key (rk_live_… or rk_test_…) with read and write
   * access to invoices and invoice items; Paddle an API key
   * (pdl_live_apikey_… or pdl_sdbx_apikey_…) with transaction access; Lago
   * an API key and, for the EU region or a self-hosted Lago, api_url. Only
   * the last four characters, the mode and the Lago API URL are ever
   * returned.
   *
   * @returns BillingConnectionResponse The stored connection.
   * @throws ApiError
   */
  public static putBillingConnection({
    provider,
    requestBody,
  }: {
    /**
     * Billing provider statements export to (ADR-1507).
     */
    provider: BillingProvider,
    requestBody: PutBillingConnectionRequest,
  }): CancelablePromise<BillingConnectionResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/account/billing-connections/{provider}',
      path: {
        'provider': provider,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        422: `Not a key of this provider, a bad api_url, or the provider rejected the key.`,
        502: `The provider could not be reached (\`billing_provider_unavailable\`).`,
      },
    });
  }
  /**
   * Remove the account's key for one billing provider.
   * @returns void
   * @throws ApiError
   */
  public static deleteBillingConnection({
    provider,
  }: {
    /**
     * Billing provider statements export to (ADR-1507).
     */
    provider: BillingProvider,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/account/billing-connections/{provider}',
      path: {
        'provider': provider,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * List invoices exported from usage statements, with their payment status (ADR-1504).
   * @returns StatementInvoiceListResponse Exported invoices, newest first.
   * @throws ApiError
   */
  public static listStatementInvoices({
    status,
    cursor,
    limit,
  }: {
    /**
     * `unpaid` (draft, open or uncollectible), `overdue` (open and past due), or one payment status.
     */
    status?: 'unpaid' | 'overdue' | 'draft' | 'open' | 'paid' | 'void' | 'uncollectible' | 'deleted',
    /**
     * The `next_cursor` of the previous invoice page.
     */
    cursor?: string,
    /**
     * Invoices to return (1–200, default 50).
     */
    limit?: number,
  }): CancelablePromise<StatementInvoiceListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/statement-invoices',
      query: {
        'status': status,
        'cursor': cursor,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
      },
    });
  }
  /**
   * List the account's month-end billing runs, newest first.
   * @returns BillingRunListResponse Billing runs with their item counts.
   * @throws ApiError
   */
  public static listBillingRuns({
    limit,
  }: {
    /**
     * Runs to return (1–100, default 20).
     */
    limit?: number,
  }): CancelablePromise<BillingRunListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/billing-runs',
      query: {
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
      },
    });
  }
  /**
   * Bill one ended month for every customer (ADR-1503).
   * Starts a background run for one UTC calendar month that ended at least
   * an hour ago. For every app consumer and platform tenant with billable
   * usage, it drafts the month's statement, holds consumers whose ledger
   * misses requests telemetry saw (`hold_on_gaps`), finalizes it, and with
   * `export` set to stripe, paddle or lago exports each statement to that provider. Usage attributed to a
   * platform tenant is billed on the tenant's statement. One run per
   * account can be queued or running at a time.
   *
   * @returns BillingRunResponse The queued run; poll it or list its items for progress.
   * @throws ApiError
   */
  public static createBillingRun({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: CreateBillingRunRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<BillingRunResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/billing-runs',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        409: `Another run is in progress, or \`export\` names a provider without a connected key.`,
        422: `The month has not ended, or the options are invalid.`,
      },
    });
  }
  /**
   * Get a billing run and how many customers reached each outcome.
   * @returns BillingRunResponse The billing run.
   * @throws ApiError
   */
  public static getBillingRun({
    id,
  }: {
    /**
     * Billing run ID.
     */
    id: string,
  }): CancelablePromise<BillingRunResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/billing-runs/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * List what a billing run did for each customer.
   * @returns BillingRunItemListResponse One page of the run's items.
   * @throws ApiError
   */
  public static listBillingRunItems({
    id,
    outcome,
    cursor,
    limit,
  }: {
    /**
     * Billing run whose customers are listed.
     */
    id: string,
    /**
     * Only items with this outcome, such as `held` or `failed`.
     */
    outcome?: 'pending' | 'drafted' | 'finalized' | 'exported' | 'up_to_date' | 'held' | 'skipped' | 'failed',
    /**
     * The previous page's `next_cursor`.
     */
    cursor?: string,
    /**
     * Items to return (1–500, default 100).
     */
    limit?: number,
  }): CancelablePromise<BillingRunItemListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/billing-runs/{id}/items',
      path: {
        'id': id,
      },
      query: {
        'outcome': outcome,
        'cursor': cursor,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Get the account's monthly billing run schedule.
   * @returns BillingRunScheduleResponse The schedule and when it next starts a run.
   * @throws ApiError
   */
  public static getBillingRunSchedule(): CancelablePromise<BillingRunScheduleResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/billing-run-schedule',
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Start a billing run automatically after each month ends.
   * A new schedule starts with the current month: its run starts
   * `delay_hours` after the month ends. Replacing a schedule keeps its
   * next month. If a run is already in progress, the scheduled run
   * starts once it completes.
   *
   * @returns BillingRunScheduleResponse The saved schedule.
   * @throws ApiError
   */
  public static putBillingRunSchedule({
    requestBody,
  }: {
    requestBody: PutBillingRunScheduleRequest,
  }): CancelablePromise<BillingRunScheduleResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/account/billing-run-schedule',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        409: `\`export\` names a provider without a connected key.`,
        422: `Invalid schedule options or delay.`,
      },
    });
  }
  /**
   * Stop scheduled billing runs; a run already started continues.
   * @returns void
   * @throws ApiError
   */
  public static deleteBillingRunSchedule(): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/account/billing-run-schedule',
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read the account's monthly overage cap.
   * Returns integer cents, zero when no overage is allowed, or null when no cap is set.
   * @returns AccountOverageCapResponse The saved monthly overage cap.
   * @throws ApiError
   */
  public static getOverageCap(): CancelablePromise<AccountOverageCapResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/overage-cap',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        500: `Could not read the saved cap.`,
      },
    });
  }
  /**
   * Set or clear the account's spend cap (issue
   * Sets accounts.overage_cap_cents in integer cents. Body shape:
   *
   * {"overage_cap_cents": <int|null>}
   *
   * Pass `null` (or omit the field) to clear the cap (NULL).
   * Pass 0 to set "no overage allowed." Passing a positive integer
   * sets the monthly ceiling. The migration CHECK constraint at
   * `migrations/00054_account_credits.sql` pins
   * `overage_cap_cents IS NULL OR overage_cap_cents >= 0`; a
   * negative value is rejected at the apid validator before the
   * store ever sees it, returning 400 `validation_failed`.
   *
   * Once current-month overage meets/exceeds the cap, schedd refuses
   * new wakes with `code: admission_refused` (HTTP 402). The cap is
   * account-self-scoped (no admin scope required) and the response
   * is the post-update account state. Audit row
   * `overage.cap_changed` is emitted on every successful call.
   *
   * @returns AccountResponse The updated account.
   * @throws ApiError
   */
  public static raiseOverageCap({
    requestBody,
    idempotencyKey,
  }: {
    requestBody: RaiseOverageCapRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AccountResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/overage-cap',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
      },
    });
  }
  /**
   * Read the per-account egress allowlist extra budget.
   * Returns the per-account additive budget on top of the
   * plan's `apps.egress_allowlist` cap (issue #679 / PR-B /
   * ADR-082). The plan cap (Pro 16 / Scale 64) is
   * authoritative for the default case; the additive budget
   * lets admins raise one account's effective cap without
   * changing the plan default for everyone. `extra=0`
   * means "no override" — the plan cap is authoritative.
   *
   * Admin scope is required.
   *
   * @returns AccountEgressAllowlistExtraResponse Current extra + plan cap + global max extra.
   * @throws ApiError
   */
  public static getAccountEgressAllowlistExtra(): CancelablePromise<AccountEgressAllowlistExtraResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/egress_allowlist_extra',
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
   * Set the per-account egress allowlist extra budget.
   * Writes the per-account additive budget. `extra=0` clears
   * the override (the plan cap is authoritative again);
   * negative values or values above `max_extra` (1024) are
   * rejected with `account_egress_allowlist_extra_out_of_range`.
   * The PATCH emits an `account.egress_allowlist_extra_set`
   * audit row carrying `old_extra`, `new_extra`, `plan_cap`,
   * and `max_extra` so the dashboard can render the toggle
   * history.
   *
   * Admin scope + MFA are required.
   *
   * @returns AccountEgressAllowlistExtraResponse Override applied; the body echoes the new value.
   * @throws ApiError
   */
  public static setAccountEgressAllowlistExtra({
    requestBody,
  }: {
    requestBody: SetAccountEgressAllowlistExtraRequest,
  }): CancelablePromise<AccountEgressAllowlistExtraResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/account/egress_allowlist_extra',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid body (e.g. extra < 0 or extra > max_extra).`,
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
}
