/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable checkpoint for one workload in an environment promotion.
 */
export type ProjectEnvironmentPromotionStatusWorkloadResponse = {
  workload_slug: string;
  workload_name: string;
  status: 'pending' | 'promoted' | 'unchanged' | 'failed';
  source_deployment_id?: string;
  previous_target_deployment_id?: string;
  target_deployment_id?: string;
  error?: string;
  rollback_status?: 'pending' | 'restored' | 'cleared' | 'unchanged' | 'skipped' | 'failed';
  restored_target_deployment_id?: string;
  rollback_error?: string;
  verification_status?: 'pending' | 'verified' | 'failed';
  verification_error?: string;
};

