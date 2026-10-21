/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectDeploymentCheckWorkload } from './ProjectDeploymentCheckWorkload.js';
import type { ProjectReleaseConfigRequest } from './ProjectReleaseConfigRequest.js';
/**
 * Metadata-only project intent. Local source checks provide workloads; promotion checks provide from_environment instead. Proposed files never stage production changes.
 */
export type ProjectDeploymentCheckRequest = {
  environment?: string;
  from_environment?: string;
  expected_state_hash?: string;
  atomic?: boolean;
  add_services?: boolean;
  remove_services?: boolean;
  verify?: boolean;
  workers?: boolean;
  worker_health?: boolean;
  migrate?: boolean;
  progressive?: boolean;
  stabilize?: boolean;
  sync_config?: boolean;
  release_config?: ProjectReleaseConfigRequest;
  workloads?: Array<ProjectDeploymentCheckWorkload>;
  managed?: Array<string>;
};

