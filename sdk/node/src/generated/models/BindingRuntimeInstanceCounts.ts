/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Disjoint counts compared with the app-wide change stamp. Current means admitted after the stamp; stale means admitted at or before it; unknown means missing stamp or start timestamp.
 */
export type BindingRuntimeInstanceCounts = {
  current: number;
  stale: number;
  unknown: number;
};

