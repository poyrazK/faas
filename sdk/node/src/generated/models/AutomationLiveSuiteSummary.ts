/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained suite identity and linked-run counts. Listing is not an assertion verdict.
 */
export type AutomationLiveSuiteSummary = {
  /**
   * True for suites with durable server-managed admission. Metadata PUT cannot change these records; continuation requires explicit execution authorization.
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
};

