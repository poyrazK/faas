/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded, redacted span evidence. DB statements are sanitized fingerprints.
 */
export type DebugTelemetrySpan = {
  trace_id: string;
  span_id: string;
  parent_span_id?: string;
  name: string;
  kind: string;
  duration_nanos: number;
  status?: string;
  /**
   * SQL fingerprint with literals redacted.
   */
  db_statement?: string;
};

