/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe metadata-only comparison written to a completed debugger replay invocation.
 */
export type DebugReplayComparison = {
  source_deployment_id?: string | null;
  mirror_deployment_id?: string | null;
  source_status_code?: number;
  mirror_status_code?: number;
  source_latency_ms?: number;
  mirror_latency_ms?: number;
  status_diff?: boolean;
  crashed?: boolean;
};

