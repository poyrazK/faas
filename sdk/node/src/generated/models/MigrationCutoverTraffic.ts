/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Existing deployment and exact GET route observations after hostname proof. Aggregates are not hostname-specific and never prove every customer moved.
 */
export type MigrationCutoverTraffic = {
  status: 'unknown' | 'healthy' | 'violated';
  coverage: 'deployment_route_observed_only';
  reason: string;
  requests: number;
  server_errors: number;
  worst_window_p95_ms?: number;
  checked_at?: string;
};

