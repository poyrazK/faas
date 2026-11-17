/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Pending active or recoverable checkpoints in this scope. Unknown age is included in pending. When pending exceeds unknown_age, oldest known timing is required; otherwise both optional timing fields are absent. Phase start may precede the rollout and includes time waiting to resume. Counts obey an exact-integer wire bound, not an admission quota.
 */
export type ObsRuntimePreparationPhaseValues = {
  pending: number;
  unknown_age: number;
  oldest_phase_started_at?: string;
  oldest_known_age_seconds?: number;
};

