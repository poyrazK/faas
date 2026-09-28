/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Wire projection of state.JobTask. LeaseToken is intentionally omitted (internal dispatch primitive).
 */
export type JobTaskResponse = {
  run_id: string;
  task_index: number;
  /**
   * Stable declared input identity for this task.
   */
  input_id?: string;
  /**
   * Opaque input reference passed to the guest.
   */
  input_ref?: string;
  /**
   * Versioned artifact references published by a successful task. Gregale retains metadata, not artifact bytes.
   */
  output_manifest?: {
    version?: 1;
    artifacts?: Array<{
      name: string;
      uri: string;
      size_bytes: number;
      sha256: string;
    }>;
  };
  status: 'queued' | 'claimed' | 'succeeded' | 'failed' | 'timeout' | 'cancelled' | 'oom';
  attempt: number;
  instance_id?: string;
  error_class?: 'succeeded' | 'failed' | 'timeout' | 'oom' | 'cancelled' | 'infra';
  error_message?: string;
  exit_code?: number;
  started_at?: string;
  finished_at?: string;
  created_at: string;
};

