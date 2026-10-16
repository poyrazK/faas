/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateEnvironmentCachePurgeRequest } from './CreateEnvironmentCachePurgeRequest.js';
import type { EnvironmentCachePolicy } from './EnvironmentCachePolicy.js';
import type { EnvironmentCachePurgeReceipt } from './EnvironmentCachePurgeReceipt.js';
import type { SetEnvironmentCachePolicyRequest } from './SetEnvironmentCachePolicyRequest.js';
/**
 * Accepted cache delegation with execution state and optional exact action receipt, distinct from gateway convergence.
 */
export type EnvironmentCacheSchedule = {
  schedule_id: string;
  app_id: string;
  project_id: string;
  environment_id: string;
  execute_at: string;
  policy?: SetEnvironmentCachePolicyRequest;
  purge?: CreateEnvironmentCachePurgeRequest;
  scope: string;
  status: 'scheduled' | 'executing' | 'executed' | 'failed' | 'cancelled';
  created_at: string;
  finished_at?: string;
  error_code?: string;
  policy_result?: EnvironmentCachePolicy;
  purge_result?: EnvironmentCachePurgeReceipt;
  /**
   * Original schedule cancelled atomically when this successor was accepted.
   */
  replaces_schedule_id?: string;
  /**
   * Successor UUID replacing this cancelled original; cancellation of this original does not withdraw that successor.
   */
  replaced_by_schedule_id?: string;
};

