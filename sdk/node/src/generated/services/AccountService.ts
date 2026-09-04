/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountDeletionResponse } from '../models/AccountDeletionResponse.js';
import type { AccountEgressAllowlistExtraResponse } from '../models/AccountEgressAllowlistExtraResponse.js';
import type { AccountExportResponse } from '../models/AccountExportResponse.js';
import type { AccountResponse } from '../models/AccountResponse.js';
import type { AccountSLOResponse } from '../models/AccountSLOResponse.js';
import type { ChangePlanRequest } from '../models/ChangePlanRequest.js';
import type { RaiseOverageCapRequest } from '../models/RaiseOverageCapRequest.js';
import type { SetAccountEgressAllowlistExtraRequest } from '../models/SetAccountEgressAllowlistExtraRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class AccountService {
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        409: `code: conflict`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
   * `usage:read` scope + MFA.
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
