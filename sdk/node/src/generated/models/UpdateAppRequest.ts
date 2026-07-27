/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial update — every field is optional; omitted fields are unchanged.
 */
export type UpdateAppRequest = {
  ram_mb?: number | null;
  idle_timeout_s?: number | null;
  max_concurrency?: number | null;
  min_instances?: number | null;
  /**
   * v4 or v6 CIDR allowlist; empty array clears to chain-default-accept.
   */
  egress_allowlist?: Array<string>;
  /**
   * Per-instance RPS target for the reactive scale-up trigger. 0 = disable. Hobby/Pro/Scale only. Values < 0 are 422 invalid_autoscale_target_rps.
   */
  autoscale_target_rps?: number | null;
  /**
   * Per-instance CPU% target (1..100, 0 = disable) for the reactive scale-up trigger. Pro/Scale only. Values outside [1, 100] (other than 0) are 422 invalid_autoscale_target_cpu_pct.
   */
  autoscale_target_cpu_pct?: number | null;
};

