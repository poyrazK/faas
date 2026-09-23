/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateCustomDomainRequest } from '../models/CreateCustomDomainRequest.js';
import type { CustomDomainResponse } from '../models/CustomDomainResponse.js';
import type { DomainDoctorReport } from '../models/DomainDoctorReport.js';
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Run the 5-check domain doctor (ADR-120).
   * Returns the per-domain doctor report. The five checks map
   * 1:1 to the Render-style custom-domain check: dns_record,
   * points_to_gregale, tls_certificate, caa_permits,
   * ipv6_conflict. Each check carries a Status (ok / fail /
   * pending / na), Detail, Observed, Remediation, and a
   * per-probe CheckedAt. Used by `gregale domains doctor
   * <domain>`.
   *
   * The handler reads the latest observation row from
   * `domain_doctor_observations` (the dns_poller writes a
   * row every 30s). When the row is older than
   * FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300) or missing,
   * the handler triggers a synchronous re-probe with a 5s
   * budget. Stale=true is the visible degradation; the
   * response is still 200 with the per-check Status.
   *
   * 503 CodeDoctorDisabled is returned when the operator
   * hasn't set FAAS_DOMAIN_DOCTOR_ENABLED. The route stays
   * registered so the CLI gets a deterministic error code
   * (matches the pre-#911 pattern in `api/flags.go`).
   *
   * @returns DomainDoctorReport The doctor report. `stale:true` means the cached row was older than the TTL when the handler ran a synchronous re-probe.
   * @throws ApiError
   */
  public static getDomainDoctor({
    domain,
  }: {
    /**
     * The custom domain to diagnose (e.g. `app.example.com`).
     */
    domain: string,
  }): CancelablePromise<DomainDoctorReport> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/domains/{domain}/doctor',
      path: {
        'domain': domain,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
        503: `Doctor is dark-launched (CodeDoctorDisabled) or the probe pass failed (CodeDoctorUnavailable).`,
      },
    });
  }
}
