/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxApprovalCheck } from './APISandboxApprovalCheck.js';
import type { APISandboxLibraryScenarioSummary } from './APISandboxLibraryScenarioSummary.js';
/**
 * Prior approval checks and inputs available for a new check.
 */
export type APISandboxApprovalChecksResponse = {
  availability: 'ready' | 'not_waiting' | 'unsupported' | 'no_baseline' | 'no_scenario';
  baseline_deployment_id?: string;
  scenario_revision?: string;
  scenario_name?: string;
  library_scenarios?: Array<APISandboxLibraryScenarioSummary>;
  checks: Array<APISandboxApprovalCheck>;
};

