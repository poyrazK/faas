/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentPromotionReleaseGraphResponse } from './ProjectEnvironmentPromotionReleaseGraphResponse.js';
import type { ProjectEnvironmentPromotionWorkloadResponse } from './ProjectEnvironmentPromotionWorkloadResponse.js';
/**
 * Result of a guarded project environment promotion.
 */
export type ProjectEnvironmentPromotionResponse = {
  promotion_id: string;
  project_slug: string;
  from_environment: string;
  to_environment: string;
  promotion_hash: string;
  release_graph?: ProjectEnvironmentPromotionReleaseGraphResponse;
  workloads: Array<ProjectEnvironmentPromotionWorkloadResponse>;
};

