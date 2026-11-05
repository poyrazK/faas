/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessSnapshotEntry } from './OperationWorkflowReadinessSnapshotEntry.js';
/**
 * Caller-held compact readiness observations for at most 100 returned transitions. No historical authentication or execution authority is implied.
 */
export type OperationWorkflowReadinessSnapshot = {
  /**
   * ReadinessSnapshot. Snapshot format version; currently 1.
   */
  version: 1;
  /**
   * ReadinessSnapshot. Account app environment customer-selector and role fingerprint; preserve unchanged when saving.
   */
  query: string;
  /**
   * ReadinessSnapshot. Time the live snapshot capture completed.
   */
  captured_at: string;
  /**
   * ReadinessSnapshot. Saved transition observations.
   */
  items: Array<OperationWorkflowReadinessSnapshotEntry>;
};

