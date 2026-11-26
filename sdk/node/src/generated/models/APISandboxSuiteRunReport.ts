/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxSuiteRunSummary } from './APISandboxSuiteRunSummary.js';
import type { APISandboxSuiteScenarioResult } from './APISandboxSuiteScenarioResult.js';
export type APISandboxSuiteRunReport = (APISandboxSuiteRunSummary & {
  verdict: 'running' | 'passed' | 'regression' | 'inconclusive' | 'stale';
  stale_reason?: 'candidate_artifact_changed' | 'live_baseline_changed' | 'suite_changed' | 'suite_scenario_changed';
  failure_code?: 'timed_out' | 'approval_inputs_changed' | 'entitlement_changed';
  scenarios: Array<APISandboxSuiteScenarioResult>;
});

