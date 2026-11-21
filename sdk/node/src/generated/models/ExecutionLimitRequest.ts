/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Caller-selected execution resource limits; zero selects the plan default.
 */
export type ExecutionLimitRequest = {
  /**
   * Standard preserves existing plan limits; extended opts into an admission-to-result budget up to 300000 ms and a smaller shared concurrency cap.
   */
  execution_class?: 'standard' | 'extended';
  /**
   * Independent stored output byte budget; requires stored output_files. Zero selects the plan default constrained by scratch headroom.
   */
  max_artifact_bytes?: number;
  /**
   * Zero uses the class default: standard 5000 ms, extended 300000 ms. Standard plan maxima remain unchanged.
   */
  timeout_ms?: number;
  memory_mb?: 0 | 128 | 256 | 512 | 1024;
  cpu_millicores?: 0 | 250 | 500 | 1000;
  ephemeral_disk_mb?: 0 | 64 | 128 | 256 | 512 | 1024 | 2048;
  /**
   * Combined result/stdout/stderr/serialized-artifacts cap; zero uses the plan default.
   */
  max_output_bytes?: number;
};

