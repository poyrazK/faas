/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIKeyResponse } from '../models/APIKeyResponse.js';
import type { CreateKeyRequest } from '../models/CreateKeyRequest.js';
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
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
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
