/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST /v1/jobs body. Every field is required EXCEPT
 * env_overrides (default {}). The server enforces per-plan
 * caps (pkg/api/limits.go::JobMax*).
 *
 */
export type CreateJobRequest = {
  name: string;
  /**
   * Must start with `sha256:` or `ref:`.
   */
  image_ref: string;
  ram_mb: number;
  task_timeout_s: number;
  max_parallelism: number;
  retry_max: number;
  env_overrides?: Record<string, string>;
};

