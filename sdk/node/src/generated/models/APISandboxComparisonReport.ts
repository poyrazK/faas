/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxRunReport } from './APISandboxRunReport.js';
import type { APISandboxRunStep } from './APISandboxRunStep.js';
/**
 * Paired scenario results and the verdict for a sandbox comparison.
 */
export type APISandboxComparisonReport = {
  id: string;
  baseline_deployment_id: string;
  candidate_deployment_id: string;
  scenario_revision: string;
  scenario_name: string;
  verdict: 'running' | 'passed' | 'regression' | 'inconclusive';
  reason?: 'candidate_check_failed' | 'baseline_check_failed' | 'baseline_unavailable' | 'candidate_unavailable';
  failure_side?: 'baseline' | 'candidate';
  failure_code?: string;
  first_failing_step?: APISandboxRunStep;
  baseline: APISandboxRunReport;
  candidate: APISandboxRunReport;
  created_at: string;
};

