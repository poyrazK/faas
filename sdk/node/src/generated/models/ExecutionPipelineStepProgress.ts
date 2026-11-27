/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current state of a named step and its latest attempt metadata.
 */
export type ExecutionPipelineStepProgress = {
  label: string;
  state: 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'out_of_memory' | 'cancelled' | 'retrying' | 'blocked' | 'contract_failed' | 'reused';
  reason?: 'pending' | 'dependencies' | 'parallel_limit' | 'capacity' | 'budget_concurrency' | 'retry_backoff' | 'admissions_disabled' | 'admission_refused' | 'fail_fast' | 'plan_identity' | 'cancelling';
  attempt?: number;
  run_id?: string;
  retry_at?: string;
  events_url?: string;
};

