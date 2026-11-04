/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained server-recorded ownership change for an escalation response target. History is bounded and may be truncated; no business authority is granted.
 */
export type OperationWorkflowReadinessAssignmentChange = {
  /**
   * OperationWorkflowReadinessAssignmentChange.from_owner. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  from_owner: string;
  /**
   * OperationWorkflowReadinessAssignmentChange.to_owner. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  to_owner: string;
  /**
   * OperationWorkflowReadinessAssignmentChange.at. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  at: string;
  /**
   * OperationWorkflowReadinessAssignmentChange.actor. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  actor: string;
  /**
   * OperationWorkflowReadinessAssignmentChange.reason. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  reason: 'manual_assignment' | 'response_deadline_fallback' | 'step_assignment';
};

