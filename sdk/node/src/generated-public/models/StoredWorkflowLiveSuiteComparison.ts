/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowLiveSuiteCaseComparison } from './StoredWorkflowLiveSuiteCaseComparison.js';
import type { StoredWorkflowLiveSuiteComparisonCounts } from './StoredWorkflowLiveSuiteComparisonCounts.js';
import type { StoredWorkflowLiveSuiteSummary } from './StoredWorkflowLiveSuiteSummary.js';
/**
 * Read-only evidence comparison. Outdated identifies target drift, independently of recorded result validity. Contains no inputs, outputs, assertions or admission keys.
 */
export type StoredWorkflowLiveSuiteComparison = {
  baseline: StoredWorkflowLiveSuiteSummary;
  candidate: StoredWorkflowLiveSuiteSummary;
  baseline_outdated: boolean;
  candidate_outdated: boolean;
  observed_at: string;
  summary: StoredWorkflowLiveSuiteComparisonCounts;
  cases: Array<StoredWorkflowLiveSuiteCaseComparison>;
};

