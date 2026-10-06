/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentPromotionReleaseGraphResponse } from './ProjectEnvironmentPromotionReleaseGraphResponse.js';
import type { ProjectEnvironmentPromotionWorkloadResponse } from './ProjectEnvironmentPromotionWorkloadResponse.js';
import type { ProjectReleaseCheckResponse } from './ProjectReleaseCheckResponse.js';
/**
 * Result of a guarded project environment promotion.
 */
export type ProjectEnvironmentPromotionResponse = {
  bindings_required?: boolean;
  bindings_check?: ProjectReleaseCheckResponse;
  status?: 'running' | 'succeeded' | 'failed';
  promotion_id: string;
  project_slug: string;
  from_environment: string;
  to_environment: string;
  /**
   * Whether this promotion copied the source's non-secret configuration.
   */
  sync_config?: boolean;
  promotion_hash: string;
  release_graph?: ProjectEnvironmentPromotionReleaseGraphResponse;
  workloads: Array<ProjectEnvironmentPromotionWorkloadResponse>;
};

