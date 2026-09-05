/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAppDetail } from './ObsAppDetail.js';
import type { ObsAppHealth } from './ObsAppHealth.js';
import type { ObsDeploymentRow } from './ObsDeploymentRow.js';
import type { ObsInstanceRow } from './ObsInstanceRow.js';
import type { ObsInvocationRow } from './ObsInvocationRow.js';
/**
 * Operator app-detail envelope - the app row plus health, deployments, and instances.
 */
export type ObsAppDetailResponse = {
  app: ObsAppDetail;
  deployments: Array<ObsDeploymentRow>;
  instances: Array<ObsInstanceRow>;
  invocations: Array<ObsInvocationRow>;
  health: ObsAppHealth;
};

