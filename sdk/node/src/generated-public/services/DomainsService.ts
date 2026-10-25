/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateCustomDomainRequest } from '../models/CreateCustomDomainRequest.js';
import type { CustomDomainResponse } from '../models/CustomDomainResponse.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Show a custom domain's cert details (issue
   * Returns the durable domain row + the live cert chain
   * (NotAfter, SANs) by dialing port-443 and reading the leaf
   * cert. Used by `gregale domains show <domain>`.
   *
   * @returns CustomDomainResponse The domain row + cert details.
   * @throws ApiError
   */
  public static getDomain({
    domain,
  }: {
    /**
     * The custom domain string (e.g. `app.example.com`).
     */
    domain: string,
  }): CancelablePromise<CustomDomainResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/domains/{domain}',
      path: {
        'domain': domain,
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Re-verify a domain's DNS + cert (issue
   * Re-runs the DNS verifier + cert dial; returns the canonical
   * CustomDomainResponse. Used by `gregale domains verify
   * <domain>`. Idempotent: POSTing twice does not change the
   * durable verification state.
   *
   * @returns CustomDomainResponse The row + cert details. `cert_not_after` / `cert_sans` populated when the cert dial succeeds.
   * @throws ApiError
   */
  public static verifyDomain({
    domain,
    idempotencyKey,
  }: {
    /**
     * The custom domain to re-verify (e.g. `app.example.com`). The same shape as the GET path; verify walks DNS + cert while show returns the durable row.
     */
    domain: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<CustomDomainResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/domains/{domain}/verify',
      path: {
        'domain': domain,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        422: `code: domain_verification_failed | domain_cert_not_issued.
        DNS walk found a missing/mismatched TXT record, or the
        port-443 cert is a CDN cert whose SANs do not include
        the customer's domain.
        `,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Set a verified custom domain as the app default.
   * Atomically replaces the app's previous default custom domain.
   * The domain must be verified and belong to the authenticated account.
   * Used by `gregale domains set-default <domain>`.
   *
   * @returns CustomDomainResponse The selected domain binding with `default=true`.
   * @throws ApiError
   */
  public static setDefaultDomain({
    domain,
    idempotencyKey,
  }: {
    /**
     * The verified custom domain to make the app's canonical host.
     */
    domain: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<CustomDomainResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/domains/{domain}/default',
      path: {
        'domain': domain,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `The domain is not verified and cannot be selected.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Re-arm an expired or backed-off TXT verification challenge.
   * @returns any Verification was queued for the next bounded poller cycle.
   * @throws ApiError
   */
  public static retryDomainVerification({
    domain,
    idempotencyKey,
  }: {
    /**
     * Customer-owned custom domain to re-arm for TXT verification.
     */
    domain: string,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/domains/{domain}/retry',
      path: {
        'domain': domain,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `The domain is already verified.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
}
