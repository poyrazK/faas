/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountDeletionResponse } from '../models/AccountDeletionResponse.js';
import type { AccountEgressAllowlistExtraResponse } from '../models/AccountEgressAllowlistExtraResponse.js';
import type { AccountExportResponse } from '../models/AccountExportResponse.js';
import type { AccountOverageCapResponse } from '../models/AccountOverageCapResponse.js';
import type { AccountRateLimitsResponse } from '../models/AccountRateLimitsResponse.js';
import type { AccountResponse } from '../models/AccountResponse.js';
import type { CapabilitiesResponse } from '../models/CapabilitiesResponse.js';
import type { ChangePlanRequest } from '../models/ChangePlanRequest.js';
import type { Preview } from '../models/Preview.js';
import type { PreviewList } from '../models/PreviewList.js';
import type { RaiseOverageCapRequest } from '../models/RaiseOverageCapRequest.js';
import type { SetAccountEgressAllowlistExtraRequest } from '../models/SetAccountEgressAllowlistExtraRequest.js';
import type { UpdateAccountBillingInfoRequest } from '../models/UpdateAccountBillingInfoRequest.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
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
   * Set or clear the account's spend cap.
   * Sets accounts.overage_cap_cents in integer cents. Body shape:
   *
   * {"overage_cap_cents": <int|null>}
   *
   * Pass `null` (or omit the field) to clear the cap (NULL).
   * Pass 0 to set "no overage allowed." Passing a positive integer
   * sets the monthly ceiling. The migration CHECK constraint pins
   * `overage_cap_cents IS NULL OR overage_cap_cents >= 0`; a
   * negative value is rejected at the API validator before the
   * store ever sees it, returning 400 `validation_failed`.
   *
   * Once current-month overage meets/exceeds the cap, the scheduler refuses
   * new wakes with `code: admission_refused` (HTTP 402). The cap is
   * account-self-scoped (admin scope required) and the response
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
   * List the platform previews you can join.
   * Self-serve platform previews: the previews an
   * operator opened to self-serve, plus any you joined that have since
   * closed, so you can still leave them. `eligible` says whether your plan
   * may join; `enabled` whether the preview is on for you now, for any
   * reason.
   *
   * @returns PreviewList The previews.
   * @throws ApiError
   */
  public static listPreviews(): CancelablePromise<PreviewList> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/previews',
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
   * Join a platform preview.
   * Opts your account in. Joining twice changes nothing. A preview never
   * grants a plan entitlement, and an operator can still exclude an
   * account.
   *
   * @returns Preview The preview after joining.
   * @throws ApiError
   */
  public static joinPreview({
    key,
  }: {
    /**
     * Platform preview key, as GET /v1/previews lists it.
     */
    key: string,
  }): CancelablePromise<Preview> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/previews/{key}',
      path: {
        'key': key,
      },
      errors: {
        401: `code: unauthorized`,
        403: `\`preview_plan_not_eligible\`: your plan cannot join this preview.`,
        404: `\`preview_not_offered\`: not a preview open to self-serve.`,
        409: `\`preview_full\` (with limit and observed) or a concurrent change.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Leave a platform preview.
   * Removes your account's opt-in. Leaving is always allowed, even after the preview closed; leaving one you never joined changes nothing.
   * @returns Preview The preview after leaving.
   * @throws ApiError
   */
  public static leavePreview({
    key,
  }: {
    /**
     * Platform preview key, as GET /v1/previews lists it.
     */
    key: string,
  }): CancelablePromise<Preview> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/previews/{key}',
      path: {
        'key': key,
      },
      errors: {
        401: `code: unauthorized`,
        404: `\`preview_not_offered\`: not a platform preview.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read the per-account egress allowlist extra budget.
   * Returns the per-account additive budget on top of the
   * plan's `apps.egress_allowlist` cap. The plan cap (Pro 16 / Scale 64) is
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
