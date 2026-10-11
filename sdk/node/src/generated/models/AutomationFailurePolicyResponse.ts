/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationFailurePolicy } from './AutomationFailurePolicy.js';
import type { AutomationFailureTransition } from './AutomationFailureTransition.js';
/**
 * Failure guard state, monitoring configuration and advisory retained-work preview.
 */
export type AutomationFailurePolicyResponse = {
  policy: AutomationFailurePolicy;
  /**
   * Whether automatic admissions are blocked by the durable failure guard.
   */
  paused: boolean;
  /**
   * Current guard generation; resume requires this value.
   */
  generation: number;
  /**
   * Start of the fresh monitoring epoch; absent before configuration.
   */
  monitoring_since?: string;
  /**
   * Time the current failure pause was latched; absent while unpaused.
   */
  paused_at?: string;
  /**
   * Failed and dead runs completed in the current bounded observation window.
   */
  observed_failures: number;
  /**
   * Succeeded, failed and dead runs completed in the current observation window.
   */
  observed_completed_runs: number;
  /**
   * Existing pending runs that continue despite a failure admission pause.
   */
  pending_runs: number;
  /**
   * Already running workflows unaffected by admission pausing.
   */
  running_runs: number;
  /**
   * Existing workflows parked on timers or external events.
   */
  waiting_runs: number;
  /**
   * Advisory count of unadmitted retained event recipients for this automation.
   */
  retained_events: number;
  /**
   * Latest pause and resume transitions in descending generation order.
   */
  history: Array<AutomationFailureTransition>;
};

