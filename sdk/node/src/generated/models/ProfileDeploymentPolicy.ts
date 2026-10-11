/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileDeploymentPolicyConfig } from './ProfileDeploymentPolicyConfig.js';
/**
 * App-owned CPU-check configuration. Revision zero represents an unsaved disabled policy; updated_at appears after saving.
 */
export type ProfileDeploymentPolicy = {
  app_id: string;
  revision: number;
  config: ProfileDeploymentPolicyConfig;
  updated_at?: string;
};

