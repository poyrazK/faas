/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current running claims in the shared fleet view. Positive counts require oldest start and age; empty sets omit both timing fields. Claim age may precede the rollout and does not establish current VM activity or a new stall deadline. Count ceiling is exact-integer wire safety, not an admission quota.
 */
export type ObsRuntimeBuildRecoveryRunningValues = {
  count: number;
  oldest_started_at?: string;
  oldest_age_seconds?: number;
};

