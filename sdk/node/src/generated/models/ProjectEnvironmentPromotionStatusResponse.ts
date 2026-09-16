/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentPromotionStatusWorkloadResponse } from './ProjectEnvironmentPromotionStatusWorkloadResponse.js';
/**
 * Durable status for a project environment promotion operation.
 */
export type ProjectEnvironmentPromotionStatusResponse = {
  promotion_id: string;
  project_slug: string;
  from_environment: string;
  to_environment: string;
  promotion_hash: string;
  status: 'running' | 'succeeded' | 'failed';
  error?: string;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  workloads: Array<ProjectEnvironmentPromotionStatusWorkloadResponse>;
};

