/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveSuiteCase } from './AutomationLiveSuiteCase.js';
import type { AutomationLiveSuiteCounts } from './AutomationLiveSuiteCounts.js';
/**
 * Server-derived observation of retained verification records. GET is read-only and never checks assertions or admits work. Missing records remain unavailable with their known IDs. Passed requires every linked case to have a current passing report and an unchanged saved version, published revision and deployment. Not a publication receipt.
 */
export type AutomationLiveSuite = {
  /**
   * True when the suite owns durable server-managed execution and can be continued with explicit real-action confirmation.
   */
  managed?: boolean;
  id: string;
  name: string;
  version: number;
  published_version: number;
  deployment_id: string;
  created_at: string;
  updated_at: string;
  case_count: number;
  linked_runs: number;
  outcome: 'passed' | 'failed' | 'pending' | 'incomplete' | 'outdated' | 'unavailable';
  outdated: boolean;
  observed_at: string;
  summary: AutomationLiveSuiteCounts;
  cases: Array<AutomationLiveSuiteCase>;
};

