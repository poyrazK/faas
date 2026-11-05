/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryPolicy } from './WorkflowRecoveryPolicy.js';
/**
 * Explicit full policy replacement fenced to its observed version. A first configuration uses version zero.
 */
export type SetWorkflowRecoveryPolicyRequest = {
  expected_version: number;
  policy: WorkflowRecoveryPolicy;
};

