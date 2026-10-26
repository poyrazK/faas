/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FailureRules } from './FailureRules.js';
/**
 * Wire projection of state.JobRun. Aggregate counters are recomputed by schedd after every terminal task transition.
 */
export type JobRunResponse = {
  id: string;
  job_id: string;
  account_id: string;
  trigger_kind: 'manual' | 'scheduled' | 'triggered';
  /**
   * Durable scheduled-occurrence decision linked to this run.
   */
  occurrence_id?: string;
  /**
   * Latest permitted first task start for this scheduled occurrence.
   */
  start_deadline_at?: string;
  failure_rules?: FailureRules;
  env_overrides?: Record<string, string>;
  tasks: number;
  /**
   * 0 for numeric fan-out, 1 for an ordered inline or external input manifest.
   */
  input_manifest_version?: number;
  /**
   * SHA-256 of the canonical ordered input manifest.
   */
  input_digest?: string;
  /**
   * Source obj:// URI for an external input manifest.
   */
  input_manifest_uri?: string;
  /**
   * SHA-256 of the external manifest's exact bytes.
   */
  input_manifest_sha256?: string;
  parallelism: number;
  execution_class: 'standard' | 'flexible';
  failure_policy: 'continue' | 'fail_fast';
  eligible_at?: string;
  latest_start_at?: string;
  retry_max?: number;
  task_timeout_sec?: number;
  /**
   * Command captured at run creation.
   */
  command?: Array<string>;
  image_ref_snapshot?: string;
  image_resolved_digest_snapshot?: string;
  ram_mb_snapshot?: number;
  effective_env_snapshot?: Record<string, string>;
  source_run_id?: string;
  aggregate_status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'dead_letter';
  tasks_succeeded: number;
  tasks_failed: number;
  tasks_cancelled: number;
  tasks_running: number;
  dead_letter_count: number;
  started_at?: string;
  finished_at?: string;
  created_at: string;
};

