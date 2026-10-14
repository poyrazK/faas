/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
import type { OperationWorkflowStateHistoryEntry } from './OperationWorkflowStateHistoryEntry.js';
/**
 * Immutable human fulfillment receipt for one exact case and obligation cycle, linked to a retained application transition report. Does not close a shared source plan or assert business success or execution authority. Retained transition lookup scans at most 1000 exact-case reports.
 */
export type OperationWorkflowObligationFulfillment = {
  binding: OperationWorkflowObligationTargetBinding;
  reason: string;
  action_kind: 'response_check_in' | 'coordinator_completion' | 'follow_up';
  owner: string;
  evidence: OperationWorkflowStateHistoryEntry;
  evidence_checked_at: string;
  completed_at: string;
  completed_by: string;
};

