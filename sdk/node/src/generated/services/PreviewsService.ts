/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppResponse } from '../models/AppResponse.js';
import type { CreatePreviewRequest } from '../models/CreatePreviewRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class PreviewsService {
  /**
   * Tear down a preview app.
   * One-click destroy of a preview app row (issue #961 Mega-C PR-1,
   * leaf 3). Typically fired from a "Tear down this preview" link
   * posted to the GitHub PR by `pkg/githubd`. Distinct from
   * `DELETE /v1/apps/{slug}` because the preview teardown also
   * stamps `apps.preview_pr_state='torn_down'` so the janitor
   * doesn't re-process the row on a subsequent tick, and emits a
   * distinct audit kind (`preview.destroyed_by_customer` vs
   * `app.deleted`).
   *
   * @returns void
   * @throws ApiError
   */
  public static destroyPreview({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/preview/{slug}/destroy',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `404 — slug does not identify a preview app. Use DELETE /v1/apps/{slug} to destroy a production app.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Provision or reopen a pull-request preview app.
   * Creates the stable `pr-{N}-{parent_slug}` preview environment for a
   * production app. The operation is idempotent for the same pull request
   * and returns the app metadata; deploy source separately with
   * `POST /v1/apps/{slug}/deployments/source-ref`.
   *
   * @returns AppResponse Existing preview environment returned.
   * @throws ApiError
   */
  public static createPreview({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreatePreviewRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<AppResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/previews',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
