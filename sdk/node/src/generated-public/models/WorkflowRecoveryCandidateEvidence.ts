/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryPolicy } from './WorkflowRecoveryPolicy.js';
import type { WorkflowRecoveryPolicyDecision } from './WorkflowRecoveryPolicyDecision.js';
/**
 * Immutable policy-review candidate facts, captured prospectively without execution inputs or outputs.
 */
export type WorkflowRecoveryCandidateEvidence = {
  review_id: string;
  app_id: string;
  job_id: string;
  policy_version: number;
  policy: WorkflowRecoveryPolicy;
  observed_at: string;
  decisions: Array<WorkflowRecoveryPolicyDecision>;
};

