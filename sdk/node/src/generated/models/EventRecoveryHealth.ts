/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryJobHealth } from './EventRecoveryJobHealth.js';
export type EventRecoveryHealth = {
  capacity_wait_warning_seconds: number;
  /**
   * Running capacity-wait jobs observed within the five-minute freshness grace.
   */
  capacity_waiting_jobs: number;
  /**
   * Fresh running capacity episodes lasting at least fifteen minutes; excludes paused and expired jobs and scheduler stalls.
   */
  prolonged_capacity_wait_jobs: number;
  app_id: string;
  observed_at: string;
  stall_grace_seconds: number;
  expiry_warning_seconds: number;
  running_jobs: number;
  paused_jobs: number;
  stalled_jobs: number;
  /**
   * Running jobs with pending work approaching expiry; excludes paused jobs.
   */
  expiring_jobs: number;
  paused_expiring_jobs: number;
  jobs: Array<EventRecoveryJobHealth>;
};

