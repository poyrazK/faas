/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
/**
 * OperationWorkflowReviewResponseLink. Immutable source identity captured from an authorized matching review delivery; preserved after inbox retention loss.
 */
export type OperationWorkflowReviewResponseLink = {
  /**
   * OperationWorkflowReviewResponseLink.subscription_id. Retained review subscription identity.
   */
  subscription_id: string;
  /**
   * OperationWorkflowReviewResponseLink.sequence. Original delivery sequence.
   */
  sequence: number;
  /**
   * OperationWorkflowReviewResponseLink.notification_id. Original delivery identity, captured by the server.
   */
  notification_id: string;
  /**
   * OperationWorkflowReviewResponseLink.progress_id. Pinned source playbook progress identity.
   */
  progress_id: string;
  /**
   * OperationWorkflowReviewResponseLink.outcome_code. Pinned follow-up outcome list.
   */
  outcome_code: string;
  /**
   * OperationWorkflowReviewResponseLink.item_index. Pinned local step index.
   */
  item_index: number;
  /**
   * OperationWorkflowReviewResponseLink.dependency_review_revision. Original review obligation revision.
   */
  dependency_review_revision: number;
  /**
   * OperationWorkflowReviewResponseLink.platform_tenant_id. Account-only exact source customer identity; customer responses omit this field.
   */
  platform_tenant_id?: string;
  /**
   * OperationWorkflowReviewResponseLink.subject. Scoped case subject captured from source delivery.
   */
  subject: OperationSubject;
  /**
   * OperationWorkflowReviewResponseLink.workflow. Protected workflow identity matching the parent risk alert.
   */
  workflow: string;
  /**
   * OperationWorkflowReviewResponseLink.instance_id. Scoped source workflow instance.
   */
  instance_id: string;
};

