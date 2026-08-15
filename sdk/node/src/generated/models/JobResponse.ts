/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only view of one job. `kind` is the closed
 * vocabulary "job" (reserved for future expansion to
 * `cron`/`webhook` once those get the job-shaped
 * surface). `status` is `active` or `disabled`. env_overrides
 * is plaintext key=value pairs (NOT sealed — secrets are
 * out of scope for jobs per ADR-099 §Decision 10).
 *
 */
export type JobResponse = {
  id: string;
  name: string;
  kind: 'job';
  /**
   * OCI image digest (`sha256:...`) or named ref (`ref:...`).
   */
  image_ref: string;
  ram_mb: number;
  task_timeout_s: number;
  max_parallelism: number;
  retry_max: number;
  /**
   * Plaintext key=value pairs.
   */
  env_overrides?: Record<string, string>;
  status: 'active' | 'disabled';
  created_at: string;
  updated_at: string;
};

