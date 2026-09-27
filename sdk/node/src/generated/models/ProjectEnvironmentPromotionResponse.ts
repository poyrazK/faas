/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentPromotionWorkloadResponse } from './ProjectEnvironmentPromotionWorkloadResponse.js';
/**
 * Result of a guarded project environment promotion.
 */
export type ProjectEnvironmentPromotionResponse = {
  promotion_id: string;
  project_slug: string;
  from_environment: string;
  to_environment: string;
  /**
   * Whether this promotion copied the source's non-secret configuration.
   */
  sync_config?: boolean;
  promotion_hash: string;
  workloads: Array<ProjectEnvironmentPromotionWorkloadResponse>;
};

