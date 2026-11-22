/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Host validation of a successful result. Invalid contracts preserve execution status, original result, artifacts, and usage; dependent workflows fail and CLI waits return nonzero.
 */
export type ExecutionResultValidation = {
  status: 'valid' | 'invalid';
  message?: 'Run result does not match result_schema';
};

