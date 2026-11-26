/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxComparisonReport } from './APISandboxComparisonReport.js';
export type APISandboxApprovalCheck = {
  comparison: APISandboxComparisonReport;
  /**
   * Current verdict for release review.
   */
  verdict: 'running' | 'passed' | 'regression' | 'inconclusive' | 'stale';
  stale_reason?: 'candidate_artifact_changed' | 'live_baseline_changed' | 'baseline_scenario_changed';
};

