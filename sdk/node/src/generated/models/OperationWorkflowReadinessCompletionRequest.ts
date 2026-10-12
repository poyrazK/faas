/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessOutcomeEscalationPolicy } from './OperationWorkflowReadinessOutcomeEscalationPolicy.js';
/**
 * An asserted coordination outcome with a required note; resolved requires at least one retained evidence Operation. Closing does not prove business resolution.
 */
export type OperationWorkflowReadinessCompletionRequest = {
  escalation_policy?: OperationWorkflowReadinessOutcomeEscalationPolicy;
  /**
   * OperationWorkflowReadinessCompletionRequest.watch_outcome. Opt in at completion to observe a pending or unknown business result; requires expected_outcome_code and a source workflow. The watch expires with source inbox retention.
   */
  watch_outcome?: boolean;
  /**
   * OperationWorkflowReadinessCompletionRequest.outcome_deadline_at. Optional future UTC deadline requiring watch_outcome. The first successful due check without an observed achieved outcome emits one notice; this is not business failure or a historical deadline verdict.
   */
  outcome_deadline_at?: string;
  /**
   * OperationWorkflowReadinessCompletionRequest.expected_outcome_code. Optional caller-selected terminal outcome slug compared with application reports for the source workflow; never defines or modifies application success rules.
   */
  expected_outcome_code?: string;
  /**
   * OperationWorkflowReadinessCompletionRequest.watch_verification. Opt in at completion to watch up to 16 currently pending saved resolutions; requires comparable current pending evidence.
   */
  watch_verification?: boolean;
  /**
   * OperationWorkflowReadinessCompletionRequest.outcome. Retained follow-up completion value; never changes application evidence.
   */
  outcome: 'resolved' | 'no_longer_applicable' | 'handed_off';
  /**
   * OperationWorkflowReadinessCompletionRequest.note. Retained follow-up completion value; never changes application evidence.
   */
  note: string;
  /**
   * OperationWorkflowReadinessCompletionRequest.evidence_operation_ids. Retained follow-up completion value; never changes application evidence.
   */
  evidence_operation_ids: Array<string>;
};

