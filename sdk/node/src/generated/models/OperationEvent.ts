/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Ordered retained event; execution identity may change under the same logical operation.
 */
export type OperationEvent = {
  operation_id: string;
  sequence: number;
  type: 'accepted' | 'running' | 'progress' | 'result_prepared' | 'artifact_prepared' | 'artifact_attached' | 'succeeded' | 'failed' | 'cancelled' | 'reconciliation_required' | 'recovery_requested' | 'cancellation_requested' | 'delivery_changed';
  execution_id?: string;
  attempt?: number;
  /**
   * Event-specific customer projection.
   */
  data: any;
  created_at: string;
};

