/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RequestAnalyticsDeploymentCost } from './RequestAnalyticsDeploymentCost.js';
/**
 * Bounded deployment allocation of the app's estimated raw compute value for the analytics window. It is not an invoice amount.
 */
export type RequestAnalyticsDeploymentCostBreakdown = {
  estimated_millicents: number;
  allocated_millicents: number;
  unallocated_millicents: number;
  /**
   * Value allocated to deployments outside the top-N list.
   */
  other_millicents: number;
  other_requests: number;
  other_request_share_pct: number;
  request_count: number;
  deployments: Array<RequestAnalyticsDeploymentCost>;
};

