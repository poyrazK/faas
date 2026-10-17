/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe original run identity, creation time and recorded result only.
 */
export type AutomationLiveSuiteObservation = {
  run_id?: string;
  /**
   * Original linked verification creation time, when retained. Never refreshed by grouping or checking runs.
   */
  created_at?: string;
  /**
   * Result of the original run independently of target drift. Unavailable includes missing runs and terminal runs without a current assertion report.
   */
  result: 'passed' | 'failed' | 'pending' | 'not_started' | 'unavailable';
};

