/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessAssignmentChange } from './OperationWorkflowReadinessAssignmentChange.js';
/**
 * Optional acknowledgment response target on a retained assigned escalation. Human acknowledgment fulfills it; completion or follow-up clearing stops it. A missed target emits one notice and optional guarded fallback. Assignment history retains the latest 16 changes.
 */
export type OperationWorkflowReadinessEscalationResponse = {
  /**
   * OperationWorkflowReadinessEscalationResponse.deadline_at. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  deadline_at: string;
  /**
   * OperationWorkflowReadinessEscalationResponse.initial_owner. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  initial_owner: string;
  /**
   * OperationWorkflowReadinessEscalationResponse.fallback_owner. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  fallback_owner?: string;
  /**
   * OperationWorkflowReadinessEscalationResponse.status. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  status: 'awaiting' | 'acknowledged' | 'closed' | 'deadline_missed';
  /**
   * OperationWorkflowReadinessEscalationResponse.first_acknowledged_at. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  first_acknowledged_at?: string;
  /**
   * OperationWorkflowReadinessEscalationResponse.deadline_notified_at. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  deadline_notified_at?: string;
  /**
   * OperationWorkflowReadinessEscalationResponse.history. Latest retained manual or automatic assignment changes.
   */
  history: Array<OperationWorkflowReadinessAssignmentChange>;
  /**
   * OperationWorkflowReadinessEscalationResponse.history_truncated. Retained acknowledgment target or assignment observation; never modifies application business reports.
   */
  history_truncated: boolean;
};

