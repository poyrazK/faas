/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A separate assertion against a real terminal workflow step, not a mock trace.
 */
export type AutomationLiveExpectation = {
  step: string;
  loop?: string;
  item_index?: number;
  state?: 'succeeded' | 'failed' | 'dead' | 'skipped';
  /**
   * Optional expected JSON result; an explicit null is asserted.
   */
  output?: any;
  attempt_count?: number;
};

