/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppEnvListResponse } from '../models/AppEnvListResponse.js';
import type { AppEnvResponse } from '../models/AppEnvResponse.js';
import type { ConfigGuard } from '../models/ConfigGuard.js';
import type { ConfigRevisionDetail } from '../models/ConfigRevisionDetail.js';
import type { ConfigRollbackRequest } from '../models/ConfigRollbackRequest.js';
import type { ConfigRollbackResponse } from '../models/ConfigRollbackResponse.js';
import type { EnvDiffResponse } from '../models/EnvDiffResponse.js';
import type { ListConfigRevisionsResponse } from '../models/ListConfigRevisionsResponse.js';
import type { PutAppEnvRequest } from '../models/PutAppEnvRequest.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class EnvService {
  /**
   * Render the env-diff matrix (presence / value-equality across scopes).
   * Returns the (rows × scopes) matrix of env vars + sealed
   * secrets on the app. Secrets never reveal plaintext — the
   * cell carries {present, value_hash} for secret rows and
   * {present, value} for env rows. Two cells with the same
   * `value_hash` therefore share byte-identical plaintext
   * (collision probability 2^-64). Older rows have
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
   * **scope filter.** The optional `?scope=`
   * query param selects which scope to read. Omitted = the
   * default scope (legacy behaviour, byte-identical wire).
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
     * Env-var scope. A domain-valid slug (1..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (legacy behaviour).
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
        400: `code: env_scope_invalid — ?scope= failed the EnvScopePattern check (empty, too long, or out-of-shape slug). Applies to both env and secrets surfaces.`,
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
   * List a scope's config revisions.
   * Newest first. Each customer env or secret write records a revision of the scope's complete config; the newest 100 are kept. Values are not listed; read one revision for its variables.
   * @returns ListConfigRevisionsResponse Config revisions and the scope's newest config guard.
   * @throws ApiError
   */
  public static listConfigRevisions({
    slug,
    scope,
    limit = 20,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Env-var scope. A domain-valid slug (1..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (legacy behaviour).
     *
     */
    scope?: string,
    /**
     * Revisions to return, newest first.
     */
    limit?: number,
  }): CancelablePromise<ListConfigRevisionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/config/revisions',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'limit': limit,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
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
   * Read one config revision.
   * The scope's variables and secret versions after the change. Secret values and fingerprints are never returned.
   * @returns ConfigRevisionDetail The config revision.
   * @throws ApiError
   */
  public static getConfigRevision({
    slug,
    revision,
    scope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * The scope's config revision number, from the config history.
     */
    revision: number,
    /**
     * Env-var scope. A domain-valid slug (1..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (legacy behaviour).
     *
     */
    scope?: string,
  }): CancelablePromise<ConfigRevisionDetail> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/config/revisions/{revision}',
      path: {
        'slug': slug,
        'revision': revision,
      },
      query: {
        'scope': scope,
      },
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
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
   * Read a scope's newest config guard.
   * A guard judges the first traffic served with a changed config against the config before it, and restores the previous config when the new one fails. 404 `config_guard_not_found` when no change in the scope was guarded.
   * @returns ConfigGuard The config guard and, once closed, its decision.
   * @throws ApiError
   */
  public static getConfigGuard({
    slug,
    scope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Env-var scope. A domain-valid slug (1..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (legacy behaviour).
     *
     */
    scope?: string,
  }): CancelablePromise<ConfigGuard> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/config/guard',
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
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Restore a scope's variables to a config revision.
   * Sets and deletes variables until the scope matches the revision, as one guarded change. Secrets cannot be restored from a revision; those that differ are listed in `secrets_not_restored`.
   * @returns ConfigRollbackResponse What the rollback changed.
   * @throws ApiError
   */
  public static rollbackConfig({
    slug,
    requestBody,
    scope,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: ConfigRollbackRequest,
    /**
     * Env-var scope. A domain-valid slug (1..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (legacy behaviour).
     *
     */
    scope?: string,
  }): CancelablePromise<ConfigRollbackResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/config/rollback',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
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
   * Set an env var.
   * Persists the plaintext value verbatim in the app_envs table (no
   * seal step). Env vars are non-sensitive runtime config by contract
   * — credentials stay on `/v1/apps/{slug}/secrets/{key}`. Applies on
   * next cold wake; cached snapshots are invalidated and the running
   * instance is unaffected. Use `POST /restart?fresh=true` to apply now.
   *
   * **scope filter.** The optional `?scope=`
   * query param selects which scope to write. Omitted = the
   * default scope (legacy behaviour). The reserved sentinel
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
    guard,
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
     * `false` opts this env or secret write out of the config guard: no guard opens, and a guard already watching the scope
     * closes as superseded. Omitted or `true` guards the change when the
     * scope has a live deployment.
     *
     */
    guard?: boolean,
    /**
     * Env-var scope. A domain-valid slug (1..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (legacy behaviour).
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
        'guard': guard,
        'scope': scope,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `400 on PUT /v1/apps/{slug}/env/{key}?scope=... — any of {env_var_invalid_key, env_scope_invalid, env_scope_reserved}.`,
        401: `code: unauthorized`,
        403: `code: plan_limit_env_vars`,
        413: `code: env_value_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
    guard,
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
     * `false` opts this env or secret write out of the config guard: no guard opens, and a guard already watching the scope
     * closes as superseded. Omitted or `true` guards the change when the
     * scope has a live deployment.
     *
     */
    guard?: boolean,
    /**
     * Env-var scope. A domain-valid slug (1..40 chars,
     * lowercase alnum + dash, no leading/trailing dash) — e.g.
     * `default`, `staging`, `prod-eu`. Or the reserved sentinel
     * `__all__` on GET only, which returns the nested
     * `env_by_scope` response shape (every scope on the app).
     * Omitted = `scope=default` (legacy behaviour).
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
        'guard': guard,
        'scope': scope,
      },
      errors: {
        400: `400 on DELETE /v1/apps/{slug}/env/{key}?scope=... — any of {env_var_not_found, env_scope_invalid, env_scope_reserved}.`,
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
}
