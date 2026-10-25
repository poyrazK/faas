/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessSnapshotEntry } from './OperationWorkflowReadinessSnapshotEntry.js';
import type { OperationWorkflowReadinessVerificationChange } from './OperationWorkflowReadinessVerificationChange.js';
/**
 * Durable in-app notification linked to its original completion watch. Source describes the original notification edge; verified does not imply workflow readiness or execution authority.
 */
export type OperationWorkflowReadinessVerificationEvent = {
  /**
   * OperationWorkflowReadinessVerificationEvent.watch_id. Server-managed advisory verification observation for the retained source completion.
   */
  watch_id: string;
  /**
   * OperationWorkflowReadinessVerificationEvent.source_notification_id. Server-managed advisory verification observation for the retained source completion.
   */
  source_notification_id: string;
  /**
   * OperationWorkflowReadinessVerificationEvent.source_sequence. Server-managed advisory verification observation for the retained source completion.
   */
  source_sequence: number;
  /**
   * OperationWorkflowReadinessVerificationEvent.checked_at. Server-managed advisory verification observation for the retained source completion.
   */
  checked_at: string;
  source: OperationWorkflowReadinessSnapshotEntry;
  /**
   * OperationWorkflowReadinessVerificationEvent.changes. Server-managed advisory verification observation for the retained source completion.
   */
  changes: Array<OperationWorkflowReadinessVerificationChange>;
};

