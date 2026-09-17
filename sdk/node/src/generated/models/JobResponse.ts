/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Wire projection of state.Job.
 */
export type JobResponse = {
  id: string;
  account_id: string;
  name: string;
  kind: 'batch' | 'recurring';
  image_ref: string;
  /**
   * Immutable OCI manifest digest selected from image_ref.
   */
  image_resolved_digest?: string;
  /**
   * Canonical ext4 artifact key consumed by vmmd.
   */
  image_storage_key?: string;
  image_materialization_status: 'pending' | 'ready' | 'failed';
  /**
   * Actionable pull/build failure when materialization_status is failed.
   */
  image_materialization_error?: string;
  image_materialized_at?: string;
  command: Array<string>;
  env_overrides?: Record<string, string>;
  ram_mb: number;
  task_timeout_sec: number;
  max_parallelism: number;
  retry_max: number;
  status: 'active' | 'paused' | 'deleted';
  created_at: string;
  updated_at: string;
};

