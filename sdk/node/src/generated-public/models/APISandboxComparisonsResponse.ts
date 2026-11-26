/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxComparisonBaseline } from './APISandboxComparisonBaseline.js';
import type { APISandboxComparisonReport } from './APISandboxComparisonReport.js';
/**
 * Eligible baselines and recent comparisons for the selected release.
 */
export type APISandboxComparisonsResponse = {
  comparisons: Array<APISandboxComparisonReport>;
  baselines: Array<APISandboxComparisonBaseline>;
};

