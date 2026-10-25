/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowResolutionVerification } from './OperationWorkflowResolutionVerification.js';
/**
 * Saved resolution identity and latest watch disposition. Missing proof is unavailable; invalidated means an explicit later application report reopened the blocker. Proof is the last retained observation, not current readiness.
 */
export type OperationWorkflowReadinessVerificationWatchItem = {
  /**
   * OperationWorkflowReadinessVerificationWatchItem.blocker_active. Whether the blocker was present at the last comparable observation; an existing active blocker is not a new reopening.
   */
  blocker_active: boolean;
  /**
   * OperationWorkflowReadinessVerificationWatchItem.key. Server-managed advisory verification observation for the retained source completion.
   */
  key: string;
  /**
   * OperationWorkflowReadinessVerificationWatchItem.status. Server-managed advisory verification observation for the retained source completion.
   */
  status: 'awaiting_verification' | 'verified' | 'invalidated' | 'unavailable';
  /**
   * OperationWorkflowReadinessVerificationWatchItem.explanation. Server-managed advisory verification observation for the retained source completion.
   */
  explanation: string;
  /**
   * OperationWorkflowReadinessVerificationWatchItem.reporting_revision. Exact state-report revision observed for the reporting or invalidating Operation.
   */
  reporting_revision?: number;
  /**
   * OperationWorkflowReadinessVerificationWatchItem.reporting_operation_id. Server-managed advisory verification observation for the retained source completion.
   */
  reporting_operation_id?: string;
  proof: OperationWorkflowResolutionVerification;
};

