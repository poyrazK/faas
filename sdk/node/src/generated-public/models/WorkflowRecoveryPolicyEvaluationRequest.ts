/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryPolicy } from './WorkflowRecoveryPolicy.js';
/**
 * Proposed policy and bounded historical review window, fenced against the current configuration version.
 */
export type WorkflowRecoveryPolicyEvaluationRequest = {
  expected_version: number;
  policy: WorkflowRecoveryPolicy;
  workflow?: string;
  /**
   * Inclusive evaluation start; defaults to seven days before the end.
   */
  created_after?: string;
  /**
   * Inclusive evaluation end; defaults to observation time. Maximum window is 30 days and future bounds are rejected.
   */
  created_before?: string;
};

