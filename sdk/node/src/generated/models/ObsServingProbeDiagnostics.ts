/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional diagnostics from the same evaluated serving run. Available
 * diagnostics have complete, coherent fixed codes and retry metadata.
 * Unknown diagnostics have no numeric values or inferred success. This
 * section does not change coverage state. Raw URLs, bodies and exceptions
 * are excluded; none is success and unknown is an unclassified failure.
 *
 */
export type ObsServingProbeDiagnostics = {
  state: 'available' | 'unknown';
  reason?: 'diagnostics_missing' | 'query_failed' | 'coverage_unavailable' | 'diagnostics_not_current' | 'diagnostics_inconsistent';
  evaluated_at: string | null;
  failure_code?: 'none' | 'dns' | 'connection' | 'tls' | 'timeout' | 'http_status' | 'latency' | 'body_mismatch' | 'unknown';
  first_failure_code?: 'none' | 'dns' | 'connection' | 'tls' | 'timeout' | 'http_status' | 'latency' | 'body_mismatch' | 'unknown';
  attempts: number | null;
  http_status: number | null;
  recovered_on_retry: boolean | null;
};

