/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronClassName } from './ObsRuntimeCommandCronClassName.js';
/**
 * Scope counts for one ordered class across all inspected scheduler targets before clipping. Ownership scopes can overlap; these are not unique schedules, occurrences or successful deliveries.
 */
export type ObsRuntimeCommandCronClassCounts = {
  class: ObsRuntimeCommandCronClassName;
  nonempty_scopes: number;
  empty_scopes: number;
  unknown_age_scopes: number;
};

