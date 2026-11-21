/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current attempts in this scheduler scope. Unknown age is included in pending. Oldest known timing is required only when pending exceeds unknown_age, otherwise both timing fields are absent. The attempt boundary accounts for stage start, release completion, runtime invalidation and known instance activity; it is not continuous idle time. The unknown class requires pending equal to unknown_age. Counts have an exact-integer wire ceiling, not an admission quota.
 */
export type ObsRuntimeSnapshotPrimeClassValues = {
  pending: number;
  unknown_age: number;
  oldest_attempt_at?: string;
  oldest_known_age_seconds?: number;
};

