/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ResourceProfile } from './ResourceProfile.js';
/**
 * Resolved immutable compute shape persisted for this deployment revision.
 */
export type DeploymentResources = {
  /**
   * Revision memory in MiB.
   */
  ram_mb: number;
  /**
   * Revision sustained CPU.
   */
  cpu_millicores: 250 | 500 | 1000;
  resource_profile?: ResourceProfile;
};

