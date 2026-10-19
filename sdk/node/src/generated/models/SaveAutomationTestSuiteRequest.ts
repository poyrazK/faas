/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationTestSuite } from './AutomationTestSuite.js';
/**
 * Versioned suite replacement; explicitly sending null removes saved data and results.
 */
export type SaveAutomationTestSuiteRequest = {
  expected_version: number;
  /**
   * Null clears saved tests and the retained report. Saving advances the automation version and invalidates earlier publishing receipts.
   */
  suite: (AutomationTestSuite | null);
};

