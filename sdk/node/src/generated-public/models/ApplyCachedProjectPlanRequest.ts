/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectInitialSecrets } from './ProjectInitialSecrets.js';
/**
 * Applies an account-owned reviewed project archive with selected workloads and optional initial sealed configuration.
 */
export type ApplyCachedProjectPlanRequest = {
  plan_token: string;
  /**
   * Selected workload names; unselected existing workloads remain unchanged.
   */
  only?: Array<string>;
  exclude?: Array<string>;
  approval_token?: string;
  initial_secrets?: ProjectInitialSecrets;
};

