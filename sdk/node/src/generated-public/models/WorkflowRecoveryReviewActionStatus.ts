/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application freshness and execution outcome for one frozen action. A retry is complete only with saved terminal generation evidence or a terminal admission refusal.
 */
export type WorkflowRecoveryReviewActionStatus = {
  position: number;
  kind: string;
  status: string;
  reason: string;
  outcome: 'unknown' | 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  complete: boolean;
  run_id: string;
  /**
   * Exact execution generation; retry targets the next admitted generation.
   */
  resume_count: number;
  run_url: string;
  child_job_id?: string;
  items_url?: string;
  evidence_source?: string;
  recorded_at?: string;
  outcome_reason?: string;
};

