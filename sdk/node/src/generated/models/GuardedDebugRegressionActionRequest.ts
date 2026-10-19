/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Mandatory immutable app/observation-version guarded triage action. Use the dedicated checked endpoint; older API nodes reject it.
 */
export type GuardedDebugRegressionActionRequest = {
  deployment_id: string;
  route: string;
  action: 'acknowledge' | 'dismiss' | 'resolve' | 'reopen';
  /**
   * Absolute checked-action dismissal expiry, frozen by MCP review and capped at 30 days.
   */
  dismissed_until?: string;
  expected_app_id: string;
  expected_version: string;
  reason: string;
};

