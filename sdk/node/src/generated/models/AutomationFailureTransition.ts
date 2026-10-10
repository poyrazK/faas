/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable bounded aggregate evidence for a failure guard transition.
 */
export type AutomationFailureTransition = {
  /**
   * Unique runtime transition generation for this automation.
   */
  generation: number;
  /**
   * Failure guard state resulting from this transition.
   */
  state: 'paused' | 'resumed';
  /**
   * Closed reason for the recorded transition.
   */
  reason: 'failure_threshold' | 'operator_resume';
  /**
   * Server timestamp of the pause or operator resume.
   */
  recorded_at: string;
  /**
   * Observed failures recorded with this transition.
   */
  failures: number;
  /**
   * Observed completed sample count recorded with this transition.
   */
  completed_runs: number;
  /**
   * Failure monitoring policy revision used for this transition.
   */
  policy_version: number;
  /**
   * Account that explicitly resumed; empty or omitted for an automatic pause.
   */
  actor_account_id?: string;
};

