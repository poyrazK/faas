/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryHistoryEntry } from './WorkflowRecoveryHistoryEntry.js';
/**
 * A bounded page of retained recovery evidence with a fixed observation window.
 */
export type WorkflowRecoveryHistory = {
  observed_at: string;
  created_after: string;
  created_before: string;
  entries: Array<WorkflowRecoveryHistoryEntry>;
  next_after?: string;
  /**
   * True indicates retained evidence rather than a complete immutable audit log.
   */
  retained_observations: boolean;
};

