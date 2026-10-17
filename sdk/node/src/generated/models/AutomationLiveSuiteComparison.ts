/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveSuiteCaseComparison } from './AutomationLiveSuiteCaseComparison.js';
import type { AutomationLiveSuiteComparisonCounts } from './AutomationLiveSuiteComparisonCounts.js';
import type { AutomationLiveSuiteSummary } from './AutomationLiveSuiteSummary.js';
/**
 * Read-only evidence comparison. Outdated identifies target drift, independently of recorded result validity. Contains no inputs, outputs, assertions or admission keys.
 */
export type AutomationLiveSuiteComparison = {
  baseline: AutomationLiveSuiteSummary;
  candidate: AutomationLiveSuiteSummary;
  baseline_outdated: boolean;
  candidate_outdated: boolean;
  observed_at: string;
  summary: AutomationLiveSuiteComparisonCounts;
  cases: Array<AutomationLiveSuiteCaseComparison>;
};

