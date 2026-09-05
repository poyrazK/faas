/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountCreditResponse } from '../models/AccountCreditResponse.js';
import type { AdminRefundResponse } from '../models/AdminRefundResponse.js';
import type { AdminSetGithubWebhookSecretRequest } from '../models/AdminSetGithubWebhookSecretRequest.js';
import type { AdminSetGithubWebhookSecretResponse } from '../models/AdminSetGithubWebhookSecretResponse.js';
import type { BillingCatalogResponse } from '../models/BillingCatalogResponse.js';
import type { BillingPaddleOveragePreflightResponse } from '../models/BillingPaddleOveragePreflightResponse.js';
import type { BillingReconcileResponse } from '../models/BillingReconcileResponse.js';
import type { ConsumeInvoiceResponse } from '../models/ConsumeInvoiceResponse.js';
import type { ObsAppDetailResponse } from '../models/ObsAppDetailResponse.js';
import type { ObsCapacityResponse } from '../models/ObsCapacityResponse.js';
import type { ObsHealthResponse } from '../models/ObsHealthResponse.js';
import type { ObsNodeDetailResponse } from '../models/ObsNodeDetailResponse.js';
import type { ObsNodeListResponse } from '../models/ObsNodeListResponse.js';
import type { ObsOverviewResponse } from '../models/ObsOverviewResponse.js';
import type { ObsTenant360Response } from '../models/ObsTenant360Response.js';
import type { ObsTenantActivityResponse } from '../models/ObsTenantActivityResponse.js';
import type { ObsTenantListResponse } from '../models/ObsTenantListResponse.js';
import type { OperatorIntentAcceptedResponse } from '../models/OperatorIntentAcceptedResponse.js';
import type { OperatorIntentResponse } from '../models/OperatorIntentResponse.js';
import type { RekeyProgress } from '../models/RekeyProgress.js';
import type { SweepStuckBuildsResponse } from '../models/SweepStuckBuildsResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class AdminService {
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
   * @returns OperatorIntentAcceptedResponse Intent row written + pg_notify emitted. Poll `status_url` for terminal state.
   * @throws ApiError
   */
  public static postForceParkInstance({
    id,
    confirm,
    reason,
  }: {
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
      query: {
        'confirm': confirm,
        'reason': reason,
      },
      errors: {
        400: `Force-park validation: \`?confirm=true\` is missing or \`?reason=\` failed validation.`,
        401: `code: unauthorized`,
        403: `Force-park 403: code: admin_required — caller is not in the FAAS_ADMIN_EMAILS allowlist.`,
        404: `code: instance_not_found — no instance row with the supplied id.`,
        409: `code: instance_not_parkable — instance state is not in {RUNNING, WAKING, COLD_BOOTING}.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
   * @returns OperatorIntentAcceptedResponse Intent row written + pg_notify emitted. Poll `status_url` for terminal state and `snap_ids_marked_stale`.
   * @throws ApiError
   */
  public static postForceColdBootApp({
    slug,
    confirm,
    reason,
  }: {
    /**
     * App slug (e.g. "my-app"). Resolved to the app's latest deployment by `created_at DESC`.
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
      query: {
        'confirm': confirm,
        'reason': reason,
      },
      errors: {
        400: `Force-cold-boot validation: \`?confirm=true\` is missing or \`?reason=\` failed validation.`,
        401: `code: unauthorized`,
        403: `Force-cold-boot 403: code: admin_required — caller is not in the FAAS_ADMIN_EMAILS allowlist.`,
        404: `code: app_not_found — no app with the supplied slug; OR code: deployment_not_found — app has no deployments to force-cold-boot.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
   * @returns OperatorIntentAcceptedResponse Force-restart 202: intent row written + pg_notify emitted. Poll `status_url` for terminal state and `snap_ids_marked_stale`.
   * @throws ApiError
   */
  public static postForceRestartInstance({
    id,
    confirm,
    reason,
  }: {
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
      query: {
        'confirm': confirm,
        'reason': reason,
      },
      errors: {
        400: `Force-restart validation: \`?confirm=true\` is missing or \`?reason=\` failed validation.`,
        401: `code: unauthorized`,
        403: `Force-restart 403: code: admin_required — caller is not in the FAAS_ADMIN_EMAILS allowlist.`,
        404: `Force-restart 404: code: instance_not_found — no instance row with the supplied id.`,
        409: `Force-restart 409: code: instance_not_restartable — instance state is not RUNNING. WAKING / COLD_BOOTING / PARKED / STOPPED all return this code without writing an intent row.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Meta-obs health snapshot — audit write rates, outcome-missing counts, trace_id completeness, alert firing count (admin-only).
   * Operator-side meta-observation endpoint. Composes a single
   * JSON snapshot from:
   *
   * - `audit_log_write_total[5m]` / `audit_log_write_failures_total[5m]` /
   * `audit_log_coverage_ratio_5m` — apid's own Prometheus
   * counters (PR #TBD / C5).
   * - `SELECT kind, count(*) FROM operator_intents WHERE
   * status = 'running' AND started_at < now() - interval
   * '5 minutes' GROUP BY kind` — single SQL query.
   * - `SELECT kind, count(*) FILTER (WHERE trace_id IS NOT
   * NULL)::float / count(*) FROM events WHERE kind LIKE
   * 'operator.action.%' AND at > now() - interval '5
   * minutes' GROUP BY kind` — reads events (live), NOT
   * audit_log (FK-free post-deletion copy).
   * - `count(ALERTS{alertstate="firing"})` — Prometheus
   * Alertmanager integration.
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Read the fleet operator KPI snapshot.
   * @returns ObsOverviewResponse Fleet counts, node health, and bounded failure buckets.
   * @throws ApiError
   */
  public static getOperatorObservabilityOverview(): CancelablePromise<ObsOverviewResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/overview',
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
   * Read fleet capacity and placement counters.
   * @returns ObsCapacityResponse Aggregate capacity snapshot with per-node headroom.
   * @throws ApiError
   */
  public static getOperatorCapacity(): CancelablePromise<ObsCapacityResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/capacity',
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
   * List tenants for the operator console.
   * @returns ObsTenantListResponse Cursor-paginated tenant inventory.
   * @throws ApiError
   */
  public static listOperatorTenants({
    limit = 200,
    cursor,
    includePii = false,
  }: {
    /**
     * Page size; capped at 500.
     */
    limit?: number,
    /**
     * Opaque pagination cursor from the previous page.
     */
    cursor?: string,
    /**
     * Opt-in email projection; every use is audited.
     */
    includePii?: boolean,
  }): CancelablePromise<ObsTenantListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/tenants',
      query: {
        'limit': limit,
        'cursor': cursor,
        'include_pii': includePii,
      },
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
   * Read the bounded tenant 360 view.
   * @returns ObsTenant360Response Tenant identity, apps, usage, and bounded billing summary.
   * @throws ApiError
   */
  public static getOperatorTenant360({
    id,
    month,
    includePii = false,
  }: {
    /**
     * Tenant (account) id.
     */
    id: string,
    /**
     * Usage month as YYYY-MM; defaults to the current month.
     */
    month?: string,
    /**
     * Opt-in email projection on the 360 view; every use is audited.
     */
    includePii?: boolean,
  }): CancelablePromise<ObsTenant360Response> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/tenants/{id}/360',
      path: {
        'id': id,
      },
      query: {
        'month': month,
        'include_pii': includePii,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Read safe tenant activity metadata.
   * @returns ObsTenantActivityResponse Invocation and audit metadata without request payloads or results.
   * @throws ApiError
   */
  public static getOperatorTenantActivity({
    id,
    limit = 50,
  }: {
    /**
     * Tenant (account) id whose activity to read.
     */
    id: string,
    /**
     * Max activity rows; capped at 200.
     */
    limit?: number,
  }): CancelablePromise<ObsTenantActivityResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/tenants/{id}/activity',
      path: {
        'id': id,
      },
      query: {
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List compute nodes with live utilization.
   * @returns ObsNodeListResponse Cursor-paginated node inventory.
   * @throws ApiError
   */
  public static listOperatorNodes({
    limit = 200,
    cursor,
    includeInactive = '0',
  }: {
    /**
     * Node page size; capped at 500.
     */
    limit?: number,
    /**
     * Opaque node-page cursor from the previous page.
     */
    cursor?: string,
    /**
     * Set to '1' to include nodes not accepting placements.
     */
    includeInactive?: '0' | '1',
  }): CancelablePromise<ObsNodeListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/nodes',
      query: {
        'limit': limit,
        'cursor': cursor,
        'include_inactive': includeInactive,
      },
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
   * Read the apps and instances placed on one node.
   * @returns ObsNodeDetailResponse Node health, workload placement, and drain safety.
   * @throws ApiError
   */
  public static getOperatorNodeDetail({
    name,
  }: {
    /**
     * Node name.
     */
    name: string,
  }): CancelablePromise<ObsNodeDetailResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/nodes/{name}/detail',
      path: {
        'name': name,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Read app, deployment, instance, and health details.
   * @returns ObsAppDetailResponse Safe workload and health projection for an app.
   * @throws ApiError
   */
  public static getOperatorAppDetail({
    id,
    range = '5m',
  }: {
    /**
     * App id.
     */
    id: string,
    /**
     * Metrics aggregation window.
     */
    range?: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d',
  }): CancelablePromise<ObsAppDetailResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/admin/obs/apps/{id}',
      path: {
        'id': id,
      },
      query: {
        'range': range,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
        500: `Store call failed (transient PG hiccup; retry with the same threshold).`,
      },
    });
  }
}
