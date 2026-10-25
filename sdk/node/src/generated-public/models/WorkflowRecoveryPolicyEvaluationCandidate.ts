/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryFailureEligibilityAssessment } from './WorkflowRecoveryFailureEligibilityAssessment.js';
import type { WorkflowRecoveryPolicyAssessment } from './WorkflowRecoveryPolicyAssessment.js';
/**
 * One retained historical decision with current and proposed settings applied to the same captured evidence.
 */
export type WorkflowRecoveryPolicyEvaluationCandidate = {
  later_eligibility?: WorkflowRecoveryFailureEligibilityAssessment;
  /**
   * Source of historical candidate facts.
   */
  evidence_source: 'policy_review' | 'failure_inventory';
  /**
   * Stable identifier for a captured policy-independent failure.
   */
  failure_evidence_id?: string;
  /**
   * Durable history event containing the candidate facts when available.
   */
  candidate_evidence_id?: string;
  review_id?: string;
  job_id?: string;
  run_id: string;
  workflow: string;
  observed_at: string;
  policy_version?: number;
  position?: number;
  resume_count: number;
  trigger: string;
  recorded_outcome: string;
  /**
   * Exact observed status as a singleton when captured; otherwise the legacy policy filter represents a possible status set. Empty means unrestricted and unrecorded.
   */
  historical_failure_statuses: Array<string>;
  observed_hourly_executions: number;
  last_observed_execution_at?: string;
  /**
   * Bounded application proof identifiers from recovery history, including last known cooldown evidence when older than one hour.
   */
  execution_evidence_ids: Array<string>;
  current: WorkflowRecoveryPolicyAssessment;
  proposed: WorkflowRecoveryPolicyAssessment;
};

