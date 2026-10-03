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
   * OTel SDKs speak OTLP/HTTP directly. Supports application/json
   * (OTLP hexadecimal trace/span IDs) and application/x-protobuf,
   * optional gzip compression, empty exports, and multi-trace batches.
   * Responses use ExportTraceServiceResponse; per-trace ownership
   * rejection returns partialSuccess with rejectedSpans. Error bodies
   * use google.rpc.Status in the request encoding. Missing Content-Type
   * retains the legacy ordinary-protobuf JSON input decoder. Acceptance
   * stages an in-memory diagnostic summary; it is not durable raw-span storage.
   *
   * @returns any Spans accepted (truncated summary staged in flush accumulator).
   * @throws ApiError
   */
  public static ingestOtlpSpans(): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/otel/v1/traces',
      errors: {
        400: `Invalid OTLP payload; google.rpc.Status in the request encoding.`,
        401: `Missing or invalid bearer; google.rpc.Status in the request encoding.`,
        402: `Telemetry is unavailable on the account plan; google.rpc.Status in the request encoding.`,
        415: `Unsupported media type or content encoding.`,
        429: `Request rate limit exceeded; google.rpc.Status with Retry-After.`,
      },
    });
  }
}
