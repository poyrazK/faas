/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationSimulationStep } from './AutomationSimulationStep.js';
/**
 * Definition validity, submitted-definition SHA-256 and deterministic simulated data flow.
 */
export type SimulateAutomationResponse = {
  /**
   * Whether the definition and managed integration bindings passed validation.
   */
  definition_valid: boolean;
  /**
   * SHA-256 of the JSON-serialized submitted definition, independent of samples; not a publication revision.
   */
  definition_hash: string;
  /**
   * All roots resolved or skipped under these mocks; false for missing results, waits or evaluation errors.
   */
  complete: boolean;
  issues: Array<string>;
  warnings: Array<string>;
  step_order: Array<string>;
  trace: Array<AutomationSimulationStep>;
};

