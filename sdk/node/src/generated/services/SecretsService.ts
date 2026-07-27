/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppSecretListResponse } from '../models/AppSecretListResponse.js';
import type { AppSecretResponse } from '../models/AppSecretResponse.js';
import type { PutAppSecretRequest } from '../models/PutAppSecretRequest.js';
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
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppSecretListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/secrets',
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
   * Set a sealed secret.
   * Seals the plaintext value against the host X25519 recipient and
   * persists the ciphertext. The plaintext never lands in PG.
   *
   * @returns AppSecretResponse The stored sealed-secret envelope.
   * @throws ApiError
   */
  public static setSecret({
    slug,
    key,
    requestBody,
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
  }): CancelablePromise<AppSecretResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/secrets/{key}',
      path: {
        'slug': slug,
        'key': key,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: plan_limit_secrets`,
        413: `code: secret_value_too_large`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Secret key. Must start with a letter; A-Z, 0-9, underscore.
     */
    key: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/secrets/{key}',
      path: {
        'slug': slug,
        'key': key,
      },
      errors: {
        400: `code: secret_not_found`,
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
