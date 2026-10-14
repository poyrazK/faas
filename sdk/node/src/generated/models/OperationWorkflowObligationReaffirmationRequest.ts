/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
/**
 * Explicitly reaffirm a flagged latest fulfillment without reopening work. Preserve source status, owner, note and deadline and do not combine with other actions. Source revision, binding, fulfillment time and review fingerprint fence stale decisions. Evidence must be a full application state report for the exact case under the fresh current contract and be published after review was required; current retained evidence is rechecked independently before commit.
 */
export type OperationWorkflowObligationReaffirmationRequest = {
  binding: OperationWorkflowObligationTargetBinding;
  fulfilled_at: string;
  review_fingerprint: string;
  reason: string;
  evidence_operation_id: string;
  evidence_report_id: string;
};

