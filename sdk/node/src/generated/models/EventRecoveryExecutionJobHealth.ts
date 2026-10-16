/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryExecutionSummary } from './EventRecoveryExecutionSummary.js';
import type { WorkflowRecoveryExecutionAttention } from './WorkflowRecoveryExecutionAttention.js';
/**
 * Wait age starts at admission completion or creation while admission is active; prolonged waits do not assert scheduler or handler failure. retain_until is the nominal job retention boundary. Unknown includes admitted items without replay identity. Awaiting saved results counts known terminal observations without saved confirmation.
 */
export type EventRecoveryExecutionJobHealth = {
  attention?: WorkflowRecoveryExecutionAttention;
  job_id: string;
  parent_job_id?: string;
  state: 'running' | 'paused' | 'completed' | 'cancelled' | 'expired';
  status: 'stalled' | 'deadline_exceeded' | 'capacity_wait' | 'scheduled_wait' | 'waiting' | 'prolonged_wait' | 'unknown' | 'retention_risk';
  /**
   * Admission completion time or job creation time while admission is active.
   */
  completed_at: string;
  wait_age_seconds: number;
  retain_until: string;
  prolonged_wait: boolean;
  retention_risk: boolean;
  notification_pending: boolean;
  queued_count: number;
  untracked_count: number;
  unknown_count: number;
  unresolved_count: number;
  awaiting_saved_results_count: number;
  execution: EventRecoveryExecutionSummary;
};

