/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryPolicyDecision } from './WorkflowRecoveryPolicyDecision.js';
/**
 * Matching retained native target, with preparation eligibility simulated under proposed settings and retained deduplication. Would_prepare never creates a plan and does not guarantee execution quota or admission. Policy_disabled means an eligible target would not be prepared while disabled.
 */
export type WorkflowRecoveryPolicyChangeCandidate = {
  job_id: string;
  decision: WorkflowRecoveryPolicyDecision;
};

