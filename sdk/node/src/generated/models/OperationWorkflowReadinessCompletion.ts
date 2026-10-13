/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookCompletionOutcome } from './OperationWorkflowPlaybookCompletionOutcome.js';
import type { OperationWorkflowReadinessBusinessOutcome } from './OperationWorkflowReadinessBusinessOutcome.js';
import type { OperationWorkflowReadinessCompletionEvidence } from './OperationWorkflowReadinessCompletionEvidence.js';
import type { OperationWorkflowReadinessOutcomeEscalationPolicy } from './OperationWorkflowReadinessOutcomeEscalationPolicy.js';
/**
 * Immutable recorded follow-up completion with server-derived actor, write time and advisory pre-write evidence check. Retained with the original notification.
 */
export type OperationWorkflowReadinessCompletion = {
  /**
   * OperationWorkflowReadinessCompletion.playbook_review_after_seconds. Optional review window after business-aware playbook completion; zero disables. Monitoring also ends with source inbox retention.
   */
  playbook_review_after_seconds?: number;
  playbook_outcome?: OperationWorkflowPlaybookCompletionOutcome;
  escalation_policy?: OperationWorkflowReadinessOutcomeEscalationPolicy;
  /**
   * OperationWorkflowReadinessCompletion.watch_outcome. Opt in at completion to observe a pending or unknown business result; requires expected_outcome_code and a source workflow. The watch expires with source inbox retention.
   */
  watch_outcome?: boolean;
  /**
   * OperationWorkflowReadinessCompletion.outcome_deadline_at. Optional future UTC deadline requiring watch_outcome. The first successful due check without an observed achieved outcome emits one notice; this is not business failure or a historical deadline verdict.
   */
  outcome_deadline_at?: string;
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

