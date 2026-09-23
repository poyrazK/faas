/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class DebuggerService {
  /**
   * Queue a debugger replay from the dashboard.
   * Accepts the server-rendered dashboard form payload and queues the
   * same metadata-only replay as POST /v1/apps/{slug}/debug/requests/{req_id}/replay.
   * The response redirects to the request detail with a replay_id query
   * parameter; request bodies, credentials, and customer headers are
   * never replayed.
   *
   * @returns void
   * @throws ApiError
   */
  public static dashboardReplayAppDebugRequest({
    slug,
    reqId,
    formData,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * Public x-faas-request-id, or the internal telemetry row UUID for compatibility.
     */
    reqId: string,
    formData: {
      csrf_token: string;
      since?: string;
      route?: string;
    },
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/dashboard/apps/{slug}/debug/requests/{req_id}/replay',
      path: {
        'slug': slug,
        'req_id': reqId,
      },
      formData: formData,
      mediaType: 'application/x-www-form-urlencoded',
      errors: {
        303: `Redirect to the request detail and replay status.`,
        400: `Invalid dashboard CSRF token or form payload.`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        404: `code: not_found`,
        409: `No enabled mirror rule targets the serving deployment.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
