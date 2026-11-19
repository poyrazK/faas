/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHTTPCronDeliveryName } from './ObsRuntimeHTTPCronDeliveryName.js';
/**
 * Full-set target classifications for one ordered HTTP delivery outcome, independent of preview clipping. A later quiet sample does not erase an earlier increase from retained progress.
 */
export type ObsRuntimeHTTPCronDeliveryOutcomeSummary = {
  outcome: ObsRuntimeHTTPCronDeliveryName;
  increased: number;
  unchanged: number;
  baseline: number;
  unknown: number;
};

