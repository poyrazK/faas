/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class OpenapiDiscoveryService {
  /**
   * Read the captured OpenAPI document for a deployment.
   * Returns the OpenAPI document the cold-boot probe captured from the customer's app (issue #975 item #1, ADR-122). The probe runs unconditionally during cold boot; the apid surfaces the doc only on paid plans (Hobby/Pro/Scale). Free customers receive 402 + openapi_docs_not_allowed. Cache-Control: 5 min. Response headers: X-OpenAPI-Doc-Source (cold_boot or manual_upload), X-OpenAPI-Doc-Truncated (1 if clipped at 128 KiB), X-OpenAPI-Doc-Byte-Size, X-OpenAPI-Doc-Deployment-ID, X-OpenAPI-Doc-App-ID, X-OpenAPI-Doc-SHA256, X-OpenAPI-Doc-Captured-At and X-OpenAPI-Doc-Updated-At. These authenticated metadata headers bind the unchanged body to its capture.
   * @returns any The OpenAPI document.
   * @throws ApiError
   */
  public static getDeploymentOpenApiDoc({
    slug,
    deployment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment UUID.
     */
    deployment: string,
  }): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployments/{deployment}/openapi',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      errors: {
        401: `code: unauthorized`,
        402: `Free plan cannot access endpoint discovery. MicroVM captures the doc, but the apid refuses to expose it.`,
        404: `No document captured for this deployment, or the deployment id is not owned by the caller (IDOR floor).`,
      },
    });
  }
  /**
   * Manually upload.
   * @returns any The updated row.
   * @throws ApiError
   */
  public static updateDeploymentOpenApiDoc({
    slug,
    deployment,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment UUID.
     */
    deployment: string,
    requestBody: Record<string, any>,
  }): CancelablePromise<{
    deployment_id: string;
    account_id: string;
    app_id: string;
    source: 'cold_boot' | 'manual_upload';
    byte_size: number;
    /**
     * Lower-case hex SHA-256 of the stored body.
     */
    doc_sha256?: string;
    truncated?: boolean;
    captured_at: string;
    updated_at: string;
    /**
     * The OpenAPI document body.
     */
    doc?: Record<string, any>;
  }> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/deployments/{deployment}/openapi',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Bad request.`,
        401: `code: unauthorized`,
        402: `Two distinct 402 reasons: openapi_docs_not_allowed (Free plan cannot use endpoint discovery), plan_openapi_doc_quota_reached (per-account Plan.OpenAPIDocsPerAccount() cap reached).`,
        404: `code: not_found`,
        413: `code: plan_openapi_doc_too_large. Body exceeds Plan.OpenAPIDocMaxBytes() (Hobby 100 KiB, Pro 100 KiB, Scale 100 KiB).`,
      },
    });
  }
  /**
   * Delete the captured OpenAPI document for a deployment.
   * Companion to PATCH — explicitly wipes the captured doc (re-applied on next cold boot of the deployment).
   * @returns void
   * @throws ApiError
   */
  public static deleteDeploymentOpenApiDoc({
    slug,
    deployment,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Deployment UUID.
     */
    deployment: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/deployments/{deployment}/openapi',
      path: {
        'slug': slug,
        'deployment': deployment,
      },
      errors: {
        401: `code: unauthorized`,
        402: `Free plan cannot use endpoint discovery.`,
        404: `code: not_found`,
      },
    });
  }
}
