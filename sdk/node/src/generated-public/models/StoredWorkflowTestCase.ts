/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowCheckExpectation } from './StoredWorkflowCheckExpectation.js';
import type { StoredWorkflowLiveTest } from './StoredWorkflowLiveTest.js';
import type { StoredWorkflowTestInputs } from './StoredWorkflowTestInputs.js';
/**
 * A uniquely named set of definition-free mocks and expected step results.
 */
export type StoredWorkflowTestCase = {
  /**
   * Optional live assertions saved with this case; null disables its live test.
   */
  live?: (StoredWorkflowLiveTest | null);
  name: string;
  simulation: StoredWorkflowTestInputs;
  expectations: Array<StoredWorkflowCheckExpectation>;
};

