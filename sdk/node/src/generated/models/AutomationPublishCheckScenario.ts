/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationCheckExpectation } from './AutomationCheckExpectation.js';
import type { AutomationLiveTest } from './AutomationLiveTest.js';
import type { SimulateAutomationRequest } from './SimulateAutomationRequest.js';
/**
 * Named simulation and assertions to evaluate against a saved automation draft.
 */
export type AutomationPublishCheckScenario = {
  /**
   * Live test configuration carried by the case editor; mock publishing checks do not evaluate it.
   */
  live?: (AutomationLiveTest | null);
  name: string;
  simulation: SimulateAutomationRequest;
  expectations: Array<AutomationCheckExpectation>;
};

