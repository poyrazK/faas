/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileSourceLocation } from './ProfileSourceLocation.js';
/**
 * Inclusive CPU rates for one complete caller path. Rates are normalized by each selected window; omitted rates mean the path was not observed, not measured zero. Delta is omitted unless both sides were observed. Width is the sum of observed rates for an additive union layout. Named frames match by symbol and file within their parent path; unknown and anonymous frames also retain their line identity.
 */
export type ProfileStackDelta = {
  name: string;
  file?: string;
  baseline_line?: number;
  candidate_line?: number;
  baseline_source?: ProfileSourceLocation;
  candidate_source?: ProfileSourceLocation;
  baseline_cpu_per_second?: number;
  candidate_cpu_per_second?: number;
  delta_cpu_per_second?: number;
  width_cpu_per_second: number;
  children?: Array<ProfileStackDelta>;
};

