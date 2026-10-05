/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorCustomerReport } from './RouteMonitorCustomerReport.js';
import type { RouteMonitorFinding } from './RouteMonitorFinding.js';
/**
 * Read-only absolute-budget evidence for the sole fully serving default-scope deployment. Coverage is limited to stored telemetry.
 */
export type RouteMonitorReport = {
  version: number;
  app_id: string;
  enabled: boolean;
  revision: number;
  customer_group_by?: 'tenant' | 'consumer';
  deployment_id?: string;
  commit_sha?: string;
  checked_at: string;
  observation_anchor?: string;
  coverage: 'observed_only';
  status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  reason: string;
  minimum_requests: number;
  minimum_latency_requests: number;
  routes: Array<RouteMonitorFinding>;
  customers?: RouteMonitorCustomerReport;
};

