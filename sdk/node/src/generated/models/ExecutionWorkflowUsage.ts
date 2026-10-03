/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Host-measured usage summed over terminal runs; peak memory is the maximum individual run peak.
 */
export type ExecutionWorkflowUsage = {
  wall_time_ms: number;
  cpu_time_ms: number;
  peak_memory_mb: number;
  output_bytes: number;
};

