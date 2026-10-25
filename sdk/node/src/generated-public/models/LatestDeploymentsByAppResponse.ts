/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentResponse } from './DeploymentResponse.js';
/**
 * At most one newest deployment for each non-deleted app owned by the authenticated account.
 */
export type LatestDeploymentsByAppResponse = {
  items: Array<DeploymentResponse>;
};

