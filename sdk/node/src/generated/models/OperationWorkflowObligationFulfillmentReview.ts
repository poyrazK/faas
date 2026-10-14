/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
/**
 * Bounded independent observation of the latest fulfilled case/cycle and its original or reaffirmed proof. Flags remain latched until explicit reaffirmation or reopening; normal workflow progress is not invalidation. Reasons describe retained coverage, not inferred business failure. Identical semantic observations suppress owner notices.
 */
export type OperationWorkflowObligationFulfillmentReview = {
  /**
   * Optional escalation team, distinct from the review owner. Requires an explicit review deadline and positive grace period.
   */
  escalation_owner?: string;
  /**
   * Grace period after actual overdue-reminder delivery. Positive values require an escalation owner; zero disables escalation.
   */
  escalate_after_seconds?: number;
  /**
   * Explicit review-cycle owner, preserved across worker observations without altering original work ownership.
   */
  manual_owner?: boolean;
  /**
   * Explicit review deadline, independent of the original source plan.
   */
  due_at?: string;
  action_kind: 'response_check_in' | 'coordinator_completion' | 'follow_up';
  binding: OperationWorkflowObligationTargetBinding;
  fulfilled_at: string;
  /**
   * Original fulfillment or latest explicit reaffirmation time; old observations cannot overwrite a newer basis.
   */
  basis_at: string;
  source_sequence: number;
  owner: string;
  review_required: boolean;
  /**
   * Latched reasons requiring explicit review, retained even after current evidence recovers. Possible reasons are supporting_report_unavailable, current_evidence_unavailable, contract_changed, resolution_invalidated and verification_unavailable.
   */
  reasons: Array<string>;
  /**
   * Current observed issues; empty does not automatically clear a latched review flag.
   */
  current_reasons: Array<string>;
  required_at?: string;
  checked_at: string;
  /**
   * Semantic comparison identity; timestamps and ordinary reporting revisions do not create new notices.
   */
  fingerprint: string;
  current_contract_version: number;
  current_state_revision: number;
  current_operation_id?: string;
  current_report_id?: string;
};

