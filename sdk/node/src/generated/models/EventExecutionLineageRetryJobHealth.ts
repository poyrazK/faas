/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageRetryJobHealthSample } from './EventExecutionLineageRetryJobHealthSample.js';
/**
 * Bounded retained pending and paused lineage retry jobs at observed_at. Independent of the execution window and handler outcome completeness. Earliest retention deadlines are observed first. When counts_complete is false counts are lower bounds and oldest/next timestamps describe only observed jobs. Stalled pending is an advisory inactivity warning; pause/resume activity can reset inactivity without changing last progress. Legacy evaluated jobs without a progress timestamp report unknown progress. No handler payloads or case selections are included.
 */
export type EventExecutionLineageRetryJobHealth = {
  coverage: 'bounded_retained_active_lineage_retry_jobs';
  counts_complete: boolean;
  job_limit: number;
  inactivity_warning_seconds: number;
  retention_warning_seconds: number;
  observed_jobs: number;
  pending_jobs: number;
  paused_jobs: number;
  remaining_cases: number;
  pending_remaining_cases: number;
  paused_remaining_cases: number;
  stalled_pending_jobs: number;
  retention_risk_jobs: number;
  unknown_progress_jobs: number;
  oldest_pending_activity_at?: string;
  next_retain_until?: string;
  jobs: Array<EventExecutionLineageRetryJobHealthSample>;
};

