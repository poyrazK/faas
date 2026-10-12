/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookEvidence } from './OperationWorkflowPlaybookEvidence.js';
/**
 * OperationWorkflowPlaybookProgressUpdateRequest. Durable advisory checklist coordination, independent of application business success.
 */
export type OperationWorkflowPlaybookProgressUpdateRequest = {
  /**
   * OperationWorkflowPlaybookProgressUpdateRequest.revision. Expected current progress revision; conflicting edits return 409.
   */
  revision: number;
  /**
   * OperationWorkflowPlaybookProgressUpdateRequest.item_index. Zero-based index in the frozen evidence checklist.
   */
  item_index: number;
  /**
   * OperationWorkflowPlaybookProgressUpdateRequest.status. Human review state; completed requires at least one scoped evidence reference.
   */
  status: 'pending' | 'completed';
  /**
   * OperationWorkflowPlaybookProgressUpdateRequest.note. Required review note, trimmed nonempty and without control characters.
   */
  note: string;
  /**
   * OperationWorkflowPlaybookProgressUpdateRequest.evidence. Replacement evidence references for this item; empty allowed only for pending.
   */
  evidence: Array<OperationWorkflowPlaybookEvidence>;
};

