/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryExecutionJobHealth } from './EventRecoveryExecutionJobHealth.js';
/**
 * Oldest retained terminal admission jobs still missing exact saved execution results. Counts overlap and are lower bounds when counts_complete is false. Reads do not capture results or notifications.
 */
export type EventRecoveryExecutionHealth = {
  coverage: 'bounded_retained_execution_jobs';
  observed_jobs: number;
  waiting_jobs: number;
  prolonged_wait_jobs: number;
  unknown_jobs: number;
  retention_risk_jobs: number;
  counts_complete: boolean;
  job_limit: 50;
  wait_warning_seconds: number;
  retention_warning_seconds: number;
  jobs: Array<EventRecoveryExecutionJobHealth>;
};

