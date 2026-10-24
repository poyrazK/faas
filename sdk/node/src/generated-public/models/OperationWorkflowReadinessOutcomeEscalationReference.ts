/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Reference to the single retained escalation notification in the same subscription. Pruning does not recreate escalations; links may expire.
 */
export type OperationWorkflowReadinessOutcomeEscalationReference = {
  /**
   * OperationWorkflowReadinessOutcomeEscalationReference.notification_id. Retained coordination escalation policy or evidence; business interpretation and execution remain application-owned.
   */
  notification_id: string;
  /**
   * OperationWorkflowReadinessOutcomeEscalationReference.sequence. Retained coordination escalation policy or evidence; business interpretation and execution remain application-owned.
   */
  sequence: number;
  /**
   * OperationWorkflowReadinessOutcomeEscalationReference.created_at. Retained coordination escalation policy or evidence; business interpretation and execution remain application-owned.
   */
  created_at: string;
};

