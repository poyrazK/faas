/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxComparisonReport } from './APISandboxComparisonReport.js';
/**
 * The result of comparing a saved scenario on both releases in a suite.
 */
export type APISandboxSuiteScenarioResult = {
  scenario_id: string;
  scenario_name: string;
  scenario_revision: string;
  status: 'queued' | 'running' | 'passed' | 'regression' | 'inconclusive' | 'not_run';
  comparison?: APISandboxComparisonReport;
};

