/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileDeploymentPolicyConfig } from './ProfileDeploymentPolicyConfig.js';
/**
 * Full replacement of the CPU rollout-check settings. Changing or disabling settings cancels queued and leased work for earlier policy revisions.
 */
export type SaveProfileDeploymentPolicyRequest = {
  expected_revision: number;
  config: ProfileDeploymentPolicyConfig;
};

