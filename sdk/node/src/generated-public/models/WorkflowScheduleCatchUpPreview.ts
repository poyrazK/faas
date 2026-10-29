/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowScheduleFirePreview } from './WorkflowScheduleFirePreview.js';
/**
 * Simulated next evaluator decision. It does not reserve quota or promise worker availability.
 */
export type WorkflowScheduleCatchUpPreview = {
  policy: 'skip' | 'latest';
  /**
   * Effective maximum age for latest catch-up.
   */
  window?: string;
  outcome: 'disabled' | 'first_evaluation_arms' | 'no_new_evaluation' | 'run_current_fire' | 'coalesce_latest' | 'skip_missed' | 'outside_catch_up_window' | 'no_due_occurrence';
  /**
   * Durable or simulated prior evaluator time.
   */
  since?: string;
  /**
   * Candidate occurrences within the latest policy window, or the single current occurrence for skip.
   */
  eligible_occurrences: number;
  /**
   * Older eligible fires represented by the selected latest occurrence.
   */
  coalesced_occurrences: number;
  /**
   * True when the skip policy drops earlier fires before the current occurrence.
   */
  missed_occurrences_not_recovered: boolean;
  /**
   * True when at least one unconsumed fire is older than the latest policy window.
   */
  missed_outside_window: boolean;
  selected?: WorkflowScheduleFirePreview;
};

