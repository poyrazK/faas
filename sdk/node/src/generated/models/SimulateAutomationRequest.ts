/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowSpec } from './WorkflowSpec.js';
/**
 * Sample workflow data and successful action results for a stateless simulation.
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
};

