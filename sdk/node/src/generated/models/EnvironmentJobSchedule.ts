/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FailureRules } from './FailureRules.js';
import type { SchedulePolicy } from './SchedulePolicy.js';
/**
 * Reviewed recurring schedule for a job workload. Cron and timezone map to Gregale's durable Job schedule; production dispatch remains gated until the managed Job adapter is available.
 */
export type EnvironmentJobSchedule = {
  /**
   * Five-field cron expression evaluated in timezone.
   */
  cron: string;
  /**
   * IANA timezone. Empty or omitted uses UTC.
   */
  timezone?: string;
  schedule_policy?: SchedulePolicy;
  failure_rules?: FailureRules;
};

