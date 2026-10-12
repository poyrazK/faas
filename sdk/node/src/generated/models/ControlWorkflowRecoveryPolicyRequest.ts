/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operational pause or resume using the last observed policy and pause state. Omit expected_paused_at when the observed policy was unpaused. Every accepted control updates updated_at and records a trusted audit actor and reason without changing the policy version. A retry using stale state returns conflict.
 */
export type ControlWorkflowRecoveryPolicyRequest = {
  expected_version: number;
  expected_updated_at: string;
  expected_paused_at?: string;
  operation: 'pause' | 'resume';
  reason: string;
};

