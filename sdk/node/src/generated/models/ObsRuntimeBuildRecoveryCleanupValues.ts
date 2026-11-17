/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Pending durable obligations include claimed and retrying rows. Retried is the subset with a recorded failed attempt, including later live claims; it is not an attempt count or retry rate. Age starts at the oldest obligation creation and survives retries. Positive pending requires oldest creation and age; empty sets omit both timing fields.
 */
export type ObsRuntimeBuildRecoveryCleanupValues = {
  pending: number;
  retried: number;
  oldest_created_at?: string;
  oldest_age_seconds?: number;
};

