/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DiffRequest } from '../models/DiffRequest.js';
import type { DiffResponse } from '../models/DiffResponse.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class DeploydiffService {
  /**
   * Read-only preview of what a deploy would change.
   * PR-1 of the deploy-diff cluster. Server-side equivalent of
   * `gregale deploy --diff` — runs the same engine (pkg/deploydiff)
   * and returns the same wire shape (DiffResponse).
   *
   * Read-only: no DB writes, no audit row, no deployment row. The
   * app-not-found case is 200 with `diff.changes[]` containing a
   * `would_create_app` row + the quota gate firing against the
   * customer's plan — a CI consumer can preview a fresh deploy the
   * same way the CLI does.
   *
   * Auth: bearer-key with `apps:read` (or admin). NO MFA on this
   * endpoint, mirroring GET /v1/apps/{slug}/metrics.
   *
   * Schema-break detection is text-only in PR-1 (handler / entrypoint
   * / env-key changes → warn-severity Breaks). Structural OpenAPI
   * response-schema diff lands in PR-2.
   *
   * @returns DiffResponse DiffResponse with `blocking: true` if any break has
   * `severity: "error"`. CI gate input — `jq '.blocking'`
   * collapses to a single bool.
   *
   * @throws ApiError
   */
  public static diffApp({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: DiffRequest,
  }): CancelablePromise<DiffResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/diff',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed. Body is malformed JSON, fails
        strict-decode, or has a plan lookup error.
        `,
        401: `code: unauthorized`,
        403: `code: forbidden. Caller's bearer key lacks both
        \`apps:read\` and admin scope.
        `,
        503: `code: capacity. pkg/state read failed for the baseline
        builder (deployment / env / crons / edge rules).
        `,
      },
    });
  }
}
