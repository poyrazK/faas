/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable limits admitted and enforced for one execution.
 */
export type ResolvedExecutionLimits = {
  /**
   * Standard preserves existing plan limits; extended opts into an admission-to-result budget up to 300000 ms and a smaller shared concurrency cap.
   */
  execution_class?: 'standard' | 'extended';
  max_artifact_bytes?: number;
  timeout_ms: number;
  memory_mb: number;
  cpu_millicores: number;
  ephemeral_disk_mb: number;
  max_output_bytes: number;
  pids_max: number;
};

