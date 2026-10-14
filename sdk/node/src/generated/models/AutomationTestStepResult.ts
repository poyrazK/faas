/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One assertion result and observed state, excluding input, output and error data.
 */
export type AutomationTestStepResult = {
  step: string;
  loop?: string;
  item_index?: number;
  state?: string;
  passed: boolean;
};

