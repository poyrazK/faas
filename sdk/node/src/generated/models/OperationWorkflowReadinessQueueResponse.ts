/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessQueueEntry } from './OperationWorkflowReadinessQueueEntry.js';
import type { OperationWorkflowReadinessSnapshot } from './OperationWorkflowReadinessSnapshot.js';
/**
 * Bounded live readiness queue; counts describe the evaluated sample and filters.
 */
export type OperationWorkflowReadinessQueueResponse = {
  snapshot: OperationWorkflowReadinessSnapshot;
  /**
   * Matching transitions with unmet rows first.
   */
  items: Array<OperationWorkflowReadinessQueueEntry>;
  /**
   * Time used to select recently updated ongoing workflows.
   */
  sampled_at: string;
  /**
   * Time evaluation completed; reads are live rather than atomic.
   */
  evaluated_at: string;
  /**
   * Retained ongoing workflows matching app customer and workflow selectors before sampling.
   */
  matching_workflow_count: number;
  /**
   * Ongoing workflows selected up to the workflow cap.
   */
  sampled_workflow_count: number;
  /**
   * Sampled workflows excluded after their current report or contract changed.
   */
  excluded_changed_workflow_count: number;
  /**
   * Unchanged sampled workflows without a matching current-state edge.
   */
  no_transition_workflow_count: number;
  /**
   * Matching current-state edges before per-workflow edge sampling.
   */
  declared_transition_count: number;
  /**
   * Current-state edges evaluated before owner and requirement filtering.
   */
  evaluated_transition_count: number;
  /**
   * Evaluated transitions matching filters before the row limit.
   */
  matching_transition_count: number;
  /**
   * Matching evaluated transitions with all declared requirements met.
   */
  ready_transition_count: number;
  /**
   * Matching evaluated transitions with unmet declared requirements.
   */
  unmet_transition_count: number;
  /**
   * More matching ongoing workflows exist than were sampled.
   */
  workflow_sample_truncated: boolean;
  /**
   * At least one workflow has more matching edges than the per-workflow cap.
   */
  transition_sample_truncated: boolean;
  /**
   * More matching evaluated rows exist than the response row limit.
   */
  has_more: boolean;
};

