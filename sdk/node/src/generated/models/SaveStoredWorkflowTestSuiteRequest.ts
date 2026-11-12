/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowTestSuite } from './StoredWorkflowTestSuite.js';
/**
 * Versioned suite replacement; explicitly sending null removes saved data and results.
 */
export type SaveStoredWorkflowTestSuiteRequest = {
  expected_version: number;
  /**
   * Null clears saved tests and the retained report. Saving advances the stored workflow version and invalidates earlier publishing receipts.
   */
  suite: (StoredWorkflowTestSuite | null);
};

