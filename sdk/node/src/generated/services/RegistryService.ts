/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppRegistryCredentialListResponse } from '../models/AppRegistryCredentialListResponse.js';
import type { AppRegistryCredentialResponse } from '../models/AppRegistryCredentialResponse.js';
import type { PutAppRegistryCredentialRequest } from '../models/PutAppRegistryCredentialRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class RegistryService {
  /**
   * List sealed private-registry credentials on an app.
   * Returns every (registry, username, timestamps) triple on the app.
   * Plaintext passwords are never returned — imaged transiently
   * unseals the ciphertext in the pull path. Quota metadata
   * (`quota_max`, `count`) is included so the CLI can render a
   * progress bar without a second call.
   *
   * @returns AppRegistryCredentialListResponse List of credential envelopes on the app (plaintext never returned).
   * @throws ApiError
   */
  public static listRegistryCredentials({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppRegistryCredentialListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/registry-credentials',
      path: {
        'slug': slug,
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
   * Set or replace a sealed private-registry credential.
   * Seals the plaintext password against the host X25519 recipient
   * (namespace `"registry_creds"`) and upserts the `(app_id, registry)`
   * row. The plaintext never lands in PG. Re-PUTs of an existing
   * `(app, host)` replace the ciphertext and bump `updated_at`
   * WITHOUT consuming a new quota slot.
   *
   * @returns AppRegistryCredentialResponse The stored credential envelope.
   * @throws ApiError
   */
  public static setRegistryCredential({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Registry host + username + plaintext password. Sealed at rest.
     */
    requestBody: PutAppRegistryCredentialRequest,
  }): CancelablePromise<AppRegistryCredentialResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/registry-credentials',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: invalid_registry_host — host missing scheme/path, uppercase, port out of range, or field-length cap exceeded.`,
        401: `code: unauthorized`,
        403: `code: plan_registry_credentials_not_allowed — Free plan cannot store private-registry credentials.`,
        404: `code: not_found`,
        413: `code: plan_registry_credential_quota — per-app host cap reached (Hobby 2 / Pro 5 / Scale 20).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Delete a sealed private-registry credential.
   * Removes the `(app_id, registry)` row. The registry host is the
   * URL resource and is supplied via the `?registry=` query param
   * (URL-encoded). Hosts may carry a `:port` — path segments don't
   * escape cleanly.
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteRegistryCredential({
    slug,
    registry,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Registry host to delete (URL-encoded; matches the host stored on the row).
     */
    registry: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/registry-credentials',
      path: {
        'slug': slug,
      },
      query: {
        'registry': registry,
      },
      errors: {
        400: `code: registry_credential_not_found — DELETE on a host that has no sealed credential.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
