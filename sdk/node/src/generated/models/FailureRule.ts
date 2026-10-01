/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A guest exit-code or structured application-outcome matcher and the action for that confirmed result. HTTP status evidence is not available on the scheduled-job execution path.
 */
export type FailureRule = {
  exit_codes?: Array<number>;
  outcome_codes?: Array<string>;
  action: 'retry' | 'fail_partition';
};

