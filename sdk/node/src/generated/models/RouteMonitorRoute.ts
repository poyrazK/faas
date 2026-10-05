/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact normalized production telemetry label with absolute budgets. Omitted max_5xx_rate_bps disables errors; zero is a selected zero-error budget. Zero or omitted max_p95_ms disables latency. At least one budget is required.
 */
export type RouteMonitorRoute = {
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  path: string;
  max_5xx_rate_bps?: number;
  max_p95_ms?: number;
};

