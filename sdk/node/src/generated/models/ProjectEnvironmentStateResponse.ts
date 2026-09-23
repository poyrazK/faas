/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentConfigResponse } from './ProjectEnvironmentConfigResponse.js';
import type { ProjectEnvironmentSharedResourceResponse } from './ProjectEnvironmentSharedResourceResponse.js';
import type { ProjectEnvironmentStateWorkloadResponse } from './ProjectEnvironmentStateWorkloadResponse.js';
/**
 * Point-in-time effective state snapshot for a project environment.
 */
export type ProjectEnvironmentStateResponse = {
  project_slug: string;
  environment: string;
  protected: boolean;
  configuration: ProjectEnvironmentConfigResponse;
  workloads: Array<ProjectEnvironmentStateWorkloadResponse>;
  shared_resources: Array<ProjectEnvironmentSharedResourceResponse>;
  generated_at: string;
};

