/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronTimingName } from './ObsRuntimeCommandCronTimingName.js';
/**
 * Full-set target classifications for one closed timing signal; counts are independent of preview clipping.
 */
export type ObsRuntimeCommandCronTimingSignalSummary = {
  signal: ObsRuntimeCommandCronTimingName;
  increased: number;
  unchanged: number;
  baseline: number;
  unknown: number;
};

