/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectPromotionMigrationPolicy } from './ProjectPromotionMigrationPolicy.js';
import type { ProjectReleaseProbe } from './ProjectReleaseProbe.js';
import type { ProjectReleaseWorker } from './ProjectReleaseWorker.js';
/**
 * Destination probe and optional migration contract bound to the exact promotion preview and approval.
 */
export type ProjectPromotionHealthPolicy = {
  /**
   * Complete immutable worker set for multiple-worker promotions; mutually exclusive with worker.
   */
  workers?: Array<ProjectReleaseWorker>;
  worker?: ProjectReleaseWorker;
  migration?: ProjectPromotionMigrationPolicy;
  probes: Array<ProjectReleaseProbe>;
};

