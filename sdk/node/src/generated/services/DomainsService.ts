/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateCustomDomainRequest } from '../models/CreateCustomDomainRequest.js';
import type { CustomDomainResponse } from '../models/CustomDomainResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DomainsService {
  /**
   * List custom domain bindings.
   * @returns CustomDomainResponse Custom-domain bindings on the account.
   * @throws ApiError
   */
  public static listDomains(): CancelablePromise<Array<CustomDomainResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/domains',
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
   * Bind a custom domain.
   * @returns CustomDomainResponse The new custom-domain binding.
   * @throws ApiError
   */
  public static createDomain({
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Domain-bind payload — domain string + target app slug. See CreateCustomDomainRequest.
     */
    requestBody: CreateCustomDomainRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<CustomDomainResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/domains',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        409: `code: conflict`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Remove a custom domain binding.
   * @returns void
   * @throws ApiError
   */
  public static deleteDomain({
    domain,
  }: {
    /**
     * The custom domain string (e.g. `app.example.com`).
     */
    domain: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/domains/{domain}',
      path: {
        'domain': domain,
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
