/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationSimulationMockAttempt } from './AutomationSimulationMockAttempt.js';
import type { WorkflowSpec } from './WorkflowSpec.js';
/**
 * Sample workflow data and mocked action or timeout outcomes for a stateless simulation.
 */
export type SimulateAutomationRequest = {
  definition: WorkflowSpec;
  /**
   * Workflow input as any JSON value; omission is equivalent to null.
   */
  input?: any;
  /**
   * Successful action outputs keyed by root step name; explicit null is a supplied result.
   */
  mock_outputs?: Record<string, any>;
  /**
   * Ordered successful output prefix keyed by for_each root name; waits and controls cannot be mocked.
   */
  mock_item_outputs?: Record<string, Array<any>>;
  /**
   * Ordered per-attempt outcomes keyed by action name; waits accept one timeout outcome when they have an on_timeout route. Action timeouts also require on_timeout. Cannot be combined with mock_outputs for the same step.
   */
  mock_attempts?: Record<string, Array<AutomationSimulationMockAttempt>>;
};

