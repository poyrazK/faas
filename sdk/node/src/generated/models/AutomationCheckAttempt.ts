/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Expected ordered simulation attempt outcome and optional failure status.
 */
export type AutomationCheckAttempt = {
  outcome: 'success' | 'failure' | 'timeout';
  http_status?: number;
};

