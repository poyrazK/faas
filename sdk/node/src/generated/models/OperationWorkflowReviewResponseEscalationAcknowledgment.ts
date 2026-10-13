/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseEscalationCompletion } from './OperationWorkflowReviewResponseEscalationCompletion.js';
/**
 * Immutable human escalation acknowledgment with a coordinator commitment, recorded actor and time. Notification acknowledgment alone does not create this record. The system overdue marker and explicit human completion may be appended without changing commitment fields.
 */
export type OperationWorkflowReviewResponseEscalationAcknowledgment = {
  /**
   * OperationWorkflowReviewResponseEscalationAcknowledgment.completion. Explicit completion of coordinator work, separate from handoff acceptance and business outcomes.
   */
  completion?: OperationWorkflowReviewResponseEscalationCompletion;
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

