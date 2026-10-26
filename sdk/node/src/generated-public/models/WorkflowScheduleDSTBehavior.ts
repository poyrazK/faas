/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Gregale's timezone-transition behavior for fixed wall-time and interval cron expressions.
 */
export type WorkflowScheduleDSTBehavior = {
  mode: 'fixed_wall_time' | 'interval';
  spring_gap: 'shift_to_first_valid_minute' | 'follow_cron_interval';
  fall_fold: 'run_once_at_first_occurrence' | 'repeat_as_clock_falls_back';
};

