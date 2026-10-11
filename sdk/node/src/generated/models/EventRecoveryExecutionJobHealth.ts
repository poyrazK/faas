/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryExecutionSummary } from './EventRecoveryExecutionSummary.js';
/**
 * Wait age starts at admission completion; prolonged waits do not assert scheduler or handler failure. retain_until is the nominal job retention boundary. Unknown includes admitted items without replay identity. Awaiting saved results counts known terminal observations without saved confirmation.
 */
export type EventRecoveryExecutionJobHealth = {
  job_id: string;
  parent_job_id?: string;
  state: 'completed' | 'cancelled';
  status: 'waiting' | 'prolonged_wait' | 'unknown' | 'retention_risk';
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

