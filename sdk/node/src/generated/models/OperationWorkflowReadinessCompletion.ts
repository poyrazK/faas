/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessBusinessOutcome } from './OperationWorkflowReadinessBusinessOutcome.js';
import type { OperationWorkflowReadinessCompletionEvidence } from './OperationWorkflowReadinessCompletionEvidence.js';
/**
 * Immutable recorded follow-up completion with server-derived actor, write time and advisory pre-write evidence check. Retained with the original notification.
 */
export type OperationWorkflowReadinessCompletion = {
  /**
   * OperationWorkflowReadinessCompletion.expected_outcome_code. Optional caller-selected terminal outcome slug compared with application reports for the source workflow; never defines or modifies application success rules.
   */
  expected_outcome_code?: string;
  business_outcome?: OperationWorkflowReadinessBusinessOutcome;
  /**
   * OperationWorkflowReadinessCompletion.watch_verification. Opt in at completion to watch up to 16 currently pending saved resolutions; requires comparable current pending evidence.
   */
  watch_verification?: boolean;
  /**
   * OperationWorkflowReadinessCompletion.outcome. Retained follow-up completion value; never changes application evidence.
   */
  outcome: 'resolved' | 'no_longer_applicable' | 'handed_off';
  /**
   * OperationWorkflowReadinessCompletion.note. Retained follow-up completion value; never changes application evidence.
   */
  note: string;
  /**
   * OperationWorkflowReadinessCompletion.evidence_operation_ids. Retained follow-up completion value; never changes application evidence.
   */
  evidence_operation_ids: Array<string>;
  /**
   * OperationWorkflowReadinessCompletion.completed_at. Retained follow-up completion value; never changes application evidence.
   */
  completed_at: string;
  /**
   * OperationWorkflowReadinessCompletion.completed_by. Retained follow-up completion value; never changes application evidence.
   */
  completed_by: string;
  evidence: OperationWorkflowReadinessCompletionEvidence;
};

