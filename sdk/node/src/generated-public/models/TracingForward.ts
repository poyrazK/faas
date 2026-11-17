/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sends a copy of every OpenTelemetry trace and log export the app makes to your own OTLP/HTTP endpoint, from inside the app's instance. Requires tracing to be enabled.
 */
export type TracingForward = {
  /**
   * Base OTLP/HTTP URL (http or https, no credentials, query or fragment); /v1/traces and /v1/logs are appended.
   */
  endpoint?: string;
  /**
   * Name of an app secret holding request headers as comma-separated key=value pairs, in the OTEL_EXPORTER_OTLP_HEADERS format.
   */
  headers_secret?: string;
};

