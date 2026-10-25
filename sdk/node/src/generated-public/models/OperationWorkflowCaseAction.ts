/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowCaseAction. Current evidence and advisory coordination for an exact scoped business workflow.
 */
export type OperationWorkflowCaseAction = {
  /**
   * OperationWorkflowCaseAction.kind. Advisory suggestion category; never an execution command.
   */
  kind: string;
  /**
   * OperationWorkflowCaseAction.summary. Evidence-based human next step.
   */
  summary: string;
  /**
   * OperationWorkflowCaseAction.owner. Application blocker owner, verification owner or separate human coordination owner depending on kind.
   */
  owner?: string;
  /**
   * OperationWorkflowCaseAction.operation_id. Retained reporting operation evidence link.
   */
  operation_id?: string;
  /**
   * OperationWorkflowCaseAction.subscription_id. Source readiness inbox for a follow-up suggestion.
   */
  subscription_id?: string;
  /**
   * OperationWorkflowCaseAction.sequence. Retained source notification sequence.
   */
  sequence?: number;
};

