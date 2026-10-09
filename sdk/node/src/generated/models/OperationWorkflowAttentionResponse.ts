/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowAttentionEntry } from './OperationWorkflowAttentionEntry.js';
/**
 * Bounded attention queue with a stable evaluation time and filter-bound continuation.
 */
export type OperationWorkflowAttentionResponse = {
  items: Array<OperationWorkflowAttentionEntry>;
  /**
   * Staleness evaluation time retained across cursor pages.
   */
  evaluated_at: string;
  /**
   * Opaque continuation bound to account/customer role and every queue filter.
   */
  next_cursor?: string;
};

