/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SchedulePolicy } from './SchedulePolicy.js';
import type { WorkDecision } from './WorkDecision.js';
/**
 * Durable decision and lifecycle snapshot for one nominal schedule time.
 */
export type ScheduleOccurrenceResponse = {
  id: string;
  schedule_revision: number;
  scheduled_for: string;
  start_deadline_at?: string;
  schedule_policy: SchedulePolicy;
  status: 'pending' | 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'skipped_overlap' | 'missed_deadline' | 'coalesced' | 'waiting_replacement' | 'uncertain';
  reason?: string;
  work_decision?: WorkDecision;
  outcome_code?: string;
  blocking_occurrence_id?: string;
  exclusive_operation_id?: string;
  job_run_id?: string;
  invocation_id?: string;
  app_task_id?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
};

