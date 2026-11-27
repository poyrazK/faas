/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperimentArm } from './FlagExperimentArm.js';
import type { FlagExperimentVariantResult } from './FlagExperimentVariantResult.js';
/**
 * Exploratory results of a cohort's members (members true) or of everyone else, with Benjamini-Hochberg adjusted p-values; segments never drive the recommendation.
 */
export type FlagExperimentSegment = {
  cohort: string;
  members: boolean;
  control: FlagExperimentArm;
  variants: Array<FlagExperimentVariantResult>;
};

