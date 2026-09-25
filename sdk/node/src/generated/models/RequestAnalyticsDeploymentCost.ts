/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One deployment's estimated share of app raw compute value, allocated by its observed request share.
 */
export type RequestAnalyticsDeploymentCost = {
  /**
   * Immutable deployment UUID.
   */
  deployment_id: string;
  commit_sha?: string;
  deployment_tag?: string;
  deployment_created_at?: string;
  requests: number;
  request_share_pct: number;
  estimated_compute_cost_millicents: number;
};

