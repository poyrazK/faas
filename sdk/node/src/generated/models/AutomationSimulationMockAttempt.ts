/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One mocked attempt; success requires output, failure requires exactly one of error or non-2xx http_status, and timeout has no additional fields.
 */
export type AutomationSimulationMockAttempt = {
  outcome: 'success' | 'failure' | 'timeout';
  /**
   * Successful action result or received event/callback payload; any JSON value, including null.
   */
  output?: any;
  /**
   * Mocked transport or action error message used as failure.message.
   */
  error?: string;
  /**
   * Mocked non-2xx response status.
   */
  http_status?: number;
};

