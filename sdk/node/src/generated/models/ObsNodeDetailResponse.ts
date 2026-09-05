/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsInstanceRow } from './ObsInstanceRow.js';
import type { ObsNodeApp } from './ObsNodeApp.js';
import type { ObsNodeDrainStatus } from './ObsNodeDrainStatus.js';
import type { ObsNodeRow } from './ObsNodeRow.js';
/**
 * One node - health, placed workloads, and drain safety.
 */
export type ObsNodeDetailResponse = {
  node: ObsNodeRow;
  apps: Array<ObsNodeApp>;
  instances: Array<ObsInstanceRow>;
  drain: ObsNodeDrainStatus;
};

