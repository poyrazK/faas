/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SimulateStoredWorkflowRequest } from './SimulateStoredWorkflowRequest.js';
import type { StoredWorkflowCheckExpectation } from './StoredWorkflowCheckExpectation.js';
import type { StoredWorkflowLiveTest } from './StoredWorkflowLiveTest.js';
/**
 * Named simulation and assertions to evaluate against a saved stored workflow draft.
 */
export type StoredWorkflowPublishCheckScenario = {
  /**
   * Live test configuration carried by the case editor; mock publishing checks do not evaluate it.
   */
  live?: (StoredWorkflowLiveTest | null);
  name: string;
  simulation: SimulateStoredWorkflowRequest;
  expectations: Array<StoredWorkflowCheckExpectation>;
};

