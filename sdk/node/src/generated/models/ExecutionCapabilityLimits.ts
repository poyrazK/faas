/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Plan limits and fixed request-shape caps for Runs.
 */
export type ExecutionCapabilityLimits = {
  max_concurrent_runs: number;
  max_source_bytes: number;
  max_input_bytes: number;
  default_output_bytes: number;
  max_output_bytes: number;
  default_timeout_ms: number;
  max_timeout_ms: number;
  default_memory_mb: number;
  max_memory_mb: number;
  default_cpu_millicores: number;
  max_cpu_millicores: number;
  default_ephemeral_disk_mb: number;
  max_ephemeral_disk_mb: number;
  pids_max: number;
  max_bundle_files: number;
  max_artifact_inputs: number;
  max_output_files: number;
  max_artifact_path_bytes: number;
};

