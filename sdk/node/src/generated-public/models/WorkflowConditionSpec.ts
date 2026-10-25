/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded scheduled checker. Each 2xx response must be a JSON object with boolean done. A false response becomes the next check's input; no compute is held between checks.
 */
export type WorkflowConditionSpec = {
  /**
   * Named app handler that checks the condition.
   */
  run: string;
  /**
   * Delay between checks; at least 1m and no more than the plan wait limit.
   */
  interval: string;
  max_attempts: number;
};

