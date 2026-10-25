/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-level usage roll-up for terminal disposable executions in one UTC calendar month. Payload cleanup does not remove these ledger-backed facts.
 */
export type ExecutionUsageSummaryResponse = {
  runs: number;
  /**
   * Sum of host-measured wall time across terminal runs.
   */
  wall_time_ms: number;
  /**
   * Sum of host-measured CPU time across terminal runs.
   */
  cpu_time_ms: number;
  /**
   * Maximum host-measured peak memory across terminal runs.
   */
  peak_memory_mb: number;
  /**
   * Sum of result, stdout, and stderr bytes across terminal runs.
   */
  output_bytes: number;
  succeeded: number;
  failed: number;
  timed_out: number;
  out_of_memory: number;
  cancelled: number;
};

