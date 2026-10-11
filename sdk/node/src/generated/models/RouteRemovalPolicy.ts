/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * App route retirement policy and its current observation baseline.
 */
export type RouteRemovalPolicy = {
  app_id: string;
  mode: 'report' | 'enforce';
  revision: number;
  /**
   * Go duration; default 720h.
   */
  grace_period: string;
  /**
   * Go duration; default 1h.
   */
  max_approval_age: string;
  baseline_deployment_id?: string;
  updated_at?: string;
};

