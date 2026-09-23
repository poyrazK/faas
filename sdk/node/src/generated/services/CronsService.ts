/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateCronRequest } from '../models/CreateCronRequest.js';
import type { CronResponse } from '../models/CronResponse.js';
import type { FireCronResponse } from '../models/FireCronResponse.js';
import type { ListCronRunsResponse } from '../models/ListCronRunsResponse.js';
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
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
        400: `code: cron_invalid | plan_crons_not_allowed | plan_cron_quota`,
        401: `code: unauthorized`,
        402: `code: cron_invalid | plan_crons_not_allowed | plan_cron_quota`,
        403: `code: plan_limit_apps | plan_limit_ram | plan_limit_concurrency | plan_min_instances_not_allowed | plan_limit_secrets | plan_cron_quota | app_layer_too_large | image_egress_denied`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * Get one cron trigger.
   * Single-row read for one cron (issue #791 PR-E / ADR-090
   * closure). Backs `gregale crons info <id>` and any dashboard
   * drill-down. The wire shape matches `CronResponse` so SDK
   * clients decode it with the existing struct.
   *
   * @returns CronResponse The cron trigger.
   * @throws ApiError
   */
  public static getCron({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<CronResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/crons/{id}',
      path: {
        'id': id,
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
        400: `code: cron_invalid | plan_crons_not_allowed | plan_cron_quota`,
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
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
  /**
   * List recent runs of a cron.
   * Execution history for one cron, newest first. Each row reports
   * when the fire started, how long it took (`duration_ms`,
   * computed server-side), and a normalized `outcome` — so a
   * timeout is distinguishable from a generic failure without
   * parsing `error`.
   *
   * Paginated by `?before=<id>` (the LAST id of the returned
   * slice). Defaults to 10 per page; capped at 100. For a wider,
   * cross-source view use `GET /v1/invocations`.
   *
   * @returns ListCronRunsResponse A page of runs for this cron, newest first.
   * @throws ApiError
   */
  public static listCronRuns({
    id,
    before,
    limit = 10,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Cursor — return runs older than this run id. Omit for the most recent page.
     */
    before?: string,
    /**
     * Page size; 1-100, default 10.
     */
    limit?: number,
  }): CancelablePromise<ListCronRunsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/crons/{id}/runs',
      path: {
        'id': id,
      },
      query: {
        'before': before,
        'limit': limit,
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
   * Manually fire a cron now (bypasses the schedule boundary).
   * Issue #791 PR-C / ADR-090. Inserts a pending row into
   * `cron_fire_now_requests` and emits `db.NotifyCronRunNow`;
   * schedd claims the row on the next LISTEN delivery and calls
   * `RunCronNow` in its own process. The response is the
   * immediate 202 with the request id; the customer's
   * `GET /v1/crons/{id}/runs` will surface the matching
   * `cron.fired.manually` audit row once schedd stamps the
   * terminal state.
   *
   * Idempotent: a replay with the same Idempotency-Key returns
   * the stored 202 without enqueuing a second fire.
   *
   * Scoped to `deploy:write` (or `admin`); no new `cron:write`
   * scope is added (ADR-090 §Sub-decisions 1). The fire does
   * NOT shift `last_fired_at` — the next scheduled boundary is
   * unaffected.
   *
   * @returns FireCronResponse Fire-now enqueued. The request_id is the durable handle.
   * @throws ApiError
   */
  public static fireCron({
    id,
    idempotencyKey,
  }: {
    /**
     * The cron id (UUID hex, no dashes).
     */
    id: string,
    /**
     * Replay token. A duplicate (account, key) pair returns the
     * stored 202 with the original request id.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<FireCronResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/crons/{id}/run',
      path: {
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Plan tier does not include cron support (e.g. Free plan).`,
        404: `code: not_found`,
        410: `The cron is disabled.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
