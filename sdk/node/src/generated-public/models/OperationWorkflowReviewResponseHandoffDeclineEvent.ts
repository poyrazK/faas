/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseHandoffDecline } from './OperationWorkflowReviewResponseHandoffDecline.js';
/**
 * One historical decline notification routed to the previous owner, without automatic reassignment or deadline changes. Consult current response ownership before acting.
 */
export type OperationWorkflowReviewResponseHandoffDeclineEvent = {
  /**
   * OperationWorkflowReviewResponseHandoffDeclineEvent.source_notification_id. Original response alert identity.
   */
  source_notification_id: string;
  /**
   * OperationWorkflowReviewResponseHandoffDeclineEvent.source_sequence. Original response sequence within this inbox.
   */
  source_sequence: number;
  /**
   * OperationWorkflowReviewResponseHandoffDeclineEvent.decline. Historical decline and its published notification marker.
   */
  decline: OperationWorkflowReviewResponseHandoffDecline;
};

