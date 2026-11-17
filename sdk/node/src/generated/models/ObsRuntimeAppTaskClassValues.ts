/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable task counts in one shared fleet view. Unknown age is included in pending. Known timing exists only when pending exceeds unknown_age. Restoring, retry_wait, held and unknown have only unknown ages. Known boundaries are queued admission timestamps, running start, cancellation request, lease expiry or missed start deadline, not continuous class residence or a new health threshold. Empty or entirely unknown timing omits both age fields. Integer ceiling is wire safety, not an admission quota.
 */
export type ObsRuntimeAppTaskClassValues = {
  pending: number;
  unknown_age: number;
  oldest_boundary_at?: string;
  oldest_known_age_seconds?: number;
};

