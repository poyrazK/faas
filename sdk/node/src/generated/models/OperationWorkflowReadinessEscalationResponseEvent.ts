/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessSnapshotEntry } from './OperationWorkflowReadinessSnapshotEntry.js';
/**
 * Single durable acknowledgment-deadline notice for an assigned escalation, with exact scoped source identity. Does not automatically schedule a second follow-up or indicate business failure.
 */
export type OperationWorkflowReadinessEscalationResponseEvent = {
  /**
   * OperationWorkflowReadinessEscalationResponseEvent.escalation_notification_id. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  escalation_notification_id: string;
  /**
   * OperationWorkflowReadinessEscalationResponseEvent.escalation_sequence. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  escalation_sequence: number;
  source: OperationWorkflowReadinessSnapshotEntry;
  /**
   * OperationWorkflowReadinessEscalationResponseEvent.checked_at. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  checked_at: string;
  /**
   * OperationWorkflowReadinessEscalationResponseEvent.deadline_at. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  deadline_at: string;
  /**
   * OperationWorkflowReadinessEscalationResponseEvent.owner. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  owner: string;
  /**
   * OperationWorkflowReadinessEscalationResponseEvent.reassigned. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  reassigned: boolean;
};

