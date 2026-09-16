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
  rollback_status?: 'rolling_back' | 'rolled_back' | 'rollback_failed';
  rollback_error?: string;
  rollback_started_at?: string;
  rollback_completed_at?: string;
  verification_status?: 'pending' | 'verifying' | 'verified' | 'failed';
  verification_error?: string;
  verification_started_at?: string;
  verification_completed_at?: string;
  workloads: Array<ProjectEnvironmentPromotionStatusWorkloadResponse>;
};

