/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowBlockerDuration } from './OperationWorkflowBlockerDuration.js';
import type { OperationWorkflowStateDuration } from './OperationWorkflowStateDuration.js';
import type { OperationWorkflowVerificationDuration } from './OperationWorkflowVerificationDuration.js';
/**
 * Observed durations for one retained workflow instance. State/blocker intervals use application occurrence time. Verification waits use platform publication time. Gaps are excluded and incomplete histories are marked. Groups are sorted by observed duration and bounded independently of exact window totals.
 */
export type OperationWorkflowBottlenecks = {
  evaluated_at: string;
  /**
   * True only when the retained window starts at revision 1 and reaches the current report without gaps or invalid interval boundaries.
   */
  history_complete: boolean;
  incomplete_reasons: Array<'missing_start' | 'missing_latest' | 'revision_gap' | 'duplicate_revision' | 'out_of_order_time' | 'future_observation' | 'contract_changed' | 'state_discontinuity' | 'history_window_truncated' | 'verification_start_missing'>;
  observed_from?: string;
  observed_through?: string;
  reports_in_window: number;
  history_truncated: boolean;
  /**
   * Current workflow state is nonterminal. Ongoing duration is evaluated as of evaluated_at.
   */
  ongoing: boolean;
  state_seconds: number;
  /**
   * Union of measured intervals with at least one active blocker. Overlapping blocker groups can sum to more than this total.
   */
  blocked_seconds: number;
  verification_unknown_start_count: number;
  /**
   * Sum of whole observed wait seconds across obligations with known publication starts in the history window. Overlapping waits are counted separately.
   */
  verification_wait_seconds: number;
  states: Array<OperationWorkflowStateDuration>;
  blockers: Array<OperationWorkflowBlockerDuration>;
  verification_owners: Array<OperationWorkflowVerificationDuration>;
  states_truncated: boolean;
  blockers_truncated: boolean;
  verification_owners_truncated: boolean;
};

