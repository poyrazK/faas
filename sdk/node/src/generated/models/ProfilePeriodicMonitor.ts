/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileDeploymentPolicyConfig } from './ProfileDeploymentPolicyConfig.js';
import type { ProfilePeriodicObservation } from './ProfilePeriodicObservation.js';
import type { ProfileQuery } from './ProfileQuery.js';
export type ProfilePeriodicMonitor = {
  /**
   * Whether this monitor still matches the running deployment, current policy and eligible account.
   */
  active: boolean;
  id: string;
  app_id: string;
  deployment_id: string;
  scope: string;
  route: string;
  policy_revision: number;
  config: ProfileDeploymentPolicyConfig;
  baseline?: ProfileQuery;
  candidate: ProfileQuery;
  attempts: number;
  next_attempt_at: string;
  history: Array<ProfilePeriodicObservation>;
};

