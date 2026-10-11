/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An asserted coordination outcome with a required note; resolved requires at least one retained evidence Operation. Closing does not prove business resolution.
 */
export type OperationWorkflowReadinessCompletionRequest = {
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

