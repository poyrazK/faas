/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable snapshot-flow evidence in this replica view. Unknown age is included in pending. Known boundary timing is required only when pending exceeds unknown_age; otherwise both timing fields are absent. A boundary can be capture park time, init publication or retained gate/verification evidence and may precede the rollout. Age is not continuous residence in a class, an execution deadline or proof of deployment readiness. The unknown class requires pending equal to unknown_age. The integer ceiling is a wire bound, not an admission quota.
 */
export type ObsRuntimePostCaptureClassValues = {
  pending: number;
  unknown_age: number;
  oldest_boundary_at?: string;
  oldest_known_age_seconds?: number;
};

