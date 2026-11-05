/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Persisted classifier decision for one execution result.
 */
export type WorkDecision = {
  classification: string;
  action: 'retry' | 'fail_partition' | 'complete' | 'hold';
  reason: string;
  policy_version: number;
};

