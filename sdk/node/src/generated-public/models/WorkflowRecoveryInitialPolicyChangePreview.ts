/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryInitialPolicyChangeCandidate } from './WorkflowRecoveryInitialPolicyChangeCandidate.js';
/**
 * Bounded read-only sample of retained generation-zero failures, present for preview policies with initial_failure and automatic_initial proposals. Matching workflow and failure-status filters sort first, then ascending failure ID; at most five are inspected. Coverage is always partial and counts describe only this sample. Candidate preparation and submission simulate processing the sample in order, after sampled parent-job preparations; later candidates can hit simulated cooldown or slot pressure. Quota and unresolved fields report the current snapshot before simulation. Applying the proposal clears the current pause but retains charges and unresolved work. Actual workers rescan and repeat admission checks. No policy, review, cursor, evidence, job or notification is written or pruned.
 */
export type WorkflowRecoveryInitialPolicyChangePreview = {
  coverage: 'partial';
  inspected_failures: number;
  truncated: boolean;
  /**
   * Sampled failures passing policy filters and fresh safety diagnostics; preparation or execution pressure can still block them.
   */
  eligible: number;
  /**
   * Sampled failures excluded by policy settings, including disabled proposals.
   */
  excluded: number;
  /**
   * Sampled policy-matching failures blocked by live safety or changed evidence.
   */
  safety_blocked: number;
  would_prepare: number;
  would_submit: number;
  /**
   * Initial reviews that count after normal history pruning, plus unresolved automatic submissions retained indefinitely. Preview does not perform that pruning.
   */
  retained_reviews: number;
  remaining_review_capacity: number;
  /**
   * Active native or routing recovery jobs across the account, including paused jobs.
   */
  active_recovery_jobs: number;
  remaining_recovery_job_capacity: number;
  /**
   * Current app has unresolved automatic or native recovery work that blocks a new automatic first submission.
   */
  unresolved_recovery: boolean;
  /**
   * HTTP server runtime availability. When false, automatic submission decisions report workflow_runtime_disabled.
   */
  runtime_available?: boolean;
  candidates: Array<WorkflowRecoveryInitialPolicyChangeCandidate>;
};

