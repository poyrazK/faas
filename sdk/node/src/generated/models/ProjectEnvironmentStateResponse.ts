/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentConfigResponse } from './ProjectEnvironmentConfigResponse.js';
import type { ProjectEnvironmentSharedResourceResponse } from './ProjectEnvironmentSharedResourceResponse.js';
import type { ProjectEnvironmentStateWorkloadResponse } from './ProjectEnvironmentStateWorkloadResponse.js';
import type { ProjectReleaseSetResponse } from './ProjectReleaseSetResponse.js';
/**
 * Point-in-time effective state snapshot for a project environment.
 */
export type ProjectEnvironmentStateResponse = {
  /**
   * Active graph, or null when no graph has been published. Its selected members may differ from the per-workload live deployments below.
   */
  active_release_set?: (ProjectReleaseSetResponse | null);
  release_set_status?: 'active' | 'none';
  project_slug: string;
  environment: string;
  protected: boolean;
  configuration: ProjectEnvironmentConfigResponse;
  workloads: Array<ProjectEnvironmentStateWorkloadResponse>;
  shared_resources: Array<ProjectEnvironmentSharedResourceResponse>;
  generated_at: string;
};

