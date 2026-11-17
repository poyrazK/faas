/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimePostCaptureClassName } from './ObsRuntimePostCaptureClassName.js';
/**
 * Replica-view counts for one ordered class over all inspected targets before clipping. Views share fleet data; these are neither unique deployment counts nor completion counters.
 */
export type ObsRuntimePostCaptureClassCounts = {
  class: ObsRuntimePostCaptureClassName;
  nonempty_views: number;
  empty_views: number;
  unknown_age_views: number;
};

