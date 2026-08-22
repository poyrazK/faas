/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppEnvListResponse } from '../models/AppEnvListResponse.js';
import type { AppEnvResponse } from '../models/AppEnvResponse.js';
import type { EnvDiffResponse } from '../models/EnvDiffResponse.js';
import type { PutAppEnvRequest } from '../models/PutAppEnvRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class EnvService {
  /**
   * Render the env-diff matrix (presence / value-equality across scopes).
   * Returns the (rows × scopes) matrix of env vars + sealed
   * secrets on the app. Secrets never reveal plaintext — the
   * cell carries {present, value_hash} for secret rows and
   * {present, value} for env rows. Two cells with the same
   * `value_hash` therefore share byte-identical plaintext
   * (collision probability 2^-64). Pre-PR-C rows have
   * `value_hash = ''` and emit no `value_hash` key.
   *
   * @returns EnvDiffResponse Env-diff matrix.
   * @throws ApiError
   */
  public static getAppEnvDiff({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<EnvDiffResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/env-diff',
      path: {
        'slug': slug,
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
   * List env vars on an app.
   * Returns every env var key + timestamps on the app. The plaintext
   * value NEVER appears in the response — guest-init reads the value
   * at process start from `/etc/faas/env.json` inside the guest.
   *
   * **ADR-090 PR-B scope filter.** The optional `?scope=`
   * query param selects which scope to read. Omitted = the
   * default scope (pre-PR-B behavior, byte-identical wire).
   * `?scope=__all__` returns the nested `env_by_scope` response
   * shape with every scope on the app; the flat `env` array
   * is empty in that arm (discriminated union). Any other
   * `?scope=<slug>` filters to that one scope. Invalid scope
   * values return 400 `env_scope_invalid`.
   *
   * @returns AppEnvListResponse Env var envelopes on the app (plaintext never returned).
   * @throws ApiError
   */
  public static listEnv({
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
  }): CancelablePromise<AppEnvListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/env',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
      },
      errors: {
        400: `code: env_scope_invalid — ?scope= failed the EnvScopePattern check (empty, too long, or out-of-shape slug). Applies to both env and secrets surfaces (ADR-090 PR-B, ADR-092 PR-B).`,
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
   * Set an env var.
   * Persists the plaintext value verbatim in the app_envs table (no
   * seal step). Env vars are non-sensitive runtime config by contract
   * — credentials stay on `/v1/apps/{slug}/secrets/{key}`. Applies on
   * next wake (cold-boot OR snapshot-restore); the running instance
   * is unaffected.
   *
   * **ADR-090 PR-B scope filter.** The optional `?scope=`
   * query param selects which scope to write. Omitted = the
   * default scope (pre-PR-B behavior). The reserved sentinel
   * `__all__` is rejected with 400 `env_scope_reserved` on
   * writes (it has no meaning on a single-row write). Invalid
   * scope shapes return 400 `env_scope_invalid`.
   *
   * @returns AppEnvResponse The stored env var envelope.
   * @throws ApiError
   */
  public static setEnv({
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
     * Env var payload — key name + plaintext.
     */
    requestBody: PutAppEnvRequest,
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
  }): CancelablePromise<AppEnvResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/env/{key}',
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
        400: `400 on PUT /v1/apps/{slug}/env/{key}?scope=... — any of {env_var_invalid_key, env_scope_invalid, env_scope_reserved}.`,
        401: `code: unauthorized`,
        403: `code: plan_limit_env_vars`,
        413: `code: env_value_too_large`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Delete an env var.
   * Removes the (app_id, scope, key) row. `?scope=` selects
   * which scope; omitted = the default scope. `?scope=__all__`
   * is rejected (400 `env_scope_reserved`) — same reason as
   * on PUT: the sentinel has no meaning on a single-row
   * delete. Returns 400 `env_var_not_found` (not 404) when
   * no row matches — the URL resource is the env-var, not
   * the app.
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteEnv({
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
      url: '/v1/apps/{slug}/env/{key}',
      path: {
        'slug': slug,
        'key': key,
      },
      query: {
        'scope': scope,
      },
      errors: {
        400: `400 on DELETE /v1/apps/{slug}/env/{key}?scope=... — any of {env_var_not_found, env_scope_invalid, env_scope_reserved}.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
