/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed alarm timing and bounded recovery attempt metadata.
 */
export type DurableEntityAlarmInspection = {
  alarm_at?: string;
  attempts: number;
  next_attempt_at?: string;
  exhausted: boolean;
};

