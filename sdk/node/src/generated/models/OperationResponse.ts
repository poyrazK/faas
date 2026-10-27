/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationDataRepair } from './OperationDataRepair.js';
import type { OperationDeliveryResponse } from './OperationDeliveryResponse.js';
import type { OperationLimits } from './OperationLimits.js';
import type { OperationProgress } from './OperationProgress.js';
import type { OperationResultArtifact } from './OperationResultArtifact.js';
import type { OperationSubject } from './OperationSubject.js';
import type { OperationUsage } from './OperationUsage.js';
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
  /**
   * Stable failure reason. deadline_exceeded and budget_exhausted mean the operation's own limits settled it.
   */
  failure_code?: string;
  waiting?: OperationWaitingState;
  data_repair?: OperationDataRepair;
  limits?: OperationLimits;
  /**
   * Absolute deadline fixed at acceptance; waits, retries and recovery count against it.
   */
  deadline_at?: string;
  usage?: OperationUsage;
  latest_sequence: number;
  created_at: string;
  updated_at: string;
  expires_at: string;
};

