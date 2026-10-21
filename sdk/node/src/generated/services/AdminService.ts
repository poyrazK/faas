/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountAbuseHoldAction } from '../models/AccountAbuseHoldAction.js';
import type { AccountAbuseHoldActionResponse } from '../models/AccountAbuseHoldActionResponse.js';
import type { AccountCreditResponse } from '../models/AccountCreditResponse.js';
import type { AdminRefundResponse } from '../models/AdminRefundResponse.js';
import type { AdminSetGithubWebhookSecretRequest } from '../models/AdminSetGithubWebhookSecretRequest.js';
import type { AdminSetGithubWebhookSecretResponse } from '../models/AdminSetGithubWebhookSecretResponse.js';
import type { AdminStatusEventCreateRequest } from '../models/AdminStatusEventCreateRequest.js';
import type { AdminStatusEventEditRequest } from '../models/AdminStatusEventEditRequest.js';
import type { AdminStatusEventUpdateRequest } from '../models/AdminStatusEventUpdateRequest.js';
import type { AdminStatusUpdateEditRequest } from '../models/AdminStatusUpdateEditRequest.js';
import type { BillingCatalogResponse } from '../models/BillingCatalogResponse.js';
import type { BillingPaddleOveragePreflightResponse } from '../models/BillingPaddleOveragePreflightResponse.js';
import type { BillingReconcileResponse } from '../models/BillingReconcileResponse.js';
import type { ConsumeInvoiceResponse } from '../models/ConsumeInvoiceResponse.js';
import type { DataRepairHealth } from '../models/DataRepairHealth.js';
import type { EgressFlowLogResponse } from '../models/EgressFlowLogResponse.js';
import type { GithubRecoveryRetryResponse } from '../models/GithubRecoveryRetryResponse.js';
import type { GithubRecoveryStatusResponse } from '../models/GithubRecoveryStatusResponse.js';
import type { ObsAlertCollectionHistory } from '../models/ObsAlertCollectionHistory.js';
import type { ObsAlertRecurrence } from '../models/ObsAlertRecurrence.js';
import type { ObsAlertResponseTimes } from '../models/ObsAlertResponseTimes.js';
import type { ObsAlertTriageHistory } from '../models/ObsAlertTriageHistory.js';
import type { ObsAlertTriageRequest } from '../models/ObsAlertTriageRequest.js';
import type { ObsAlertTriageResponse } from '../models/ObsAlertTriageResponse.js';
import type { ObsAlertWorkloadSummary } from '../models/ObsAlertWorkloadSummary.js';
import type { ObsHealthResponse } from '../models/ObsHealthResponse.js';
import type { ObsInputHealth } from '../models/ObsInputHealth.js';
import type { ObsNotificationHealth } from '../models/ObsNotificationHealth.js';
import type { ObsRecordingCoverage } from '../models/ObsRecordingCoverage.js';
import type { ObsRuleCoverage } from '../models/ObsRuleCoverage.js';
import type { ObsRuleHealthResponse } from '../models/ObsRuleHealthResponse.js';
import type { OperatorIntentAcceptedResponse } from '../models/OperatorIntentAcceptedResponse.js';
import type { OperatorIntentResponse } from '../models/OperatorIntentResponse.js';
import type { PublicStatusEvent } from '../models/PublicStatusEvent.js';
import type { RekeyProgress } from '../models/RekeyProgress.js';
import type { SweepStuckBuildsResponse } from '../models/SweepStuckBuildsResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class AdminService {
  /**
   * List public status events (admin-only).
   * Requires admin scope, operator allowlist membership, and MFA.
   * @returns PublicStatusEvent Status events, newest update first.
   * @throws ApiError
   */
  public static listAdminStatusEvents({
    faasSid,
    kind,
    active = false,
  }: {
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
    /**
     * Restrict results to incidents or maintenance.
     */
    kind?: 'incident' | 'maintenance',
    /**
     * Return only events that have not reached a terminal lifecycle state.
     */
    active?: boolean,
  }): CancelablePromise<Array<PublicStatusEvent>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/status/incidents',
      cookies: {
        'faas_sid': faasSid,
      },
      query: {
        'kind': kind,
        'active': active,
      },
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
   * Publish a public incident or maintenance event (admin-only).
   * Requires admin scope, operator allowlist membership, MFA, recent step-up authentication, and an Idempotency-Key.
   * @returns PublicStatusEvent Published event. Replays return the same public UUID.
   * @throws ApiError
   */
  public static createAdminStatusEvent({
    idempotencyKey,
    requestBody,
    faasSid,
  }: {
    /**
     * Stable caller-generated key used to replay this create safely.
     */
    idempotencyKey: string,
    requestBody: AdminStatusEventCreateRequest,
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<PublicStatusEvent> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/status/incidents',
      cookies: {
        'faas_sid': faasSid,
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
   * Append a public timeline update and lifecycle transition (admin-only).
   * Updates are append-only. Terminal incidents and maintenance cannot reopen.
   * @returns PublicStatusEvent Updated event with its complete chronological timeline.
   * @throws ApiError
   */
  public static updateAdminStatusEvent({
    publicId,
    idempotencyKey,
    requestBody,
    faasSid,
  }: {
    /**
     * Stable public UUID used by status permalinks.
     */
    publicId: string,
    /**
     * Stable caller-generated key used to replay this timeline update safely.
     */
    idempotencyKey: string,
    requestBody: AdminStatusEventUpdateRequest,
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<PublicStatusEvent> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/status/incidents/{public_id}/updates',
      path: {
        'public_id': publicId,
      },
      cookies: {
        'faas_sid': faasSid,
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Correct a published event title without changing its timeline order.
   * Requires an operator session. The event remains published and exposes edited_at; deletion is unsupported.
   * @returns PublicStatusEvent Corrected event with stable updated_at ordering and visible edited_at.
   * @throws ApiError
   */
  public static editAdminStatusEvent({
    publicId,
    requestBody,
    faasSid,
  }: {
    /**
     * Stable public UUID used by status permalinks.
     */
    publicId: string,
    requestBody: AdminStatusEventEditRequest,
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<PublicStatusEvent> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/admin/status/incidents/{public_id}',
      path: {
        'public_id': publicId,
      },
      cookies: {
        'faas_sid': faasSid,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
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
   * Correct a published timeline message without changing its posted time.
   * Requires an operator session. The entry remains in place and exposes edited_at; deletion is unsupported.
   * @returns PublicStatusEvent Corrected timeline with stable posted_at and visible edited_at.
   * @throws ApiError
   */
  public static editAdminStatusUpdate({
    publicId,
    updateId,
    requestBody,
    faasSid,
  }: {
    /**
     * Stable public UUID used by status permalinks.
     */
    publicId: string,
    /**
     * Public UUID of the timeline entry being corrected.
     */
    updateId: string,
    requestBody: AdminStatusUpdateEditRequest,
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<PublicStatusEvent> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/admin/status/incidents/{public_id}/updates/{update_id}',
      path: {
        'public_id': publicId,
        'update_id': updateId,
      },
      cookies: {
        'faas_sid': faasSid,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
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
   * Issue a positive-cents credit to an account (admin-only).
   * @returns AccountCreditResponse Credit issued. Returns the new credit row.
   * @throws ApiError
   */
  public static issueAccountCredit({
    id,
    requestBody,
  }: {
    /**
     * Target account UUID.
     */
    id: string,
    requestBody: {
      /**
       * Credit amount in EUR cents (integer).
       */
      cents: number;
      /**
       * Operator-supplied audit reason.
       */
      reason: string;
    },
  }): CancelablePromise<AccountCreditResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/accounts/{id}/credits',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: admin_required — call requires a Bearer with the admin scope AND an email in FAAS_ADMIN_EMAILS.`,
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
   * Place an abuse hold on an account (operator-only).
   * @returns AccountAbuseHoldActionResponse The account's hold after the action. `changed` is false when a hold was already in place.
   * @throws ApiError
   */
  public static placeAccountAbuseHold({
    id,
    requestBody,
  }: {
    /**
     * Account UUID whose abuse hold changes.
     */
    id: string,
    requestBody: AccountAbuseHoldAction,
  }): CancelablePromise<AccountAbuseHoldActionResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/accounts/{id}/abuse-hold',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: admin_required — placing a hold needs an operator session with MFA and recent step-up.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Release an account's abuse hold (operator-only).
   * @returns AccountAbuseHoldActionResponse The account's hold after the action. `changed` is false when no hold was set.
   * @throws ApiError
   */
  public static releaseAccountAbuseHold({
    id,
    requestBody,
  }: {
    /**
     * Account UUID whose abuse hold changes.
     */
    id: string,
    requestBody: AccountAbuseHoldAction,
  }): CancelablePromise<AccountAbuseHoldActionResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/admin/accounts/{id}/abuse-hold',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: admin_required — releasing a hold needs an operator session with MFA and recent step-up.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Search the egress flow log (operator-only).
   * Destination addresses and TCP ports tenant guests opened new flows to,
   * newest first. Filter by remote address or CIDR, account, and a
   * [from, to) window of at most the 30-day retention. Defaults to the
   * last 24 hours and 200 rows.
   *
   * @returns EgressFlowLogResponse Matching flow log rows.
   * @throws ApiError
   */
  public static listEgressFlows({
    remote,
    accountId,
    from,
    to,
    limit,
  }: {
    /**
     * Remote IP address or CIDR.
     */
    remote?: string,
    /**
     * Only flows from this account's instances.
     */
    accountId?: string,
    /**
     * Window start (inclusive); defaults to 24 hours before to.
     */
    from?: string,
    /**
     * Window end (exclusive); defaults to now.
     */
    to?: string,
    /**
     * Maximum rows; defaults to 200.
     */
    limit?: number,
  }): CancelablePromise<EgressFlowLogResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/egress-flows',
      query: {
        'remote': remote,
        'account_id': accountId,
        'from': from,
        'to': to,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: admin_required — reading the flow log needs an operator session with MFA.`,
      },
    });
  }
  /**
   * Refund a paid Polar invoice (admin-only).
   * `invoice_id` must identify a local Gregale invoice belonging to the
   * target account. The current public-release implementation supports
   * Polar order IDs and integer EUR cents. `Idempotency-Key` is required
   * and is sent to the provider unchanged (up to 255 characters).
   *
   * @returns AdminRefundResponse Refund accepted by the provider.
   * @throws ApiError
   */
  public static refundAccountInvoice({
    id,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Account UUID whose paid invoice will be refunded.
     */
    id: string,
    /**
     * Stable key for this refund operation.
     */
    idempotencyKey: string,
    requestBody: {
      /**
       * Local Gregale invoice UUID.
       */
      invoice_id: string;
      /**
       * Refund amount in EUR cents.
       */
      amount_cents: number;
      /**
       * Reason recorded with the money-moving operation.
       */
      reason: string;
    },
  }): CancelablePromise<AdminRefundResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/accounts/{id}/refunds',
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
        403: `code: admin_required or mfa_required — operator scope, email allowlist, and MFA gates apply.`,
        404: `code: not_found`,
        409: `Invoice is not refundable or has no paid amount/provider identity.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `The selected billing provider does not expose this refund surface.`,
        502: `Provider rejected the refund or returned an incomplete response.`,
      },
    });
  }
  /**
   * Set the per-tenant GitHub App webhook secret (admin-only).
   * @returns AdminSetGithubWebhookSecretResponse Secret persisted. The new upgraded_at/upgraded_by are returned for the audit trail.
   * @throws ApiError
   */
  public static setGithubWebhookSecret({
    requestBody,
  }: {
    requestBody: AdminSetGithubWebhookSecretRequest,
  }): CancelablePromise<AdminSetGithubWebhookSecretResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/github-webhook-secrets',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `Code admin_required — admin-scoped Bearer + email in FAAS_ADMIN_EMAILS allowlist. PR-D widens the scope to cover per-tenant webhook secret rotation; the table-level pg_notify side-effect (installation_id) is intentional and consumed by githubd.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Drain active credits FIFO against an invoice's overage (admin-only, MFA-gated).
   * @returns ConsumeInvoiceResponse Reducer ran (or replayed). consumed_cents is the floored integer cents drained against this invoice.
   * @throws ApiError
   */
  public static consumeInvoiceCredits({
    id,
  }: {
    /**
     * Invoice row UUID from GET /v1/invoices.
     */
    id: string,
  }): CancelablePromise<ConsumeInvoiceResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/invoices/{id}/consume-credits',
      path: {
        'id': id,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: admin_required — call requires an admin-scoped Bearer with the caller email in FAAS_ADMIN_EMAILS AND a verified MFA factor.`,
        404: `code: not_found`,
        409: `Invoice was imported from history without a verifiable historical Gregale plan; credit proration is not available.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read the cached Paddle price + product catalog (admin-only).
   * Returns the in-memory catalog snapshot from *paddle.Provider.
   * synced_at is the timestamp of the most recent successful
   * EnsurePlanProducts call; an empty string means no hydration
   * has run yet. On a Stripe deployment the handler returns 501
   * with code billing_op_unsupported — the type assertion at
   * the dispatcher fails and the surface is provider-scoped.
   *
   * @returns BillingCatalogResponse Catalog snapshot. Entries may be empty if no hydration has run.
   * @throws ApiError
   */
  public static listPaddleCatalog(): CancelablePromise<BillingCatalogResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/billing-paddle-catalog',
      errors: {
        401: `code: unauthorized`,
        403: `code: admin_required — GET /v1/admin/billing-paddle-catalog requires a Bearer with the admin scope AND an email in FAAS_ADMIN_EMAILS.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: billing_op_unsupported — active provider does not implement the paddle.OpProvider surface.`,
      },
    });
  }
  /**
   * Signal a Paddle catalog reset (admin-only).
   * Paddle's catalog is durable on the platform — the in-memory
   * reset is a no-op (returns nil immediately) and the handler
   * renders a 200 with empty entries so the CLI can print the
   * "delete products from the Paddle Dashboard, then call
   * sync" message. Future work (issue #279+) may add
   * merchant-side cleanup; this handler will then return 502
   * on SDK failure rather than 200.
   *
   * @returns BillingCatalogResponse Reset signal recorded. Always returns empty entries.
   * @throws ApiError
   */
  public static resetPaddleCatalog(): CancelablePromise<BillingCatalogResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/admin/billing-paddle-catalog',
      errors: {
        401: `code: unauthorized`,
        403: `code: admin_required — DELETE /v1/admin/billing-paddle-catalog requires a Bearer with the admin scope AND an email in FAAS_ADMIN_EMAILS.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: billing_op_unsupported — provider does not implement paddle.OpProvider.`,
      },
    });
  }
  /**
   * Force a Paddle catalog hydration (admin-only).
   * Idempotent: re-running on the same platform hits the
   * Status=active filter on ListProducts, finds existing
   * products/prices, and skips POST. Idempotency-Key middleware
   * replays the same 200 for a flaky-network retry so the SDK
   * round-trip is not re-issued. Returns the post-sync catalog.
   *
   * @returns BillingCatalogResponse Post-sync catalog. synced_at is "now" by construction.
   * @throws ApiError
   */
  public static syncPaddleCatalog(): CancelablePromise<BillingCatalogResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/billing-paddle-catalog/sync',
      errors: {
        401: `code: unauthorized`,
        403: `code: admin_required — POST /v1/admin/billing-paddle-catalog/sync requires a Bearer with the admin scope AND an email in FAAS_ADMIN_EMAILS.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: billing_op_unsupported — active provider does not advertise paddle.OpProvider; the type assertion at the dispatcher fails.`,
        502: `code: billing_sync_failed — Paddle SDK round-trip failed.`,
      },
    });
  }
  /**
   * Read the paddle_overage_dedupe schema probe (admin-only).
   * Operator-side guard for the Paddle overage pusher's
   * per-window claim state machine (migration 00041). Returns
   * which of the four new columns are present + the per-state
   * row counts so an operator can verify the meterd loop will
   * not crash on a 42703 (column missing) error before any
   * customer-facing push is attempted.
   *
   * Returned by the B4 pre-flight CLI subcommand. A response
   * with table_exists=true and any of has_window_start /
   * has_state / has_claimed_at / has_claimed_by=false means
   * migration 00041 was not (fully) applied. A response with
   * table_exists=false means migrations 00034 + 00041 are
   * both unapplied (the table has never been created).
   *
   * @returns BillingPaddleOveragePreflightResponse Schema probe result. Booleans reflect the current DB shape.
   * @throws ApiError
   */
  public static getBillingPaddleOveragePreflight(): CancelablePromise<BillingPaddleOveragePreflightResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/billing-paddle-overage/preflight',
      errors: {
        401: `code: unauthorized`,
        403: `code: admin_required — GET /v1/admin/billing-paddle-overage/preflight requires a Bearer with the admin scope AND an email in FAAS_ADMIN_EMAILS.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Run a single-account reconcile against the active billing Provider (admin-only).
   * Loads the account, calls billing.Provider.ReconcileUsage for
   * a rolling 30-day window [start, end). Stripe implements
   * this (ADR-049 §B.1); Paddle returns billing.ErrNotImplemented
   * and the handler maps to 501. The response surfaces the
   * SDK-returned mb_seconds total so an operator can diff
   * against the local usage_minutes sum.
   *
   * @returns BillingReconcileResponse Reconcile ran. mb_seconds is the SDK-returned total for [start, end).
   * @throws ApiError
   */
  public static reconcileAccount({
    id,
  }: {
    /**
     * Account UUID whose 30-day reconcile window the operator wants to inspect.
     */
    id: string,
  }): CancelablePromise<BillingReconcileResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/billing-reconcile/{id}',
      path: {
        'id': id,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: admin_required — POST /v1/admin/billing-reconcile/{id} requires a Bearer with the admin scope AND an email in FAAS_ADMIN_EMAILS.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        501: `code: billing_reconcile_unsupported — provider does not implement ReconcileUsage.`,
        502: `code: billing_reconcile_failed — SDK round-trip failed.`,
      },
    });
  }
  /**
   * Read the cumulative rekey walk progress (admin-only).
   * Returns the latest RekeyProgress snapshot the apid rekey
   * runner has written — either to the in-process atomic pointer
   * (memory-only mode) or to FAAS_REKEY_PROGRESS_FILE on disk.
   * Operators poll this endpoint to monitor the walk after a
   * host identity rotation; the response shape mirrors
   * rekey.RekeyProgress exactly so a future operator tool
   * (e.g. `gregale rekey status`) can decode without a parallel
   * type.
   *
   * `total` is the running count of rows observed so far; it can
   * grow as the walk paginates through (account_id, app_id, key)
   * order. `rekeyed` + `skipped` should approach `total` once
   * the walk drains. `failed` should stay at zero; a non-zero
   * value means the unseal step threw for at least one row —
   * the operationally safe recovery is `git rm
   * migrations*reserve_slot.sql`-style idempotent re-trigger
   * (toggle FAAS_REKEY_ENABLED and restart apid; the seen-set
   * inside Replayer dedupes already-done rows).
   *
   * When the runner is disabled (FAAS_REKEY_ENABLED unset), the
   * endpoint returns 503 with code `rekey_disabled` so an
   * operator can distinguish "no work yet" from "feature off".
   * If FAAS_REKEY_ENABLED=true is set but no host age identities
   * loaded (mfaIdentities() empty — typically FAAS_HOST_AGE_IDENTITY_PATH
   * unset), the endpoint returns 503 with the distinct code
   * `rekey_no_identities` (PR #825 follow-up); this avoids the
   * misleading "set FAAS_REKEY_ENABLED and restart" detail when
   * the operator already opted in.
   *
   * @returns RekeyProgress Current rekey progress snapshot.
   * @throws ApiError
   */
  public static getRekeyProgress(): CancelablePromise<RekeyProgress> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/secrets/rekey-progress',
      errors: {
        401: `code: unauthorized`,
        403: `code: admin_required — GET /v1/admin/secrets/rekey-progress requires a Bearer with the admin scope AND an email in FAAS_ADMIN_EMAILS.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Runner not running. The detail distinguishes two cases
        via the \`code\` field:
        - \`rekey_disabled\` — FAAS_REKEY_ENABLED is unset on
        this apid; the background re-seal runner is not
        running. Set the env flag and restart apid to opt in.
        - \`rekey_no_identities\` — FAAS_REKEY_ENABLED=true but
        no host age identities loaded (FAAS_HOST_AGE_IDENTITY_PATH
        missing or empty). Set the identity path alongside
        the flag, then restart apid.
        `,
      },
    });
  }
  /**
   * Enqueue a force-park intent for a wedged live instance (admin-only).
   * Operator-side recovery primitive for instances wedged in
   * {RUNNING, WAKING, COLD_BOOTING} that the customer can't
   * wait for the idle reaper to handle. PR #1099 P2 redesign:
   * apid writes a row to `operator_intents` (PR #1099 P2.1)
   * and emits `pg_notify('operator_intent', …)`; schedd
   * (the ONLY writer to `instances` per CLAUDE.md §6.2) is
   * the sole consumer and dispatches via
   * `engine.ParkWithReason` so the `pkg/state/machine.go`
   * `CanTransition` guard fires. The handler returns 202
   * Accepted with an intent_id; the operator polls
   * GET /v1/admin/operator-intents/{id} for terminal state.
   *
   * `?confirm=true` is required as a tripwire against
   * operator fat-fingering. Optional `?reason=<slug>` defaults
   * to `operator_force_park`; values are clamped to the
   * `[a-z0-9_]{1,64}` shape.
   *
   * ADR-1524: requires an MFA-verified operator session with step-up in
   * the last five minutes, admin scope, the operator allowlist and same
   * origin. Bearer keys do not satisfy step-up. Requires one
   * Idempotency-Key (1-255 visible ASCII characters), an empty body,
   * exactly one confirm=true and at most one reason; other query
   * parameters are rejected. The key is scoped to the actor across all
   * three recovery routes. Intent, exact 202 response and attributed
   * request audit handoff commit atomically. Matching retries return
   * the original response before reading mutable target state, without
   * expiry; changed action, target or reason returns 409
   * operator_submission_conflict. Trace changes do not change identity.
   * An existing legacy recovery cache/reservation returns 409
   * operator_submission_legacy and requires reconciliation. Notification
   * after acceptance is best effort; the scheduler safety tick recovers
   * a lost notification.
   *
   * @returns OperatorIntentAcceptedResponse Intent, response receipt and request audit handoff committed; notification attempted. Poll `status_url` for terminal state.
   * @throws ApiError
   */
  public static postForceParkInstance({
    idempotencyKey,
    id,
    confirm,
    reason,
  }: {
    /**
     * Park request key. Reuse with the original target and reason to recover an uncertain submission. No receipt expiry.
     */
    idempotencyKey: string,
    /**
     * Instance UUID returned by /v1/apps/{slug}/instances or /v1/admin/obs/instances.
     */
    id: string,
    /**
     * Must be the literal string "true" — tripwire on force-park against operator fat-fingering.
     */
    confirm: 'true',
    /**
     * Audit-log slug. Default `operator_force_park`. Clamped to `[a-z0-9_]{1,64}`.
     */
    reason?: string,
  }): CancelablePromise<OperatorIntentAcceptedResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/instances/{id}/force-park',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'confirm': confirm,
        'reason': reason,
      },
      errors: {
        400: `Force-park validation: \`?confirm=true\` is missing or \`?reason=\` failed validation.`,
        401: `code: unauthorized`,
        403: `Force-park 403: code: admin_required — caller is not in the FAAS_ADMIN_EMAILS allowlist.`,
        404: `code: instance_not_found — no instance row with the supplied id.`,
        409: `code: instance_not_parkable — instance state is not in {RUNNING, WAKING, COLD_BOOTING}. Also operator_submission_conflict for changed parameters or operator_submission_legacy for an ambiguous pre-upgrade key.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Enqueue a force-cold-boot intent for an app's latest deployment (admin-only).
   * Operator-side recovery primitive for the case where the
   * live instance is fine but the snapshot backing the warm
   * tier is suspected to be the carrier of a customer-reported
   * wedge. Per ADR-005 ("snapshot of a wedged VM is a wedged
   * VM"), the recovery action is `MarkSnapshotStale` on the
   * deployment's latest warm + init snapshots — NOT a state-
   * machine transition. The instance row is NOT mutated;
   * the next customer Wake takes the cold-boot path through
   * `engine.go::usableSnapshotForWake` returning `haveSnap=false`.
   *
   * PR #1099 P2 redesign: apid writes a row to `operator_intents`
   * and emits `pg_notify('operator_intent', …)`; schedd is the
   * sole consumer and dispatches via
   * `engine.ForceColdBootNextWake`. The handler returns 202
   * Accepted with an intent_id; the operator polls
   * GET /v1/admin/operator-intents/{id} for the resolved
   * `snap_ids_marked_stale` (unknown at enqueue time).
   *
   * Requires `?confirm=true` as a tripwire. Optional
   * `?reason=<slug>` defaults to `operator_force_cold_boot`.
   *
   * ADR-1524: requires an MFA-verified operator session with step-up in
   * the last five minutes, admin scope, the operator allowlist and same
   * origin. Bearer keys do not satisfy step-up. Requires one
   * Idempotency-Key (1-255 visible ASCII characters), an empty body,
   * exactly one confirm=true and at most one reason; other query
   * parameters are rejected. The key is scoped to the actor across all
   * three recovery routes. Intent, exact 202 response and attributed
   * request audit handoff commit atomically. Matching retries return
   * the original response before reading mutable target state, without
   * expiry; changed action, target or reason returns 409
   * operator_submission_conflict. Trace changes do not change identity.
   * An existing legacy recovery cache/reservation returns 409
   * operator_submission_legacy and requires reconciliation. Notification
   * after acceptance is best effort; the scheduler safety tick recovers
   * a lost notification.
   *
   * @returns OperatorIntentAcceptedResponse Intent, response receipt and request audit handoff committed; notification attempted. Poll `status_url` for terminal state and `snap_ids_marked_stale`.
   * @throws ApiError
   */
  public static postForceColdBootApp({
    idempotencyKey,
    slug,
    confirm,
    reason,
  }: {
    /**
     * Cold-boot request key. Reuse with the original target and reason to recover an uncertain submission. No receipt expiry.
     */
    idempotencyKey: string,
    /**
     * App slug (e.g. "my-app"). Resolved once at acceptance by `created_at DESC, id DESC`; retries retain the original deployment.
     */
    slug: string,
    /**
     * Must be the literal string "true" — tripwire on force-cold-boot against operator fat-fingering.
     */
    confirm: 'true',
    /**
     * Audit-log slug. Default `operator_force_cold_boot`. Clamped to `[a-z0-9_]{1,64}`.
     */
    reason?: string,
  }): CancelablePromise<OperatorIntentAcceptedResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/apps/{slug}/force-cold-boot',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'confirm': confirm,
        'reason': reason,
      },
      errors: {
        400: `Force-cold-boot validation: \`?confirm=true\` is missing or \`?reason=\` failed validation.`,
        401: `code: unauthorized`,
        403: `Force-cold-boot 403: code: admin_required — caller is not in the FAAS_ADMIN_EMAILS allowlist.`,
        404: `code: app_not_found — no app with the supplied slug; OR code: deployment_not_found — app has no deployments to force-cold-boot.`,
        409: `operator_submission_conflict for changed parameters; operator_submission_legacy for an ambiguous pre-upgrade request key.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Enqueue a force-restart intent for a wedged RUNNING instance (admin-only).
   * Operator-side recovery primitive for instances wedged in
   * {RUNNING} that the customer can't wait for the idle
   * reaper to handle AND whose snapshot is suspected to be
   * the carrier of the wedge. Composes the two earlier
   * primitives: kill the instance (force-park) AND flip the
   * deployment's latest warm + init snapshots stale
   * (force-cold-boot). Per ADR-005 ("snapshot of a wedged
   * VM is a wedged VM"), the recovery action is destroy +
   * snap-stale so the next Wake is a guaranteed cold boot.
   *
   * PR #1105 (P2d follow-on to PR #1099): apid writes a row
   * to `operator_intents` (kind = `force_restart`, CHECK
   * widened by migrations/00446) and emits
   * `pg_notify('operator_intent', …)`; schedd (the ONLY
   * writer to `instances` per CLAUDE.md §6.2) is the sole
   * consumer and dispatches via `engine.ForceRestart` so the
   * `pkg/state/machine.go` `CanTransition` guard fires on the
   * locked re-read. The handler returns 202 Accepted with an
   * intent_id; the operator polls
   * GET /v1/admin/operator-intents/{id} for terminal state
   * and `snap_ids_marked_stale`.
   *
   * Gate is intentionally TIGHTER than force-park's
   * ({RUNNING, WAKING, COLD_BOOTING}): force-restart only
   * acts on RUNNING instances because the engine's
   * state-machine validation at pkg/sched/engine.go:5299
   * rejects non-RUNNING states as
   * `state.ErrInstanceNotRunning` (a wedged WAKING /
   * COLD_BOOTING instance's nodeID may be empty or its
   * destroy path may race with the Wake). Operators targeting
   * WAKING / COLD_BOOTING instances get 409
   * `instance_not_restartable` with no intent row written.
   *
   * `?confirm=true` is required as a tripwire against
   * operator fat-fingering. Optional `?reason=<slug>` defaults
   * to `operator_force_restart`; values are clamped to the
   * `[a-z0-9_]{1,64}` shape.
   *
   * ADR-1524: requires an MFA-verified operator session with step-up in
   * the last five minutes, admin scope, the operator allowlist and same
   * origin. Bearer keys do not satisfy step-up. Requires one
   * Idempotency-Key (1-255 visible ASCII characters), an empty body,
   * exactly one confirm=true and at most one reason; other query
   * parameters are rejected. The key is scoped to the actor across all
   * three recovery routes. Intent, exact 202 response and attributed
   * request audit handoff commit atomically. Matching retries return
   * the original response before reading mutable target state, without
   * expiry; changed action, target or reason returns 409
   * operator_submission_conflict. Trace changes do not change identity.
   * An existing legacy recovery cache/reservation returns 409
   * operator_submission_legacy and requires reconciliation. Notification
   * after acceptance is best effort; the scheduler safety tick recovers
   * a lost notification.
   *
   * @returns OperatorIntentAcceptedResponse Force-restart 202: intent, response receipt and request audit handoff committed; notification attempted. Poll `status_url` for terminal state and `snap_ids_marked_stale`.
   * @throws ApiError
   */
  public static postForceRestartInstance({
    idempotencyKey,
    id,
    confirm,
    reason,
  }: {
    /**
     * Restart request key. Reuse with the original target and reason to recover an uncertain submission. No receipt expiry.
     */
    idempotencyKey: string,
    /**
     * Force-restart target. Instance UUID returned by /v1/apps/{slug}/instances or /v1/admin/obs/instances.
     */
    id: string,
    /**
     * Must be the literal string "true" — tripwire on force-restart against operator fat-fingering.
     */
    confirm: 'true',
    /**
     * Audit-log slug. Default `operator_force_restart`. Clamped to `[a-z0-9_]{1,64}`.
     */
    reason?: string,
  }): CancelablePromise<OperatorIntentAcceptedResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/instances/{id}/force-restart',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'confirm': confirm,
        'reason': reason,
      },
      errors: {
        400: `Force-restart validation: \`?confirm=true\` is missing or \`?reason=\` failed validation.`,
        401: `code: unauthorized`,
        403: `Force-restart 403: code: admin_required — caller is not in the FAAS_ADMIN_EMAILS allowlist.`,
        404: `Force-restart 404: code: instance_not_found — no instance row with the supplied id.`,
        409: `Force-restart 409: code: instance_not_restartable — instance state is not RUNNING. WAKING / COLD_BOOTING / PARKED / STOPPED all return this code without writing an intent row. Also operator_submission_conflict for changed parameters or operator_submission_legacy for an ambiguous pre-upgrade key.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read the current state of an operator intent (admin-only).
   * Returns the row written by the 202 Accepted response of
   * POST /v1/admin/instances/{id}/force-park, POST
   * /v1/admin/instances/{id}/force-restart, or POST
   * /v1/admin/apps/{slug}/force-cold-boot. Status is one of
   * "pending" | "running" | "succeeded" | "failed" |
   * "cancelled". SnapIDsMarkedStale is populated on terminal
   * status for force_cold_boot and force_restart intents
   * (warm + init tiers walked). On failure, Error carries
   * the bounded dispatch error message (1 KB cap).
   *
   * @returns OperatorIntentResponse Current state of the intent.
   * @throws ApiError
   */
  public static getOperatorIntent({
    id,
  }: {
    /**
     * Operator intent UUID returned in the 202 Accepted body (`intent_id`).
     */
    id: string,
  }): CancelablePromise<OperatorIntentResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/operator-intents/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `getOperatorIntent 403: code: admin_required — caller is not in the FAAS_ADMIN_EMAILS allowlist.`,
        404: `code: operator_intent_not_found — no operator intent with the supplied id.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Inspect prospective collection outcomes and missing-observation intervals.
   * Requires operator read scope, MFA and the operator allowlist. Accepts one
   * optional nonempty since timestamp, default 24 hours and maximum 30 days,
   * and an optional details=true or details=false flag.
   * Reads prospective immutable collection outcomes from one read-only
   * snapshot with a five-second budget. Success commits atomically with
   * retained episode changes; failures contain closed sanitized reason codes.
   * Each outcome supplies at most 60 seconds of collection cadence evidence,
   * ending sooner at the next outcome. Missing intervals, periods before
   * recording began, expired evidence and unrecordable outages remain unknown.
   * These intervals are not continuous availability or proof of expected
   * rule inventory, metric coverage or complete incident history.
   * Microsecond duration partitions cover the full half-open [since, as_of)
   * window exactly. Counts include outcomes at since and as_of; a retained
   * carry-in outcome can contribute duration without increasing those counts.
   * Outcomes before the 30-day retention horizon or after as_of are excluded.
   * More than 100000 read outcomes returns 400 requiring a narrower since.
   * Totals precede a newest-first preview of at most 200 coalesced intervals
   * with explicit truncation. Current disabled configuration does not erase
   * historical evidence. With details=true, also returns up to 20 newest failed
   * attempts inside [since, as_of] from the same snapshot, separately from
   * coalesced intervals. Every failure counts toward the complete total,
   * including those without diagnostics. Recorded rejection diagnostics
   * describe the exact bounded source snapshot at attempted_at, never a
   * later live rule-health read. Complete rule/group counts precede up to
   * 10 unhealthy rule/group items and 16 KiB of compact diagnostic JSON.
   * Missing older diagnostics and unreadable sources remain unknown.
   * Observed issues do not prove the underlying root cause or which issue
   * triggered the collector's first rejection. No raw query, evaluation
   * error, label, absolute path or arbitrary runbook URL is retained.
   * No lifecycle or triage writes occur on GET.
   *
   * @returns ObsAlertCollectionHistory Exact cadence coverage totals and a bounded newest-first collection interval preview.
   * @throws ApiError
   */
  public static getObsAlertCollectionHistory({
    since,
    details = false,
  }: {
    /**
     * Inclusive collection-outcome lower bound for a prospective retained coverage window.
     */
    since?: string,
    /**
     * Include a bounded newest-first preview of failed attempts and their captured rejection diagnostics; accepts exactly true or false once.
     */
    details?: boolean,
  }): CancelablePromise<ObsAlertCollectionHistory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/collection-history',
      query: {
        'since': since,
        'details': details,
      },
      errors: {
        400: `Invalid collection-history query or read exceeding 100000 outcomes; narrow since.`,
        401: `code: unauthorized`,
        403: `Collection-history read denied by operator scope or MFA or allowlist policy.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Collection history could not be read within its bounded storage deadline.`,
      },
    });
  }
  /**
   * Diagnose current alert-rule evaluation health.
   * Requires operator read scope, MFA and the operator allowlist. Accepts no
   * query parameters. Assesses the complete bounded Prometheus alert-rule
   * snapshot under a three-second deadline and returns at most 200 metadata
   * items, unhealthy first. Totals cover all loaded rules before preview
   * truncation. An empty loaded-rule snapshot is unavailable, never healthy.
   * Failed, stale, unavailable, clock-invalid and structurally invalid rules
   * remain explicit; pending/firing counts are null for unhealthy rules.
   * Reported state is the last reported rule state, not proof of recovery.
   * Group names and file basenames are bounded safe text; absolute paths,
   * queries, raw evaluation errors, labels and annotations are omitted.
   * Invalid identity text is omitted and opaque hashes remain available.
   * No collection, history or triage writes occur. This current read does
   * not establish the cause of a past collection failure or completeness
   * of an expected deployed rule inventory. Source errors return HTTP 200
   * with unavailable state and null totals. Disabled configuration is explicit.
   *
   * @returns ObsRuleHealthResponse Loaded-rule assessment with complete totals and a bounded diagnostic preview.
   * @throws ApiError
   */
  public static getObsRuleHealth(): CancelablePromise<ObsRuleHealthResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/rule-health',
      errors: {
        400: `Alert-rule diagnostics do not accept query parameters.`,
        401: `code: unauthorized`,
        403: `Rule assessment denied by operator scope or MFA or allowlist policy.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Compare loaded alert definitions with an explicitly selected expected set.
   * Requires operator read scope, MFA and the operator allowlist. Without
   * expected_set, returns unknown with available versioned set metadata and
   * does not read Prometheus. Selection is per request and never establishes
   * deployment intent. gregale-core-v1 expects faas.rules.yml alerts;
   * gregale-full-v1 also expects bridge.rules.yml, pg_backup.rules.yml and
   * data_placement.yaml alerts. Both are minimum sets; additional rules are
   * allowed and counted. The embedded catalog revision identifies the exact
   * expected definitions shipped in this apid binary.
   * A complete bounded Prometheus alert-rule read and comparison share a
   * three-second deadline. Compares canonical PromQL syntax, for,
   * keep_firing_for and rule labels. Identity uses file basename, group and
   * alert name; directory relocation is ignored and basename/group collisions
   * are ambiguous. Missing fields and unattributed identities remain unknown.
   * A complete empty source is degraded with known missing rules.
   * Definitions matching the selected set are covered, independently of
   * evaluation, metric input, recording rule or notification health. Group
   * intervals, offsets, annotations and source directories are not compared.
   * Totals precede a 200-item preview ordered ambiguous, changed, missing,
   * unknown, matching, with opaque identity hashes breaking ties. Raw queries,
   * labels, errors and absolute paths are omitted. Unselected or unreadable
   * evidence returns HTTP 200 with unknown state and null totals. This read
   * never changes collection, history or triage state.
   *
   * @returns ObsRuleCoverage Explicit expectation metadata with complete comparison totals or an unknown reason.
   * @throws ApiError
   */
  public static getObsRuleCoverage({
    expectedSet,
  }: {
    /**
     * Select exactly one known minimum expectation set; no automatic selection occurs.
     */
    expectedSet?: 'gregale-core-v1' | 'gregale-full-v1',
  }): CancelablePromise<ObsRuleCoverage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/rule-coverage',
      query: {
        'expected_set': expectedSet,
      },
      errors: {
        400: `Unknown or empty or repeated expected_set or another query parameter.`,
        401: `code: unauthorized`,
        403: `Rule coverage read denied by operator scope or MFA or allowlist policy.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Inspect critical telemetry inputs for an explicitly selected role.
   * Requires operator read scope, MFA and the operator allowlist. Without an
   * explicit expected_role, returns unknown with choices and reads no source.
   * Both roles expect at least one apid, gatewayd (public gateway) and schedd
   * scrape target. Single-box also expects vmmd. Control-plane adds vmmd,
   * gatewayd-internal and schedd-compute counts from fresh apid discovery.
   * Static counts are minimums, not a complete intended inventory. Compute
   * counts do not prove target identity membership. The default Prometheus
   * collision mapping job=apid plus exported_job selects discovery evidence.
   * Missing, stale, conflicting or invalid discovery stays unknown. A fresh
   * zero registry is legitimate; omitted invalid discovery targets degrade.
   * Pins strict Prometheus queries to one time under a three-second budget
   * with at most six concurrent fixed job readers. Compares original sample
   * timestamps, distinguishing fresh up, down, stale and missing minimum
   * targets, with unexpected compute counts reported separately. Checks one
   * vetted always-instantiated daemon readiness gauge per job only on fresh
   * up targets, preserving per-target gaps. Uses a ten-minute lookback at
   * fifteen-second steps; freshness is 120 seconds. Gauge value zero is a
   * valid input and does not imply ready. Other metrics and rule evaluation
   * or notification health are unassessed. A known zero expectation with no
   * observed targets makes metric checks not applicable. Failures remain
   * unknown without cached values. Returns aggregate counts and closed codes,
   * excluding target addresses, labels, queries, URLs and raw errors. No writes.
   *
   * @returns ObsInputHealth Independently scoped scrape expectations and critical gauge sample evidence.
   * @throws ApiError
   */
  public static getObsInputHealth({
    expectedRole,
  }: {
    /**
     * Operator-selected minimum role expectation; this does not discover deployment intent.
     */
    expectedRole?: 'single-box-v1' | 'control-plane-v1',
  }): CancelablePromise<ObsInputHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/input-health',
      query: {
        'expected_role': expectedRole,
      },
      errors: {
        400: `Unknown or empty or repeated expected_role or another input health query key.`,
        401: `code: unauthorized`,
        403: `Input health read denied by operator scope or MFA or allowlist policy.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Compare expected recording definitions and their catalog alert references.
   * Requires operator read scope, MFA and the operator allowlist. Select a
   * minimum expected file set explicitly with expected_set. Core selects
   * faas.rules.yml; full also selects bridge.rules.yml, pg_backup.rules.yml
   * and data_placement.yaml. Without selection, returns unknown with available
   * metadata and makes no Prometheus request. Catalog presence is not
   * deployment intent. This recording catalog has its own exact revision.
   * Reads one complete bounded /api/v1/rules?type=record source and compares
   * canonical expressions and rule labels under a shared three-second budget.
   * Omitted recording labels mean empty per the Prometheus wire contract;
   * explicit null labels or unsupported definitions remain unknown. Identity
   * uses file basename, group and recording name. Duplicate candidates or
   * basename/group collisions are ambiguous. Additional valid identities are
   * allowed. A complete empty source has known missing expected definitions.
   * Definition totals precede a 200-item preview ordered ambiguous, changed,
   * missing, unknown, matching and then identity hash. Recording comparisons
   * only report expression or labels as changed fields.
   * Each item includes at most five syntactically dependent expected alerts,
   * including transitive recording chains, with known-reference count and truncation.
   * Dependency metadata binds the exact expected alert catalog revision.
   * Nonexact metric selectors, duplicate producer names and reachable cycles
   * leave explicit mapping uncertainty; its alert preview is also capped at
   * five with full counts. Known references can coexist with uncertain links.
   * Dependencies describe selected expected catalog syntax, not current
   * deployed alert definitions, series presence, execution impact or cause.
   * Covered definitions do not prove evaluation or input or notification
   * health. Group intervals, offsets and annotations are outside comparison.
   * Source failures return unknown with null totals; selected catalog mapping
   * remains independently available. Raw queries, labels, errors, source URLs
   * and absolute paths are omitted. No collection, history or triage writes.
   *
   * @returns ObsRecordingCoverage Recording definition comparison and independently scoped expected alert references.
   * @throws ApiError
   */
  public static getObsRecordingCoverage({
    expectedSet,
  }: {
    /**
     * Select exactly one known minimum file-set expectation for recording definitions and expected alert references.
     */
    expectedSet?: 'gregale-core-v1' | 'gregale-full-v1',
  }): CancelablePromise<ObsRecordingCoverage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/recording-coverage',
      query: {
        'expected_set': expectedSet,
      },
      errors: {
        400: `Unknown or empty or repeated expected_set or another recording coverage query key.`,
        401: `code: unauthorized`,
        403: `Recording coverage read denied by operator scope or MFA or allowlist policy.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Inspect alert notification pipeline evidence.
   * Requires operator read scope, MFA and the operator allowlist. Accepts no
   * query parameters. Reads bounded Alertmanager alert status and Prometheus
   * observations under one shared three-second deadline. Signals distinguish
   * fresh zero values from missing, invalid, stale or failed evidence.
   * Integration counters cover all receivers, including watchdog traffic;
   * counter presence does not imply that an integration is configured.
   * increases over ten minutes are extrapolated and can be fractional.
   * Healthy signal state describes evidence quality. Overall degraded means
   * an observed failure, dropped notification, unhealthy target or reload,
   * zero queue capacity or current queue pressure above 80 percent. Partial
   * means some evidence is unavailable. This read never confirms delivery
   * of a specific episode, human receipt, or external watchdog receipt.
   * Receiver configuration, addresses, webhook credentials and silence
   * comments are never read or returned. A disabled or invalidly configured
   * local Alertmanager reader is explicit. Source failures produce a partial
   * HTTP 200 observation with null unavailable values.
   *
   * @returns ObsNotificationHealth Independent notification signals and integration counters with evidence quality.
   * @throws ApiError
   */
  public static getObsNotificationHealth(): CancelablePromise<ObsNotificationHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/notification-health',
      errors: {
        400: `Notification health does not accept query parameters.`,
        401: `code: unauthorized`,
        403: `Notification pipeline observation denied by operator scope or MFA or allowlist.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Diagnose repeated retained alert activations and observed recovery gaps.
   * Requires operator read scope, MFA and the operator allowlist. Reads one
   * retained activation cohort between since and as_of inclusive, default
   * 24 hours and maximum 30 days. Groups by rule definition and exact series;
   * changed rules and different resources never share a recurrence sequence.
   * Repeat activations equal episodes minus one within each group. Durations
   * measure activation to confirmed resolution observation. Recovery gaps
   * compare adjacent activations only when the previous confirmed resolution
   * precedes or equals the next activation; unknown recovery, overlapping
   * observations and invalid clocks are counted separately. Minimum and
   * nearest-rank median use valid whole-second samples; absent samples are
   * null. This does not establish exact recovery time, continuous collection,
   * lifetime recurrence, false positives or notification volume.
   * Cohorts above 1000 episodes return 400 requiring a narrower since.
   * Complete totals precede a preview of at most 200 series ranked by repeat
   * count descending, latest activation descending, rule key then series ID.
   * Each preview includes at most five recent episodes newest first with
   * explicit truncation. One read-only database snapshot has a five-second
   * budget. Current collection and retention metadata do not reconstruct
   * expected inventory or per-rule coverage gaps. Prospective cadence evidence
   * accompanies this report; its own 100000-outcome bound also requires a
   * narrower since on overflow. Reads perform no lifecycle or triage writes.
   *
   * @returns ObsAlertRecurrence Complete bounded recurrence totals and a ranked series preview with evidence coverage.
   * @throws ApiError
   */
  public static getObsAlertRecurrence({
    since,
  }: {
    /**
     * Inclusive recurrence activation lower bound; defaults to 24 hours before as_of.
     */
    since?: string,
  }): CancelablePromise<ObsAlertRecurrence> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/incidents/platform_alert/recurrence',
      query: {
        'since': since,
      },
      errors: {
        400: `Invalid recurrence window or query or cohort exceeding 1000 episodes; narrow since.`,
        401: `code: unauthorized`,
        403: `Recurrence read denied by operator scope or MFA or allowlist policy.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Recurrence storage snapshot could not be read within its deadline.`,
      },
    });
  }
  /**
   * Measure retained alert acknowledgment and observed resolution timing.
   * Reads retained platform alert episodes activated between since and as_of,
   * inclusive. The default window is 24 hours with a maximum of 30 days.
   * Receipt coverage must prove the complete revision sequence before first
   * acknowledgment is included. Invalid timestamps are excluded from samples.
   * Unknown and unobserved outcomes remain in explicit cohort denominators.
   * Resolution measures confirmed rule recovery rather than actual repair.
   * Discrete nearest-rank percentiles use whole-second valid samples only.
   * Cohorts above 1000 episodes or 10000 matching receipts return 400 and
   * require a narrower since; results are never silently sampled. Reads
   * share one database snapshot and a five-second query budget. Retention
   * and collection metadata do not guarantee a complete historical window.
   * Prospective cadence coverage is read in the same snapshot. Its separate
   * 100000-outcome read bound also returns 400 requiring a narrower since.
   *
   * @returns ObsAlertResponseTimes Retained-cohort timing totals and bounded severity/component aggregates.
   * @throws ApiError
   */
  public static getObsAlertResponseTimes({
    since,
  }: {
    /**
     * Inclusive activation-cohort lower bound; defaults to 24 hours before as_of.
     */
    since?: string,
  }): CancelablePromise<ObsAlertResponseTimes> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/incidents/platform_alert/response-times',
      query: {
        'since': since,
      },
      errors: {
        400: `Invalid timing query or cohort above the bounded read capacity; narrow since.`,
        401: `code: unauthorized`,
        403: `Timing read denied by operator scope or MFA or allowlist policy.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Response timing storage could not be read within its deadline.`,
      },
    });
  }
  /**
   * Summarize retained platform alert workload and observation freshness.
   * Requires operator read authorization, MFA and the operator allowlist.
   * Counts all physically retained platform alert episodes in one database
   * snapshot, including expired rows awaiting retention cleanup. There is
   * no look-back window, pagination or query filter. Groups are exact
   * intersections of status, severity, acknowledgment, ownership and snooze.
   * Needs-attention includes unsnoozed unresolved episodes that are either
   * unacknowledged or unowned, including unknown observations. Collection
   * failure, stale rules and disabled collection cannot resolve an alert.
   * Counts are null before the first successful collection. Age is elapsed
   * time since episode activation, not time since assignment or acknowledgment.
   * SQL failure returns unavailable rather than an empty workload.
   *
   * @returns ObsAlertWorkloadSummary Exact retained-alert workload with bounded dimension groups and oldest evidence links.
   * @throws ApiError
   */
  public static getObsAlertWorkloadSummary(): CancelablePromise<ObsAlertWorkloadSummary> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/incidents/platform_alert/summary',
      errors: {
        400: `Query parameters are unsupported for this fixed scope.`,
        401: `code: unauthorized`,
        403: `Summary read denied by operator scope, MFA or allowlist.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Workload storage could not be read within the summary budget.`,
      },
    });
  }
  /**
   * Read revision-bounded triage receipts for one retained alert episode.
   * Requires operator read authorization, MFA and the operator allowlist.
   * Initial reads accept an immutable episode ID or a legacy series alias.
   * The response identifies the canonical episode. Cursors pin that episode
   * and the initial upper revision for 15 minutes; an alias moving to a
   * recurrence invalidates an earlier cursor. Entries are ordered by
   * descending revision even if receipt clocks move backwards. Each page
   * is a consistent database read, but retention can change between pages.
   * Coverage reports missing receipts without claiming why they disappeared.
   * History excludes actor identities and raw audit payloads.
   *
   * @returns ObsAlertTriageHistory Sanitized triage page with whole-snapshot receipt coverage.
   * @throws ApiError
   */
  public static getObsAlertTriageHistory({
    id,
    limit = 25,
    cursor,
  }: {
    /**
     * Retained episode hash or legacy series alias resolving to its latest episode.
     */
    id: string,
    /**
     * Maximum receipts on this page; changing the limit between pages is allowed.
     */
    limit?: number,
    /**
     * Opaque next_cursor from this episode history; expires 15 minutes after the first page.
     */
    cursor?: string,
  }): CancelablePromise<ObsAlertTriageHistory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/incidents/platform_alert/{id}/triage-history',
      path: {
        'id': id,
      },
      query: {
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `Invalid bounds, cursor, episode binding or expired cursor.`,
        401: `code: unauthorized`,
        403: `Operator read scope, MFA or allowlist rejected.`,
        404: `No retained episode exists for this reference.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Receipt storage or integrity could not be verified within the read budget.`,
      },
    });
  }
  /**
   * Update human triage on one immutable retained alert episode.
   * Requires an allowlisted operator session, MFA with recent step-up,
   * trusted browser origin and Idempotency-Key. Bearer keys cannot mutate.
   * Series aliases cannot be mutated. Every accepted action increments the
   * episode revision and commits with its audit entry. Claims cannot take
   * over another operator's ownership. Snooze affects inbox triage only;
   * observed firing/resolved/unknown status and notification delivery remain
   * independent. Obtain the current revision from incident evidence.
   *
   * @returns ObsAlertTriageResponse Updated workflow state, independent of observed alert status.
   * @throws ApiError
   */
  public static mutateObsAlertTriage({
    id,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Immutable retained episode ID; a series alias cannot be mutated.
     */
    id: string,
    /**
     * Unique request key used to replay an identical mutation receipt.
     */
    idempotencyKey: string,
    requestBody: ObsAlertTriageRequest,
  }): CancelablePromise<ObsAlertTriageResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/obs/incidents/platform_alert/{id}/triage',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid action, revision, body or snooze deadline.`,
        401: `code: unauthorized`,
        403: `Operator session, MFA step-up, allowlist or origin rejected.`,
        404: `Immutable episode ID is not retained.`,
        409: `Revision changed or another operator owns the episode.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Required storage or audit write failed; no triage mutation committed.`,
      },
    });
  }
  /**
   * Meta-obs health snapshot — audit write rates, outcome-missing counts, trace_id completeness, alert firing count (admin-only).
   * Operator-side meta-observation endpoint. Composes a single
   * JSON snapshot from:
   *
   * - `apid_audit_log_write_total[5m]` / `apid_audit_log_write_failures_total[5m]`
   * — apid's own Prometheus counters.
   * - `audit_log_coverage_ratio_5m` — SQL trace coverage over the durable
   * audit_log table, including every audit kind.
   * - `SELECT kind, count(*) FROM operator_intents WHERE
   * status = 'running' AND started_at < now() - interval
   * '5 minutes' GROUP BY kind` — single SQL query.
   * - `SELECT kind, count(*) FILTER (WHERE trace_id IS NOT
   * NULL)::float / count(*) FROM events WHERE kind LIKE
   * 'operator.action.%' AND at > now() - interval '5
   * minutes' GROUP BY kind` — reads events (live), NOT
   * audit_log (FK-free post-deletion copy).
   * - A Prometheus count of firing alerts, excluding FaasWatchdog
   * and info-only alerts.
   *
   * Federation is out of scope (each daemon owns its own
   * /metrics); this endpoint is the local apid's view. Kinds
   * with zero rows in the SQL-derived fields are seeded to
   * 0 (counts) or 1.0 (ratios, vacuous truth) so the JSON
   * shape stays stable.
   *
   * @returns ObsHealthResponse Health snapshot. Every closed-set field is present.
   * @throws ApiError
   */
  public static getObsHealth(): CancelablePromise<ObsHealthResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/health',
      errors: {
        401: `code: unauthorized`,
        403: `obs health 403: code: admin_required — caller is not in the FAAS_ADMIN_EMAILS allowlist.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Flip every build row stuck in 'running' past the threshold to 'failed/timeout' (admin-only).
   * Operator-side recovery primitive for builder microVMs that
   * crashed (OOM, kernel panic, host reboot) and left their
   * `builds` row in 'running' indefinitely. Mirrors the
   * in-process reaper at pkg/builderd/reaper.go:48 — the
   * operator-facing endpoint is the manual escape hatch for
   * when the reaper's grace period is too long for an
   * incident.
   *
   * `?older_than=` is clamped to [1m, 60m] so a fat-fingered
   * "1ns" cannot sweep in-flight builds. Default 15m.
   *
   * Audit row: operator.action.reclaim_build with
   * account_id=NULL (fleet-level, not tenant-scoped), including
   * the normalized operator reason.
   *
   * @returns SweepStuckBuildsResponse Sweep complete. `swept_count` may be 0 when no rows match the threshold.
   * @throws ApiError
   */
  public static postSweepStuckBuilds({
    confirm,
    olderThan,
    reason = 'operator_reclaim_build',
  }: {
    /**
     * Must be the literal string "true" — tripwire on sweep-stuck against operator fat-fingering.
     */
    confirm: 'true',
    /**
     * Threshold duration. Clamped to [1m, 60m]. Default 15m.
     */
    olderThan?: string,
    /**
     * Optional durable audit reason. Lowercase letters, numbers, and underscores only.
     */
    reason?: string,
  }): CancelablePromise<SweepStuckBuildsResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/builds/sweep-stuck',
      query: {
        'confirm': confirm,
        'older_than': olderThan,
        'reason': reason,
      },
      errors: {
        400: `Sweep-stuck validation: \`?confirm=true\` is missing or \`?older_than=\` failed validation.`,
        401: `code: unauthorized`,
        403: `Sweep-stuck 403: code: admin_required — caller is not in the FAAS_ADMIN_EMAILS allowlist.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        500: `Store call failed (transient PG hiccup; retry with the same threshold).`,
      },
    });
  }
  /**
   * Observe active data repairs and retained rehearsal capacity.
   * Operator metadata from one primary-database snapshot. Summary counts
   * and oldest phase clocks cover the whole active set; only detail rows
   * are paginated. Source data, SQL and native ownership frames are excluded.
   * Requires admin scope, MFA and membership in FAAS_ADMIN_EMAILS.
   * A failed observation returns 503 instead of a healthy empty queue.
   * Responses use Cache-Control no-store and grant no recovery authority.
   *
   * @returns DataRepairHealth Global repair health and a bounded page of operator metadata.
   * @throws ApiError
   */
  public static getDataRepairHealth({
    limit = 64,
    cursor,
  }: {
    /**
     * Maximum detail rows; values outside 1 through 500 are rejected.
     */
    limit?: number,
    /**
     * Opaque next_cursor from the preceding response.
     */
    cursor?: string,
  }): CancelablePromise<DataRepairHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/ops/data-repairs',
      query: {
        'limit': limit,
        'cursor': cursor,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List operator-safe GitHub webhook and Check Run recovery queue items.
   * Returns bounded queue metadata from githubd. Webhook payloads and
   * installation credentials are deliberately excluded. Requires admin
   * scope, MFA, and membership in FAAS_ADMIN_EMAILS.
   *
   * @returns GithubRecoveryStatusResponse Bounded recovery queue projections.
   * @throws ApiError
   */
  public static getGithubRecoveryStatus({
    status = 'dead',
    limit = 100,
  }: {
    /**
     * Queue status to select; omit to list every status.
     */
    status?: 'pending' | 'processing' | 'succeeded' | 'dead',
    /**
     * Maximum rows returned from each queue.
     */
    limit?: number,
  }): CancelablePromise<GithubRecoveryStatusResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/ops/github/recovery',
      query: {
        'status': status,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Move one dead GitHub webhook delivery back to pending.
   * Strict operator-session mutation requiring a recent MFA step-up,
   * Idempotency-Key, explicit confirmation, and a durable reason. The
   * compare-and-swap write is owned by githubd and emits a trace-linked
   * operator.action.github_delivery_retry audit event.
   *
   * @returns GithubRecoveryRetryResponse Delivery moved back to pending.
   * @throws ApiError
   */
  public static retryGithubWebhookDelivery({
    id,
    confirm,
    reason,
  }: {
    /**
     * UUID of the dead webhook delivery to retry.
     */
    id: string,
    /**
     * Explicit acknowledgement that customer webhook work will be retried.
     */
    confirm: 'true',
    /**
     * Durable lowercase audit reason slug for the webhook retry.
     */
    reason: string,
  }): CancelablePromise<GithubRecoveryRetryResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/ops/github/deliveries/{id}/retry',
      path: {
        'id': id,
      },
      query: {
        'confirm': confirm,
        'reason': reason,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Move one dead GitHub Check Run update back to pending.
   * Strict operator-session mutation requiring a recent MFA step-up,
   * Idempotency-Key, explicit confirmation, and a durable reason. The
   * compare-and-swap write is owned by githubd and emits a trace-linked
   * operator.action.github_check_update_retry audit event.
   *
   * @returns GithubRecoveryRetryResponse Check Run update moved back to pending.
   * @throws ApiError
   */
  public static retryGithubCheckUpdate({
    id,
    confirm,
    reason,
  }: {
    /**
     * Deployment UUID whose dead Check Run update should be retried.
     */
    id: string,
    /**
     * Explicit acknowledgement that customer Check Run work will be retried.
     */
    confirm: 'true',
    /**
     * Durable lowercase audit reason slug for the Check Run retry.
     */
    reason: string,
  }): CancelablePromise<GithubRecoveryRetryResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/admin/ops/github/check-updates/{id}/retry',
      path: {
        'id': id,
      },
      query: {
        'confirm': confirm,
        'reason': reason,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
}
