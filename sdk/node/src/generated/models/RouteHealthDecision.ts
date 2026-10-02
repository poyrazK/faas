/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata-only decision evaluated inside the canary traffic transaction; history_id correlates the exact saved evidence with the advance response and traffic audit.
 */
export type RouteHealthDecision = {
  mode: 'report' | 'enforce';
  /**
   * Defaults to hold, including when omitted in a replacement update. Abort opts into automatic recovery for confirmed route 5xx regressions during an enforced canary; report mode is observational.
   */
  on_regression?: 'hold' | 'abort';
  revision: number;
  deployment_id: string;
  stable_deployment_id: string;
  /**
   * Retained saved decision UUID when selected routes were evaluated.
   */
  history_id?: string;
  status: 'allowed' | 'blocked' | 'report_only' | 'aborted';
  reason: string;
  checked_at: string;
};

