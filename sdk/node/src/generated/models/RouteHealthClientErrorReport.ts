/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthClientErrorFinding } from './RouteHealthClientErrorFinding.js';
/**
 * Live advisory watched-code evidence, independent of the 5xx/latency verdict. Each code uses at least 20 represented requests on both deployments per window, two matching regression windows, at least two candidate responses, a rate of at least 5 percent, three times stable, and at least five percentage points above stable. Stable expected rejection rates remain healthy. Sparse, one-sided, missing or pre-anchor evidence is unknown. Coverage inherits observed_only from the containing report; these observations neither prove a defect nor certify an SLO. Excluded from saved decisions, recovery and webhook payloads.
 */
export type RouteHealthClientErrorReport = {
  status: 'healthy' | 'regressed' | 'unknown';
  reason: string;
  minimum_requests: number;
  minimum_responses: number;
  rate_floor: number;
  rate_delta: number;
  rate_factor: number;
  statuses: Array<RouteHealthClientErrorFinding>;
};

