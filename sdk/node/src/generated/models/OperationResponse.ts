/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationDeliveryResponse } from './OperationDeliveryResponse.js';
import type { OperationProgress } from './OperationProgress.js';
import type { OperationResultArtifact } from './OperationResultArtifact.js';
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWaitingState } from './OperationWaitingState.js';
/**
 * Customer business work; execution recovery and delivery retain independent semantics.
 */
export type OperationResponse = {
  subject?: OperationSubject;
  id: string;
  name: string;
  generation: number;
  state: 'accepted' | 'running' | 'waiting' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation';
  progress?: OperationProgress;
  /**
   * Pinned-schema-validated business result, when confirmed successful.
   */
  result?: any;
  artifacts?: Array<OperationResultArtifact>;
  completion_delivery: OperationDeliveryResponse;
  cancellation_requested: boolean;
  failure_code?: string;
  waiting?: OperationWaitingState;
  latest_sequence: number;
  created_at: string;
  updated_at: string;
  expires_at: string;
};

