/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationSimulationMockAttempt } from './AutomationSimulationMockAttempt.js';
/**
 * Saved test data — Explicitly saved synthetic inputs and mocks. Runs always bind the current saved draft.
 */
export type AutomationTestInputs = {
  /**
   * Saved test data — Workflow input as any JSON value; omission is equivalent to null.
   */
  input?: any;
  /**
   * Saved test data — Successful action outputs keyed by root step name; explicit null is a supplied result.
   */
  mock_outputs?: Record<string, any>;
  /**
   * Saved test data — Ordered successful output prefix keyed by for_each root name; waits and controls cannot be mocked.
   */
  mock_item_outputs?: Record<string, Array<any>>;
  /**
   * Saved test data — Per-item attempt outcomes keyed by for_each root name and canonical zero-based item index (0..127). Sparse indexes are allowed but must exist in the materialized collection. Cannot be combined with mock_item_outputs for the same loop. Item timeouts require an action timeout and follow its retry policy. Terminal item failures stop later items unless on_item_failure is continue; the loop remains failed or dead, with null placeholders for failed items when continuing.
   */
  mock_item_attempts?: Record<string, Record<string, Array<AutomationSimulationMockAttempt>>>;
  /**
   * Saved test data — Ordered per-attempt outcomes keyed by action or wait name. Event and callback waits accept exactly one success with the received payload as output, or one timeout with a configured timeout and on_timeout route. Other waits accept timeout only. Successful wait payloads cannot be the reserved timeout sentinel. Action timeouts require on_timeout. Cannot be combined with mock_outputs for the same step.
   */
  mock_attempts?: Record<string, Array<AutomationSimulationMockAttempt>>;
};

