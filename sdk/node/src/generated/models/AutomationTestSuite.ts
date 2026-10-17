/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationTestCase } from './AutomationTestCase.js';
import type { AutomationTestInputs } from './AutomationTestInputs.js';
/**
 * At most 32 uniquely named cases; sample up to 3 MiB, cases up to 24 MiB combined, suite up to 27 MiB. Data is explicitly persisted, never captured from real runs.
 */
export type AutomationTestSuite = {
  sample: AutomationTestInputs;
  cases: Array<AutomationTestCase>;
};

