/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentCronConfig } from './DeploymentCronConfig.js';
/**
 * The owned app and current HTTP scheduling set captured before staging.
 */
export type DeploymentCronStagingSnapshot = {
  app_id: string;
  revision: number;
  latest_deployment_id: string;
  crons: Array<DeploymentCronConfig>;
};

