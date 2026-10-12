/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Compact app-reported evidence observation preserving prerequisite group/path or verification obligation identity.
 */
export type OperationWorkflowReadinessEvidence = {
  /**
   * Evidence category.
   */
  category: 'blockers' | 'prerequisites' | 'verification';
  /**
   * Stable scoped evidence identity digest.
   */
  key: string;
  /**
   * Human-readable requirement or blocker identity.
   */
  name: string;
  /**
   * Retained observation status.
   */
  status: 'active' | 'required' | 'verified' | 'invalidated' | 'expired' | 'awaiting_verification' | 'unknown' | 'waiting' | 'terminal' | 'satisfied' | 'outcome_mismatch';
  /**
   * Reported evidence owner.
   */
  owner?: string;
  /**
   * Operation containing the reported evidence or expected verification.
   */
  operation_id?: string;
  /**
   * Operation containing the invalidating milestone.
   */
  invalidation_operation_id?: string;
  /**
   * Stable evidence content digest excluding moving age counters.
   */
  fingerprint: string;
};

