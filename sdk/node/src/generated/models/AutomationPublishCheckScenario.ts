/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationCheckExpectation } from './AutomationCheckExpectation.js';
import type { SimulateAutomationRequest } from './SimulateAutomationRequest.js';
/**
 * Named simulation and assertions to evaluate against a saved automation draft.
 */
export type AutomationPublishCheckScenario = {
  name: string;
  simulation: SimulateAutomationRequest;
  expectations: Array<AutomationCheckExpectation>;
};

