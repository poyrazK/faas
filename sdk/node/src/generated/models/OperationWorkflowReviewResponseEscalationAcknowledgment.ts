/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable human escalation acknowledgment with a coordinator commitment, recorded actor and time. Notification acknowledgment alone does not create this record. Only the system overdue delivery marker may change.
 */
export type OperationWorkflowReviewResponseEscalationAcknowledgment = {
  coordinator: string;
  plan: string;
  due_at: string;
  at: string;
  actor: string;
  /**
   * One-time follow-up publication marker. Stored atomically with its inbox notice and mirrored in retained history; human update timestamps remain unchanged.
   */
  overdue_notified_at?: string;
};

