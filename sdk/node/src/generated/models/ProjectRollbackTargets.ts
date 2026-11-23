/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectRollbackTargetDeploy } from './ProjectRollbackTargetDeploy.js';
/**
 * Recorded project deploy groups available for rollback preview.
 */
export type ProjectRollbackTargets = {
  project: string;
  project_id: string;
  deploys: Array<ProjectRollbackTargetDeploy>;
};

