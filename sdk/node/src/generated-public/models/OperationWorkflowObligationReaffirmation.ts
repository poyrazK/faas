/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
import type { OperationWorkflowStateHistoryEntry } from './OperationWorkflowStateHistoryEntry.js';
/**
 * Immutable human review receipt with a fresh retained application state report. Original fulfillment and evidence remain intact; subsequent monitoring uses this proof as its basis. Does not authorize application execution or establish current business success. Each retained exact-case lookup scans at most 1000 reports.
 */
export type OperationWorkflowObligationReaffirmation = {
  action_kind: 'response_check_in' | 'coordinator_completion' | 'follow_up';
  binding: OperationWorkflowObligationTargetBinding;
  fulfilled_at: string;
  review_fingerprint: string;
  reason: string;
  source_sequence: number;
  owner: string;
  evidence: OperationWorkflowStateHistoryEntry;
  evidence_checked_at: string;
  reaffirmed_at: string;
  reaffirmed_by: string;
};

