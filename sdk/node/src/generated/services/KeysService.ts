/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIKeyResponse } from '../models/APIKeyResponse.js';
import type { CreateKeyRequest } from '../models/CreateKeyRequest.js';
import type { CreateOrgAPIKeyRequest } from '../models/CreateOrgAPIKeyRequest.js';
import type { GraceWindowResponse } from '../models/GraceWindowResponse.js';
import type { ListOrgAPIKeysResponse } from '../models/ListOrgAPIKeysResponse.js';
import type { RotateKeyResponse } from '../models/RotateKeyResponse.js';
import type { RotateOrgAPIKeyRequest } from '../models/RotateOrgAPIKeyRequest.js';
import type { RotateOrgAPIKeyResponse } from '../models/RotateOrgAPIKeyResponse.js';
import type { SetGraceWindowRequest } from '../models/SetGraceWindowRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class KeysService {
  /**
   * List API keys (no plaintext).
   * @returns APIKeyResponse API key metadata on the account. Plaintext is never returned.
   * @throws ApiError
   */
  public static listKeys(): CancelablePromise<Array<APIKeyResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/keys',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Mint a new API key.
   * Returns the plaintext key **once**. The plaintext is never stored
   * and cannot be retrieved; subsequent GETs return only the prefix.
   *
   * @returns APIKeyResponse The new API key. **The plaintext is returned exactly once** — every subsequent GET returns only the prefix.
   * @throws ApiError
   */
  public static createKey({
    requestBody,
  }: {
    /**
     * Create a new API key for the authenticated account. Plaintext is returned exactly once in the 201 response and cannot be recovered later — store it immediately. See IAM-1, ADR-034 rev2 for the scope vocabulary.
     */
    requestBody: CreateKeyRequest,
  }): CancelablePromise<APIKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/keys',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Revoke an API key.
   * @returns void
   * @throws ApiError
   */
  public static deleteKey({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/keys/{id}',
      path: {
        'id': id,
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
   * Rotate an API key.
   * Mints a new key (status='active') and demotes the old key in
   * a single transaction (issue #189 / IAM-5). The new key
   * inherits the predecessor's label + scopes so the customer's
   * CI config does not need to chase a label change.
   *
   * The old key's `expires_at` is OVERWRITTEN to the grace
   * deadline (now + grace_window_days, where grace_window_days
   * resolves from the per-account override or the 7-day plan
   * default). Status flips to 'grace' for the window, then to
   * 'revoked' at the deadline. Setting
   * `accounts.key_grace_window_days = 0` makes rotation atomic
   * (old key revoked immediately).
   *
   * Returns the new plaintext exactly once — the old plaintext
   * is not re-issued (we only store the SHA-256 hash). The
   * customer's CI script captures the new plaintext at the
   * moment of rotation and uses the new key thereafter; the old
   * key remains valid only for the grace window.
   *
   * Quota: rotation is quota-neutral (-1 +1 = 0 net). A
   * customer AT the per-account cap can still rotate.
   *
   * @returns RotateKeyResponse New key minted, old key now in 'grace' (or 'revoked' if grace_window_days=0).
   * @throws ApiError
   */
  public static rotateKey({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<RotateKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/keys/{id}/rotate',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `No such key, or the key is already revoked.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List API keys minted against the active org.
   * Returns every key the org owns (active + grace + revoked).
   * Mirrors `GET /v1/keys`; PR 6's canonical path. The `org_id`
   * on every row will match `{slug}` because the store filters
   * server-side on the loaded membership.
   *
   * @returns ListOrgAPIKeysResponse Org-scoped API key list.
   * @throws ApiError
   */
  public static listOrgApiKeys({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListOrgAPIKeysResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/keys',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Org slug not found, or caller has no membership.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Mint a new API key for the active org.
   * Returns the plaintext exactly once (same as `POST /v1/keys`).
   * The new row's `org_id` is the loaded membership's org; personal
   * orgs are mintable (the `org_personal_immutable` 409 applies to
   * mutations on the org row, not key mints against it).
   *
   * @returns APIKeyResponse New API key minted against the org.
   * @throws ApiError
   */
  public static createOrgApiKey({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateOrgAPIKeyRequest,
  }): CancelablePromise<APIKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/keys',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid body (unknown scope, label too long).`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Active-org header missing or unknown; caller has no membership on the resolved slug.`,
        429: `Per-account key quota (\`api.Plan.KeysMax\`) reached.`,
      },
    });
  }
  /**
   * Fetch a single API key by id (org-scoped).
   * Lookup mirror of `GET /v1/keys/{id}` (the legacy path does not
   * exist by id in pre-PR-6 — this path is the canonical single-key
   * read). The response is the standard `APIKeyResponse` (no
   * plaintext). Cross-org probes collapse to 404.
   *
   * @returns APIKeyResponse Single API key.
   * @throws ApiError
   */
  public static getOrgApiKey({
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
  }): CancelablePromise<APIKeyResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/keys/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Key id not in org, or org slug not found.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Revoke an API key (org-scoped).
   * Soft-delete mirror of `DELETE /v1/keys/{id}`. Status flips to
   * 'revoked'; subsequent bearer-auth attempts hit `ErrAPIKeyRevoked`
   * (401 unauthenticated). The audit row carries `org_id` (PR 6
   * closes the ADR-061 §E "audit scoped to org" gap).
   *
   * @returns void
   * @throws ApiError
   */
  public static revokeOrgApiKey({
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
      url: '/v1/orgs/{slug}/keys/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Key id not in this org, or active-org slug not found.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Rotate an API key (org-scoped).
   * Org-scoped counterpart of `POST /v1/keys/{id}/rotate`. Mints a
   * new key (status='active') and demotes the predecessor into the
   * grace window in one transaction. The new key inherits the
   * predecessor's `org_id` — rotation never silently rebinds across
   * orgs. Quota is neutral (-1 +1 = 0).
   *
   * @returns RotateOrgAPIKeyResponse New key minted, predecessor in 'grace' (or 'revoked' if grace_window_days=0).
   * @throws ApiError
   */
  public static rotateOrgApiKey({
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
    requestBody?: RotateOrgAPIKeyRequest,
  }): CancelablePromise<RotateOrgAPIKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/keys/{id}/rotate',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Key id not in org, or org slug not found, or predecessor already revoked.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Read the per-account rotation grace-window override.
   * Returns the customer's current override (the value stored in
   * `accounts.key_grace_window_days`) and the plan default
   * alongside. `days=null` means "no override" — the rotation
   * handler uses the plan default (api.DefaultAPIKeyGraceWindowDays = 7).
   *
   * @returns GraceWindowResponse Current override + plan default.
   * @throws ApiError
   */
  public static getGraceWindow(): CancelablePromise<GraceWindowResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/keys/grace_window_days',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Set the per-account rotation grace-window override.
   * Writes the override and invalidates the in-process cache so
   * the next rotation observes the new value. `days=0` is atomic
   * rotation (no grace); `days=null` (or omission) clears the
   * override and falls back to the plan default.
   *
   * @returns GraceWindowResponse Override applied.
   * @throws ApiError
   */
  public static setGraceWindow({
    requestBody,
  }: {
    requestBody: SetGraceWindowRequest,
  }): CancelablePromise<GraceWindowResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/account/keys/grace_window_days',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid body (e.g. negative days).`,
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
