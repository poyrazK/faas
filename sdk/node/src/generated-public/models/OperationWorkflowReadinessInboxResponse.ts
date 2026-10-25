/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessNotification } from './OperationWorkflowReadinessNotification.js';
import type { OperationWorkflowReadinessSubscription } from './OperationWorkflowReadinessSubscription.js';
/**
 * Retained notification page with explicit cursor gaps; unchanged evaluations do not create entries.
 */
export type OperationWorkflowReadinessInboxResponse = {
  subscription: OperationWorkflowReadinessSubscription;
  /**
   * OperationWorkflowReadinessInboxResponse.items. Notifications after the supplied sequence in ascending order.
   */
  items: Array<OperationWorkflowReadinessNotification>;
  /**
   * OperationWorkflowReadinessInboxResponse.latest_sequence. Most recent committed subscription sequence.
   */
  latest_sequence: number;
  /**
   * OperationWorkflowReadinessInboxResponse.next_sequence. Cursor for the next page.
   */
  next_sequence: number;
  /**
   * OperationWorkflowReadinessInboxResponse.resync_required. The supplied cursor is outside retained history; the page starts at retained entries without claiming completeness.
   */
  resync_required: boolean;
  /**
   * OperationWorkflowReadinessInboxResponse.has_more. More committed entries follow this page.
   */
  has_more: boolean;
};

