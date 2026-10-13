/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookEvidence } from './OperationWorkflowPlaybookEvidence.js';
/**
 * OperationWorkflowPlaybookProgressItem. Durable advisory checklist coordination, independent of application business success.
 */
export type OperationWorkflowPlaybookProgressItem = {
  /**
   * OperationWorkflowPlaybookProgressItem.label. Frozen application-authored evidence requirement.
   */
  label: string;
  /**
   * OperationWorkflowPlaybookProgressItem.status. Human checklist review state.
   */
  status: 'pending' | 'completed';
  /**
   * OperationWorkflowPlaybookProgressItem.note. Latest reviewer note.
   */
  note: string;
  /**
   * OperationWorkflowPlaybookProgressItem.evidence. Scoped references checked when edited; future retention availability is not guaranteed.
   */
  evidence: Array<OperationWorkflowPlaybookEvidence>;
  /**
   * OperationWorkflowPlaybookProgressItem.reviewed_by. Authenticated account or API-key reviewer, assigned by the server.
   */
  reviewed_by?: string;
  /**
   * OperationWorkflowPlaybookProgressItem.reviewed_at. Server review clock for the latest edit.
   */
  reviewed_at?: string;
};

