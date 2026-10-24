/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlanResponse } from '../models/PlanResponse.js';
import type { ProjectSourceRefScanRequest } from '../models/ProjectSourceRefScanRequest.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class ProjectsService {
  /**
   * Scan a connected GitHub repository and return a deploy plan.
   * Resolves a durable GitHub App installation owned by the authenticated
   * account, fetches the selected ref through githubd, and runs the same
   * read-only scanner as the multipart upload endpoint. The installation
   * token remains inside the control plane. When `install_id` is omitted,
   * exactly one connected installation must be able to access `repo`.
   *
   * @returns PlanResponse The deploy plan generated from the fetched repository.
   * @throws ApiError
   */
  public static scanProjectSourceRef({
    requestBody,
  }: {
    requestBody: ProjectSourceRefScanRequest,
  }): CancelablePromise<PlanResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/projects/scan/source-ref',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        413: `code: source_too_large`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
}
