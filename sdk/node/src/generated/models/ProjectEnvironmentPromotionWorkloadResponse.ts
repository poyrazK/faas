/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Result for one workload in an environment promotion.
 */
export type ProjectEnvironmentPromotionWorkloadResponse = {
  workload_slug: string;
  workload_name: string;
  status: 'promoted' | 'unchanged';
  source_deployment_id?: string;
  target_deployment_id?: string;
};

