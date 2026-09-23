/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppSecretListResponse } from '../models/AppSecretListResponse.js';
import type { AppSecretResponse } from '../models/AppSecretResponse.js';
import type { ListSecretsForAccountResponse } from '../models/ListSecretsForAccountResponse.js';
import type { PutAppSecretRequest } from '../models/PutAppSecretRequest.js';
import type { RotateAppSecretRequest } from '../models/RotateAppSecretRequest.js';
import type { RotateAppSecretResponse } from '../models/RotateAppSecretResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class SecretsService {
  /**
   * List sealed secrets on an app.
   * @returns AppSecretListResponse Sealed-secret envelopes on the app (plaintext never returned).
   * @throws ApiError
   */
  public static listSecrets({
    slug,
    scope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Env-var scope (ADR-090). A domain-valid slug (3..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (pre-PR-B behavior).
     *
     */
    scope?: string,
  }): CancelablePromise<AppSecretListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/secrets',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Set a sealed secret.
   * Seals the plaintext value against the host X25519 recipient and
   * persists the ciphertext. The plaintext never lands in PG. Existing
   * snapshots are invalidated; running processes retain their current
   * environment until a cold wake or `POST /restart?fresh=true`.
   *
   * @returns AppSecretResponse The stored sealed-secret envelope.
   * @throws ApiError
   */
  public static setSecret({
    slug,
    key,
    requestBody,
    scope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Secret key. Must start with a letter; A-Z, 0-9, underscore.
     */
    key: string,
    /**
     * Secret payload — key name + plaintext. Sealed at rest; plaintext never returned.
     */
    requestBody: PutAppSecretRequest,
    /**
     * Env-var scope (ADR-090). A domain-valid slug (3..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (pre-PR-B behavior).
     *
     */
    scope?: string,
  }): CancelablePromise<AppSecretResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/secrets/{key}',
      path: {
        'slug': slug,
        'key': key,
      },
      query: {
        'scope': scope,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `400 on PUT /v1/apps/{slug}/secrets/{key}?scope=... — any of {secret_invalid_key, env_scope_invalid, env_scope_reserved}.`,
        401: `code: unauthorized`,
        403: `code: plan_limit_secrets`,
        413: `code: secret_value_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Delete a sealed secret.
   * @returns void
   * @throws ApiError
   */
  public static deleteSecret({
    slug,
    key,
    scope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Secret key. Must start with a letter; A-Z, 0-9, underscore.
     */
    key: string,
    /**
     * Env-var scope (ADR-090). A domain-valid slug (3..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (pre-PR-B behavior).
     *
     */
    scope?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/secrets/{key}',
      path: {
        'slug': slug,
        'key': key,
      },
      query: {
        'scope': scope,
      },
      errors: {
        400: `400 on DELETE /v1/apps/{slug}/secrets/{key}?scope=... — any of {secret_invalid_key, secret_not_found, env_scope_invalid, env_scope_reserved}.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Re-seal a sealed secret under the current host identity.
   * Re-seals the `(app_id, key)` row under the current host X25519
   * recipient and stamps the kid column. Emits `secret.rotated`
   * audit kind when the row already had a value; emits `secret.set`
   * when the row was previously empty (first-time rotation). The
   * same byte cap as PUT applies (`SecretValueMaxBytes`). Existing
   * snapshots are invalidated after the write.
   *
   * @returns RotateAppSecretResponse The rotated sealed-secret envelope.
   * @throws ApiError
   */
  public static rotateAppSecret({
    slug,
    key,
    requestBody,
    scope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Secret key. Must start with a letter; A-Z, 0-9, underscore.
     */
    key: string,
    /**
     * New plaintext value. Sealed at rest server-side; plaintext never returned.
     */
    requestBody: RotateAppSecretRequest,
    /**
     * Env-var scope (ADR-090). A domain-valid slug (3..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (pre-PR-B behavior).
     *
     */
    scope?: string,
  }): CancelablePromise<RotateAppSecretResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/secrets/{key}/rotate',
      path: {
        'slug': slug,
        'key': key,
      },
      query: {
        'scope': scope,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `400 on POST /v1/apps/{slug}/secrets/{key}/rotate?scope=... — any of {secret_invalid_key, secret_not_found, env_scope_invalid, env_scope_reserved}.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        413: `code: secret_value_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `code: capacity_unavailable — no host headroom (alerting; should be near-impossible).`,
      },
    });
  }
  /**
   * List every sealed secret across the caller's account.
   * Replaces the per-app fan-out from `/v1/apps/{slug}/secrets`
   * with one account-scoped read (issue #393). Each row carries
   * the owning app's `app_id` and `app_slug` so the dashboard
   * can render "foo-app / DATABASE_URL" without a parallel
   * `/v1/apps` round-trip.
   *
   * **Plaintext never appears here** — only the age-sealed
   * envelope (base64). The plaintext value lives transiently in
   * the PUT handler and never crosses the apid wire.
   *
   * Cursor: `?before=<slug>|<key>` — the (app_slug, key) pair,
   * pipe-separated. The SQL splits it back via `split_part`.
   * Sort order is (app_slug ASC, key ASC). Default limit 25,
   * max 100 (strict 400 on bad input, matching `/v1/invoices`).
   *
   * @returns ListSecretsForAccountResponse One page of sealed-secret envelopes, ordered by (app_slug ASC, key ASC).
   * @throws ApiError
   */
  public static listSecretsForAccount({
    before,
    limit = 25,
  }: {
    /**
     * Cursor in the form `<slug>|<key>`. Omit for the first page.
     */
    before?: string,
    /**
     * Page size; server clamps to 1..100, returns 400 on bad input.
     */
    limit?: number,
  }): CancelablePromise<ListSecretsForAccountResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/secrets',
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
