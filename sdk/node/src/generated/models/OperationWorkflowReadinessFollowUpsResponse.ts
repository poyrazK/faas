/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessFollowUp } from './OperationWorkflowReadinessFollowUp.js';
/**
 * Live queue across this app/environment subscription inboxes. Owner/customer/workflow/due filters apply before pagination. No notification edits are performed by this read.
 */
export type OperationWorkflowReadinessFollowUpsResponse = {
  /**
   * OperationWorkflowReadinessFollowUpsResponse.items. Pending follow-ups ordered by due time, subscription UUID and sequence.
   */
  items: Array<OperationWorkflowReadinessFollowUp>;
  /**
   * OperationWorkflowReadinessFollowUpsResponse.evaluated_at. Shared clock for overdue classification.
   */
  evaluated_at: string;
  /**
   * OperationWorkflowReadinessFollowUpsResponse.total_count. Count or continuation offset within the selected retained follow-ups.
   */
  total_count: number;
  /**
   * OperationWorkflowReadinessFollowUpsResponse.overdue_count. Count or continuation offset within the selected retained follow-ups.
   */
  overdue_count: number;
  /**
   * OperationWorkflowReadinessFollowUpsResponse.next_offset. Count or continuation offset within the selected retained follow-ups.
   */
  next_offset: number;
  /**
   * OperationWorkflowReadinessFollowUpsResponse.has_more. More matching retained rows follow this live page.
   */
  has_more: boolean;
  /**
   * OperationWorkflowReadinessFollowUpsResponse.retention_limited. True: coordination only survives within each subscription latest-100 notification window; this is not an exhaustive task inventory.
   */
  retention_limited: boolean;
};

