/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryHealthAlertPolicy } from './WorkflowRecoveryHealthAlertPolicy.js';
/**
 * Explicit app recovery policy. Preview prepares parent-job plans and worker-discovered initial-failure reviews. initial_failure requires explicit failure statuses. Opt-in automatic_retry requires saved_failure only; automatic_initial requires initial_failure only. Both require explicit workflow and failure-status filters and execution limits. Resume count caps the absolute generation; preparation and execution budgets are separate. Cancellation remains manual.
 */
export type WorkflowRecoveryPolicy = {
  health_alerts?: WorkflowRecoveryHealthAlertPolicy;
  /**
   * Required 1..100 for either automatic mode; zero or omitted in preview. Counts automatic job creation across policy versions and automatic modes
   */
  hourly_execution_budget?: number;
  /**
   * Required 1..10 for either automatic mode; zero or omitted in preview. Consecutive proven failures pause automatic execution.
   */
  pause_after_failures?: number;
  enabled: boolean;
  mode: 'preview' | 'automatic_retry' | 'automatic_initial';
  workflow_names: Array<string>;
  triggers: Array<'saved_failure' | 'stalled' | 'deadline_exceeded' | 'initial_failure'>;
  failure_statuses: Array<'failed' | 'dead'>;
  max_resume_count: number;
  cooldown_seconds: number;
  hourly_action_budget: number;
  rate_per_second: number;
  reason: string;
};

