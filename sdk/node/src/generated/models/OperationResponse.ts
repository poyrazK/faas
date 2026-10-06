/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationDeliveryResponse } from './OperationDeliveryResponse.js';
import type { OperationProgress } from './OperationProgress.js';
import type { OperationResultArtifact } from './OperationResultArtifact.js';
/**
 * Customer business work; execution recovery and delivery retain independent semantics.
 */
export type OperationResponse = {
  id: string;
  name: string;
  generation: number;
  state: 'accepted' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation';
  progress?: OperationProgress;
  /**
   * Pinned-schema-validated business result, when confirmed successful.
   */
  result?: any;
  artifacts?: Array<OperationResultArtifact>;
  completion_delivery: OperationDeliveryResponse;
  cancellation_requested: boolean;
  failure_code?: string;
  latest_sequence: number;
  created_at: string;
  updated_at: string;
  expires_at: string;
};

