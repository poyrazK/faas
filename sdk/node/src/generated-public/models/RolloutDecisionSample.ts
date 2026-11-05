/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One side's aggregates over the observation window. Request counts are organic requests; probe counts are reported separately.
 */
export type RolloutDecisionSample = {
  requests: number;
  server_errors: number;
  probe_requests?: number;
  probe_server_errors?: number;
  p95_latency_ms?: number;
  cold_boot_requests?: number;
  cold_boot_p95_latency_ms?: number;
  cpu_requests?: number;
  cpu_usec?: number;
  dependency_calls?: number;
  dependency_errors?: number;
};

