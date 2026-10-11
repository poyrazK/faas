/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationDeliverySummary } from './OperationDeliverySummary.js';
import type { OperationProgress } from './OperationProgress.js';
/**
 * Bounded discovery metadata; omits result bytes, artifact locations and execution authority.
 */
export type OperationSummary = {
  /**
   * Present only on account operator listings; absent from tenant-self summaries.
   */
  platform_tenant_id?: string;
  id: string;
  name: string;
  generation: number;
  state: 'accepted' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation';
  progress?: OperationProgress;
  completion_delivery: OperationDeliverySummary;
  cancellation_requested: boolean;
  latest_sequence: number;
  created_at: string;
  updated_at: string;
  expires_at: string;
};

