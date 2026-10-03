/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentConfigDiffResponse } from './ProjectEnvironmentConfigDiffResponse.js';
import type { ProjectEnvironmentSharedResourceResponse } from './ProjectEnvironmentSharedResourceResponse.js';
import type { ProjectEnvironmentWorkloadDiffResponse } from './ProjectEnvironmentWorkloadDiffResponse.js';
/**
 * Effective-state comparison from one project environment to another.
 */
export type ProjectEnvironmentDiffResponse = {
  project_slug: string;
  from_environment: string;
  to_environment: string;
  configuration: ProjectEnvironmentConfigDiffResponse;
  workloads: Array<ProjectEnvironmentWorkloadDiffResponse>;
  shared_resources: Array<ProjectEnvironmentSharedResourceResponse>;
  generated_at: string;
};

