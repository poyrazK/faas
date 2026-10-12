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
   * OperationWorkflowPlaybookProgressUpdateRequest.owner. Optional manual outcome-step reassignment; omission preserves the current owner and an empty value clears it. Requires outcome_code.
   */
  owner?: string;
  /**
   * OperationWorkflowPlaybookProgressUpdateRequest.outcome_code. Optional outcome-specific follow-up selector; item_index addresses that pinned step list. First review captures fresh comparable evidence of this exact outcome.
   */
  outcome_code?: string;
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

