/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileRouteLabelCoverage } from './ProfileRouteLabelCoverage.js';
/**
 * Advisory route CPU/request requires reconciled labeling shares of at least 80 percent in both windows and a change below 20 percentage points in either direction. Missing reports or failed reconciliation produces insufficient data.
 */
export type ProfileRouteLabelComparison = {
  baseline?: ProfileRouteLabelCoverage;
  candidate?: ProfileRouteLabelCoverage;
  available: boolean;
  consistent: boolean;
  reason: string;
  delta_percentage_points?: number;
  minimum_percent: number;
  maximum_change_percentage_points: number;
};

