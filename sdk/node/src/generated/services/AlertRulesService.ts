/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AlertDeliveryResponse } from '../models/AlertDeliveryResponse.js';
import type { AlertPresetResponse } from '../models/AlertPresetResponse.js';
import type { AlertRuleResponse } from '../models/AlertRuleResponse.js';
import type { CreateAlertRuleRequest } from '../models/CreateAlertRuleRequest.js';
import type { EnableAlertPresetRequest } from '../models/EnableAlertPresetRequest.js';
import type { RotateAlertRuleSecretResponse } from '../models/RotateAlertRuleSecretResponse.js';
import type { TestAlertPresetResponse } from '../models/TestAlertPresetResponse.js';
import type { UpdateAlertRuleRequest } from '../models/UpdateAlertRuleRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class AlertRulesService {
  /**
   * List alert rules visible at this app.
   * Returns both app-scoped rules (app_id == <slug>) and
   * account-wide rules (app_id == ""). Account-wide rules
   * apply to every app on the account.
   *
   * @returns AlertRuleResponse Alert rules on the account, filtered to those visible at this app.
   * @throws ApiError
   */
  public static listAlertRules({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<AlertRuleResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/alerts',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Create an alert rule.
   * Plaintext webhook_secret arrives in the body, is sealed
   * via the host age recipient, and never appears in a
   * response. webhook_url is SSRF-guarded at write time
   * (loopback + metadata ranges denied).
   *
   * @returns AlertRuleResponse The new alert rule (carries the masked secret).
   * @throws ApiError
   */
  public static createAlertRule({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Alert rule payload. See CreateAlertRuleRequest.
     */
    requestBody: CreateAlertRuleRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AlertRuleResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/alerts',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        401: `code: unauthorized`,
        402: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        404: `code: not_found`,
        409: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Fetch one alert rule by id.
   * @returns AlertRuleResponse The alert rule.
   * @throws ApiError
   */
  public static getAlertRule({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<AlertRuleResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/alerts/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Partial-update an alert rule.
   * Every field is optional. metric cannot cross families
   * (e.g. error_rate_pct → failed_invocations) — returns
   * 400 alert_rule_invalid.
   *
   * @returns AlertRuleResponse The updated alert rule.
   * @throws ApiError
   */
  public static updateAlertRule({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: UpdateAlertRuleRequest,
  }): CancelablePromise<AlertRuleResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/alerts/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        401: `code: unauthorized`,
        402: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Delete an alert rule.
   * @returns void
   * @throws ApiError
   */
  public static deleteAlertRule({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/alerts/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Mint a new webhook HMAC secret.
   * Server-mints a 32-byte secret, base64-encodes it, and
   * overwrites the row's sealed ciphertext in place. The
   * plaintext is NEVER returned in the response — the body
   * carries the masked constant + rotated_at only.
   *
   * @returns RotateAlertRuleSecretResponse Rotation succeeded.
   * @throws ApiError
   */
  public static rotateAlertRuleSecret({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<RotateAlertRuleSecretResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/alerts/{id}/rotate-secret',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List recent alert_deliveries rows for one rule.
   * Returns the most-recent alert_deliveries rows for the rule,
   * newest-first. The default (include_test=false) hides test
   * rows; the operator pane is reachable via ?include_test=true.
   * IDOR-safe: a 404 is returned when the rule is on another
   * account (same posture as GET /v1/apps/{slug}/alerts/{id}).
   *
   * @returns AlertDeliveryResponse Recent deliveries, newest-first.
   * @throws ApiError
   */
  public static listAlertRuleDeliveries({
    slug,
    id,
    includeTest = false,
    limit = 50,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * When true, also surface rows written by Dispatcher.DispatchTest
     * (the customer-facing "send test alert" path). Default false
     * hides test rows so the customer's recent-deliveries pane is
     * not polluted by every "send test alert" click. Operators can
     * flip the toggle for post-mortems; the production read stays
     * index-only via the partial index
     * alert_deliveries_rule_fired_production_idx.
     *
     */
    includeTest?: boolean,
    /**
     * Max rows to return. Clamped to 100.
     */
    limit?: number,
  }): CancelablePromise<Array<AlertDeliveryResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/alerts/{id}/deliveries',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'include_test': includeTest,
        'limit': limit,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List the 8-row alert-preset catalog.
   * The catalog is small (8 rows in PR-A) so no pagination.
   * Rows whose enabled_in_catalog=false are returned with the
   * flag set so the dashboard can render "coming soon" — the
   * enable endpoint rejects them with 400 alert_preset_disabled.
   * Rows whose minimum_plan is above the caller's plan are
   * returned with enabled_in_catalog unchanged so the dashboard
   * can render an "upgrade to <plan>" hint per row.
   *
   * @returns AlertPresetResponse The catalog.
   * @throws ApiError
   */
  public static listAlertPresets(): CancelablePromise<Array<AlertPresetResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/alert-presets',
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
   * Instantiate a preset as an alert rule.
   * Clones the catalog row into a real alert_rules row the
   * caller owns from then on. The (metric, comparison,
   * threshold, window_spec, default_cooldown_minutes)
   * sextuple is pre-filled server-side; the caller supplies
   * only webhook_url + webhook_secret (the delivery channel)
   * and optional cooldown_minutes / enabled overrides.
   *
   * Pre-loadApp gates fire in this order: 404 on missing
   * preset → 400 alert_preset_disabled on disabled-in-catalog
   * → 402 plan_alert_presets_not_allowed on below-minimum-plan
   * → 400 alert_preset_invalid on body shape → 400
   * image_egress_denied on the SSRF egress guard → 402
   * plan_alert_rules_not_allowed on the per-plan cap → 403
   * plan_alert_rule_quota on the per-app / per-account cap.
   *
   * @returns AlertRuleResponse The instantiated alert rule (carries the masked secret).
   * @throws ApiError
   */
  public static enableAlertPreset({
    slug,
    name,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Preset name (catalog key — see `listAlertPresets`).
     */
    name: string,
    requestBody: EnableAlertPresetRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AlertRuleResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/alert-presets/{name}/enable',
      path: {
        'slug': slug,
        'name': name,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        401: `code: unauthorized`,
        402: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        404: `code: not_found`,
        409: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Form-POST sibling of enableAlertPreset for the dashboard.
   * Receives application/x-www-form-urlencoded payload from the
   * preset-grid form. Coerces the (webhook_url, webhook_secret)
   * pair into the same EnableAlertPresetRequest body the JSON
   * sibling expects, runs the same plan-tier gate, then 302-redirects
   * to /apps/{slug}?just_enabled={rule_id}. The web-cookie auth
   * path is sufficient — no MFA challenge (the JSON sibling
   * requires MFA via the public-auth middleware).
   *
   * @returns void
   * @throws ApiError
   */
  public static dashboardEnableAlertPreset({
    slug,
    name,
    formData,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Preset name (catalog key — same value the JSON sibling accepts at /v1/apps/{slug}/alert-presets/{name}/enable).
     */
    name: string,
    formData: EnableAlertPresetRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/dashboard/apps/{slug}/alert-presets/{name}/enable',
      path: {
        'slug': slug,
        'name': name,
      },
      formData: formData,
      mediaType: 'application/x-www-form-urlencoded',
      errors: {
        302: `Redirect to /apps/{slug}?just_enabled={rule_id}.`,
        400: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        401: `code: unauthorized`,
        402: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Send a synthetic test alert to the instantiated rule's webhook.
   * Test-fires the customer's instantiated alert_preset rule
   * against the webhook URL they configured at enable-time.
   * The synthetic Event body carries `payload.test = true`
   * so the customer's verifier can branch on the discriminator
   * (skip the production alert-write path, log to a quieter
   * channel, etc.) and a synthetic `observed` value JUST PAST
   * the preset's threshold — `threshold × 1.01` for `gt`
   * comparisons, `threshold × 0.99` for `lt` (a naive
   * threshold × 1.01 for `lt` would land on the wrong side of
   * the threshold; the handler's branch-swap is load-bearing).
   *
   * Pre-loadApp gates fire in this order: 404 on missing
   * preset → 400 alert_preset_disabled on disabled-in-catalog
   * → 402 plan_alert_presets_not_allowed on below-minimum-plan
   * → 404 preset_not_enabled when the customer has not yet
   * instantiated this preset for this app → 502 webhook
   * delivery failed after retry exhaustion.
   *
   * @returns TestAlertPresetResponse The test alert was delivered (any 2xx from the
   * customer's receiver). Note: 2xx-from-the-receiver is
   * the contract — the dispatcher's retry budget may span
   * multiple attempts before reporting success.
   *
   * @throws ApiError
   */
  public static testAlertPreset({
    slug,
    name,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Preset name (catalog key — same value the `listAlertPresets` and `enableAlertPreset` endpoints accept).
     */
    name: string,
    requestBody?: any,
  }): CancelablePromise<TestAlertPresetResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/alert-presets/{name}/test',
      path: {
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        401: `code: unauthorized`,
        402: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
        502: `Webhook delivery failed after the dispatcher's retry
        budget (5 attempts, 2s/8s/32s/128s backoff). The audit
        log entry (\`alert_preset.test_sent\`) carries the
        \`delivery_status_code\` + \`delivery_attempts\` for the
        investigator; the customer's receiver likely rejected
        the request, was unreachable, or returned 5xx.
        `,
      },
    });
  }
  /**
   * Form-POST sibling of testAlertPreset for the dashboard.
   * Receives the per-card "Send test alert" form submission
   * from the preset grid on /dashboard/apps/{slug}. No body
   * fields — the instantiated rule already carries the
   * webhook URL + secret. On success 302-redirects to
   * /apps/{slug}?test_alert=ok; on 4xx/5xx 302-redirects to
   * /apps/{slug}?test_alert=error so the template's flash
   * banner can render. Web-cookie auth is sufficient — no
   * MFA challenge.
   *
   * @returns void
   * @throws ApiError
   */
  public static dashboardTestAlertPreset({
    slug,
    name,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Preset name (catalog key — same value the JSON sibling accepts at /v1/apps/{slug}/alert-presets/{name}/test).
     */
    name: string,
    requestBody?: any,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/dashboard/apps/{slug}/alert-presets/{name}/test',
      path: {
        'slug': slug,
        'name': name,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        302: `Redirect to /apps/{slug}?test_alert={ok|error}.`,
        400: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        401: `code: unauthorized`,
        402: `code: alert_rule_invalid | plan_alert_rules_not_allowed | plan_alert_rule_quota | image_egress_denied`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
