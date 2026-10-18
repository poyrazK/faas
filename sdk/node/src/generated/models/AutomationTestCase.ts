/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationCheckExpectation } from './AutomationCheckExpectation.js';
import type { AutomationLiveTest } from './AutomationLiveTest.js';
import type { AutomationTestInputs } from './AutomationTestInputs.js';
/**
 * A uniquely named set of definition-free mocks and expected step results.
 */
export type AutomationTestCase = {
  /**
   * Optional live assertions saved with this case; null disables its live test.
   */
  live?: (AutomationLiveTest | null);
  name: string;
  simulation: AutomationTestInputs;
  expectations: Array<AutomationCheckExpectation>;
};

