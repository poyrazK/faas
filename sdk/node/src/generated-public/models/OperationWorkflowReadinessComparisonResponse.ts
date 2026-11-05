/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessChange } from './OperationWorkflowReadinessChange.js';
import type { OperationWorkflowReadinessSnapshot } from './OperationWorkflowReadinessSnapshot.js';
/**
 * Live comparison counts before filtering with bounded matching changes and the next available-edge snapshot.
 */
export type OperationWorkflowReadinessComparisonResponse = {
  /**
   * ReadinessComparisonResponse. Saved snapshot capture time.
   */
  baseline_at: string;
  /**
   * ReadinessComparisonResponse. Time of the shared per-workflow evidence evaluation or comparison completion.
   */
  evaluated_at: string;
  /**
   * ReadinessComparisonResponse. All transitions in the supplied snapshot before filters.
   */
  baseline_transition_count: number;
  /**
   * ReadinessComparisonResponse. Transitions with comparable current edges and declared requirements.
   */
  compared_transition_count: number;
  /**
   * ReadinessComparisonResponse. Comparable transitions that changed from unmet to ready.
   */
  newly_ready_count: number;
  /**
   * ReadinessComparisonResponse. Comparable transitions that changed from ready to unmet.
   */
  newly_blocked_count: number;
  /**
   * ReadinessComparisonResponse. Comparable transitions with evidence or reason changes and unchanged readiness result.
   */
  changed_count: number;
  /**
   * ReadinessComparisonResponse. Comparable transitions with unchanged result and evidence summaries.
   */
  unchanged_count: number;
  /**
   * ReadinessComparisonResponse. Transitions excluded from readiness comparison due to contract state edge or retained-revision boundaries.
   */
  unavailable_count: number;
  /**
   * ReadinessComparisonResponse. Changes matching the selected filter before row limit.
   */
  matching_change_count: number;
  /**
   * ReadinessComparisonResponse. Matching transition comparison rows.
   */
  items: Array<OperationWorkflowReadinessChange>;
  snapshot: OperationWorkflowReadinessSnapshot;
  /**
   * ReadinessComparisonResponse. More matching changes exist than the returned row limit.
   */
  has_more: boolean;
};

