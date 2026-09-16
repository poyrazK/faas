/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable limits admitted and enforced for one execution.
 */
export type ResolvedExecutionLimits = {
  timeout_ms: number;
  memory_mb: number;
  cpu_millicores: number;
  ephemeral_disk_mb: number;
  max_output_bytes: number;
  pids_max: number;
};

