/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class TelemetryService {
  /**
   * OTel spans writer (ADR-127 PR-D).
   * Standard OTLP/HTTP endpoint for the OTel spans sidecar
   * protocol (POST ExportTraceServiceRequest). Auth via
   * Authorization: Bearer <api-key>. Plan-gated by
   * DebugTelemetryEnabled; rate-capped by
   * DebugTelemetryRequestsPerMinute; span-capped by
   * DebugTelemetrySpansPerTrace. Body shape is defined by
   * the OpenTelemetry proto — the spec documents the
   * endpoint metadata only. The SDK does not model this
   * route (routeExclude on sdk-coverage + spec_compliance);
   * OTel SDKs speak OTLP/HTTP directly.
   *
   * @returns any Spans accepted (truncated summary staged in flush accumulator).
   * @throws ApiError
   */
  public static ingestOtlpSpans(): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/otel/v1/traces',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\` and \`quota_exhausted\`.
        `,
      },
    });
  }
}
