/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Deterministic JSON HTTP response or connection fault. Redirects are refused. Timeout requires a positive bounded delay, then closes the connection without an HTTP response. This does not attest which error the application observed.
 */
export type ScenarioSimulationResponse = {
  /**
   * Required for HTTP responses. Status 300 through 399 is unsupported. Absent for faults.
   */
  status?: number;
  /**
   * Literal JSON response body. Absent for faults.
   */
  body?: string;
  /**
   * Supported with status 429 or 503 only.
   */
  retry_after_seconds?: number;
  fault?: 'timeout' | 'disconnect';
  delay_ms?: number;
};

