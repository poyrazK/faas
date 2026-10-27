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
   * Hold or abort policy captured with this rollout evaluation so retained decisions can be interpreted independently of later configuration updates.
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

