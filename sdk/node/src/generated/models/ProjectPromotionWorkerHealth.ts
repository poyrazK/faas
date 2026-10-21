/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseWorker } from './ProjectReleaseWorker.js';
import type { ProjectWorkerHealth } from './ProjectWorkerHealth.js';
/**
 * Captured queue worker policy paired with observed delivery health for one promotion member.
 */
export type ProjectPromotionWorkerHealth = {
  worker: ProjectReleaseWorker;
  health: ProjectWorkerHealth;
};

