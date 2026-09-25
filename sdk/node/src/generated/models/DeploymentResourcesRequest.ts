/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ResourceProfile } from './ResourceProfile.js';
/**
 * Optional compute override for one immutable deployment revision. Omitted dimensions inherit the app's configured defaults; the resolved values are checked against the app's plan. A named profile cannot be combined with conflicting explicit dimensions.
 */
export type DeploymentResourcesRequest = {
  /**
   * Revision memory in MiB; omitted inherits the app default.
   */
  ram_mb?: number;
  /**
   * Revision sustained CPU; omitted inherits the app default.
   */
  cpu_millicores?: 250 | 500 | 1000;
  resource_profile?: ResourceProfile;
};

