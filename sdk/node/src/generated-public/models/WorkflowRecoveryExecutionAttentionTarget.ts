/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Advisory observation of one admitted native workflow generation with run and diagnostics links.
 */
export type WorkflowRecoveryExecutionAttentionTarget = {
  position: number;
  run_id: string;
  resume_count: number;
  status: 'unknown' | 'queued' | 'running' | 'capacity_wait' | 'scheduled_wait' | 'stalled' | 'deadline_exceeded';
  wait_reason?: string;
  admitted_at?: string;
  last_progress_at?: string;
  deadline_at?: string;
  run_url: string;
  diagnostics_url: string;
};

