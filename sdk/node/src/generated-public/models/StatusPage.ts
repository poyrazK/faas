/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StatusIncident } from './StatusIncident.js';
import type { StatusUptimeBucket } from './StatusUptimeBucket.js';
/**
 * Backwards-compatible three-indicator status response.
 */
export type StatusPage = {
  api_availability_pct: number;
  wake_p95_ms: number;
  build_success_pct: number;
  uptime_30d_pct: number;
  uptime_30d: Array<StatusUptimeBucket>;
  incidents: Array<StatusIncident>;
  degraded: boolean;
  as_of: string;
  source: string;
};

