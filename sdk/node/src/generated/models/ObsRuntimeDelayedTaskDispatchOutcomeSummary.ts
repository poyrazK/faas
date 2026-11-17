/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeDelayedTaskDispatchName } from './ObsRuntimeDelayedTaskDispatchName.js';
/**
 * Full-set target classifications for one ordered dispatch outcome, independent of preview clipping. Later quiet observations do not erase earlier increased categories from retained progress.
 */
export type ObsRuntimeDelayedTaskDispatchOutcomeSummary = {
  outcome: ObsRuntimeDelayedTaskDispatchName;
  increased: number;
  unchanged: number;
  baseline: number;
  unknown: number;
};

