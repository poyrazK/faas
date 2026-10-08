/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileRouteLabelCoverage } from './ProfileRouteLabelCoverage.js';
/**
 * Sampled CPU attributed to one validated static application route.
 */
export type ProfileRouteCPU = {
  readonly label_coverage?: ProfileRouteLabelCoverage;
  route: string;
  cpu_seconds: number;
  requests?: number;
  cpu_seconds_per_request?: number;
};

