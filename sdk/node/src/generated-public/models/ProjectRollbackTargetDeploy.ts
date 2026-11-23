/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectRollbackProtection } from './ProjectRollbackProtection.js';
export type ProjectRollbackTargetDeploy = {
  deploy_group_id: string;
  /**
   * Creation time of the newest deployment in the group.
   */
  created_at: string;
  service_count: number;
  protection?: ProjectRollbackProtection;
};

