/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Progress and timestamps for a regression suite run.
 */
export type APISandboxSuiteRunSummary = {
  id: string;
  suite_revision: string;
  baseline_deployment_id: string;
  candidate_deployment_id: string;
  scenario_count: number;
  completed_count: number;
  created_at: string;
  /**
   * This run started automatically at release approval.
   */
  automatic?: boolean;
  /**
   * The next comparison is waiting for available sandbox capacity.
   */
  waiting_for_capacity?: boolean;
  finished_at?: string;
};

