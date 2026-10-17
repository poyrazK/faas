/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveReport } from './AutomationLiveReport.js';
/**
 * Safe provenance and status for one explicitly authorized live verification run.
 */
export type AutomationLiveVerification = {
  run_id: string;
  case_name: string;
  deployment_id: string;
  inputs_hash: string;
  run_status: string;
  version: number;
  published_version: number;
  created_at: string;
  /**
   * True when the saved case, published revision, or current default deployment differs from the frozen target.
   */
  outdated: boolean;
  /**
   * True when the report still matches this run status, resume count and update time; also require outdated=false for current-target evidence.
   */
  report_current: boolean;
  report?: AutomationLiveReport;
};

