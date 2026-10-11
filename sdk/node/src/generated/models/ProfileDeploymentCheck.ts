/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileDeploymentPolicyConfig } from './ProfileDeploymentPolicyConfig.js';
import type { ProfileQuery } from './ProfileQuery.js';
/**
 * Durable automatic CPU-check receipt. Only bounded metadata is retained; raw samples remain under backend retention. Retry and lease recovery keep the original deployment pair and windows. Missing predecessors yield an inconclusive receipt without an investigation. If the saved-investigation quota is full, the result remains recorded with its original comparison link and an explicit explanation.
 */
export type ProfileDeploymentCheck = {
  deployment_id: string;
  app_id: string;
  scope: string;
  policy_revision: number;
  config: ProfileDeploymentPolicyConfig;
  baseline?: ProfileQuery;
  candidate: ProfileQuery;
  status: 'queued' | 'running' | 'regressed' | 'no_regression_detected' | 'inconclusive' | 'cancelled';
  reason: string;
  attempts: number;
  next_attempt_at?: string;
  completed_at?: string;
  investigation_id?: string;
  /**
   * Relative authenticated dashboard link to saved metadata or the original exact comparison windows.
   */
  comparison_url?: string;
  created_at: string;
};

