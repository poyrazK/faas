/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded, non-secret evidence from one authenticated candidate GET. Request and response bodies are omitted.
 */
export type HostingRouteCheckResult = {
  method: string;
  path: string;
  status: 'verified' | 'failed' | 'skipped';
  status_code?: number;
  latency_ms?: number;
  /**
   * Zero time (0001-01-01T00:00:00Z) when the check did not run.
   */
  verified_at: string;
  request_id?: string;
  error_code?: string;
  error?: string;
};

