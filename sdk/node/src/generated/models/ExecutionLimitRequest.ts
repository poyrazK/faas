/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Caller-selected execution resource limits; zero selects the plan default.
 */
export type ExecutionLimitRequest = {
  /**
   * Zero uses the plan default.
   */
  timeout_ms?: number;
  memory_mb?: 0 | 128 | 256 | 512 | 1024;
  cpu_millicores?: 0 | 250 | 500 | 1000;
  ephemeral_disk_mb?: 0 | 64 | 128 | 256 | 512 | 1024 | 2048;
  /**
   * Combined result/stdout/stderr cap; zero uses the plan default.
   */
  max_output_bytes?: number;
};

