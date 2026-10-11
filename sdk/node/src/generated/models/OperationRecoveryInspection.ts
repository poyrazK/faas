/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationRecoveryArtifact } from './OperationRecoveryArtifact.js';
import type { OperationRecoveryStep } from './OperationRecoveryStep.js';
/**
 * Account-only durable recovery evidence with execution secrets and payloads omitted.
 */
export type OperationRecoveryInspection = {
  operation_id: string;
  generation: number;
  state: 'accepted' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation';
  failure_code?: string;
  cancellation_requested: boolean;
  execution_kind: 'http' | 'workflow' | 'job';
  execution_state: string;
  invocation_id?: string;
  job_run_id?: string;
  workflow_run_id?: string;
  attempt: number;
  deployment_id: string;
  release_id?: string;
  steps: Array<OperationRecoveryStep>;
  artifacts: Array<OperationRecoveryArtifact>;
  retry_blockers: Array<string>;
  /**
   * Digest of durable execution evidence and file bindings; excludes observation time and independent notification state.
   */
  inspection_revision: string;
  observed_at: string;
};

