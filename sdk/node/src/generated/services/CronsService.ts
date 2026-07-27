/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateCronRequest } from '../models/CreateCronRequest.js';
import type { CronResponse } from '../models/CronResponse.js';
import type { UpdateCronRequest } from '../models/UpdateCronRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class CronsService {
  /**
   * List cron triggers.
   * @returns CronResponse Cron triggers on the account.
   * @throws ApiError
   */
  public static listCrons(): CancelablePromise<Array<CronResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/crons',
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
   * Create a cron trigger.
   * @returns CronResponse The new cron trigger.
   * @throws ApiError
   */
  public static createCron({
    requestBody,
    idempotencyKey,
  }: {
    /**
     * Cron payload — schedule expression + target URL. See CreateCronRequest.
     */
    requestBody: CreateCronRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<CronResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/crons',
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: cron_invalid`,
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Partial-update a cron.
   * @returns CronResponse The updated cron trigger.
   * @throws ApiError
   */
  public static updateCron({
    id,
    requestBody,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Cron patch — every field is optional. See UpdateCronRequest.
     */
    requestBody: UpdateCronRequest,
  }): CancelablePromise<CronResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/crons/{id}',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: cron_invalid`,
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
   * Delete a cron.
   * @returns void
   * @throws ApiError
   */
  public static deleteCron({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/crons/{id}',
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
