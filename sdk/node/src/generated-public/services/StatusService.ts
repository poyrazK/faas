/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PublicStatusEvent } from '../models/PublicStatusEvent.js';
import type { PublicStatusOverview } from '../models/PublicStatusOverview.js';
import type { StatusPage } from '../models/StatusPage.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class StatusService {
  /**
   * Read the current public platform status.
   * Unauthenticated single-region snapshot. Includes five public
   * capabilities, exactly 30 UTC daily observations per capability,
   * current error-budget indicators, active events, maintenance in the
   * next 30 days, and up to 20 resolved incidents from the last 90 days.
   * Data older than 90 seconds is marked stale; missing telemetry is never
   * represented as operational.
   *
   * @returns PublicStatusOverview Current status snapshot, including degraded-source responses.
   * @throws ApiError
   */
  public static getPublicStatus(): CancelablePromise<PublicStatusOverview> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/status',
    });
  }
  /**
   * Read one public incident or maintenance timeline.
   * @returns PublicStatusEvent Public event and chronological update timeline.
   * @throws ApiError
   */
  public static getPublicStatusIncident({
    publicId,
  }: {
    /**
     * Stable public UUID used by status permalinks.
     */
    publicId: string,
  }): CancelablePromise<PublicStatusEvent> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/status/incidents/{public_id}',
      path: {
        'public_id': publicId,
      },
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read the backwards-compatible status indicator projection.
   * Always returns valid JSON, including when the evaluator source is degraded.
   * @returns StatusPage Legacy status projection.
   * @throws ApiError
   */
  public static getLegacyStatusSlo(): CancelablePromise<StatusPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/status/slo.json',
    });
  }
}
