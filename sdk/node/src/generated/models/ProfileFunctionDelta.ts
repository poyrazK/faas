/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileSourceLocation } from './ProfileSourceLocation.js';
/**
 * Function self CPU rates normalized by each selected capture window. Numeric rates are meaningful only when the corresponding observed flag is true; delta_cpu_per_second is meaningful only when delta_known is true. Unobserved functions are not measured zero.
 */
export type ProfileFunctionDelta = {
  name: string;
  file?: string;
  line?: number;
  baseline_source?: ProfileSourceLocation;
  candidate_source?: ProfileSourceLocation;
  baseline_cpu_per_second: number;
  candidate_cpu_per_second: number;
  delta_cpu_per_second: number;
  baseline_observed: boolean;
  candidate_observed: boolean;
  delta_known: boolean;
};

