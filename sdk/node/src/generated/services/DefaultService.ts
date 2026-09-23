/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FireCronRequestResponse } from '../models/FireCronRequestResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DefaultService {
  /**
   * Get fire-now request state
   * Polling surface for the row that `POST /v1/crons/{id}/run`
   * inserted (issue #791 PR-D / ADR-090 §Sub-decision 7).
   *
   * @returns FireCronRequestResponse Current state of the fire-now request.
   * @throws ApiError
   */
  public static getFireCronRequest({
    requestId,
  }: {
    /**
     * Fire-now request identifier returned by `POST /v1/crons/{id}/run`.
     */
    requestId: string,
  }): CancelablePromise<FireCronRequestResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/cron-fire-now-requests/{request_id}',
      path: {
        'request_id': requestId,
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
}
