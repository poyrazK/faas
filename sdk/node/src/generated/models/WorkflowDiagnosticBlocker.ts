/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A stable recovery reason with a fixed explanation and optional affected step.
 */
export type WorkflowDiagnosticBlocker = {
  code: 'run_not_failed' | 'resume_limit_reached' | 'cancelled' | 'invalid_definition' | 'incomplete_step_state' | 'active_step' | 'handler_executed' | 'failed_control_step' | 'failure_before_dispatch' | 'unsafe_mutation' | 'no_failed_actions' | 'active_attempt' | 'account_inactive' | 'plan_not_allowed' | 'app_deleted' | 'maintenance' | 'tenant_required' | 'tenant_unavailable' | 'deployment_unavailable' | 'pinned_deployment_unavailable' | 'integration_unavailable' | 'active_run_quota' | 'runtime_disabled';
  /**
   * Fixed explanation without private values.
   */
  message: string;
  /**
   * Affected step when the planner blocker is step-specific.
   */
  step_name?: string;
};

