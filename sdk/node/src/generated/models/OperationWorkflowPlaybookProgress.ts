/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybook } from './OperationWorkflowPlaybook.js';
import type { OperationWorkflowPlaybookApplicability } from './OperationWorkflowPlaybookApplicability.js';
import type { OperationWorkflowPlaybookProgressChange } from './OperationWorkflowPlaybookProgressChange.js';
import type { OperationWorkflowPlaybookProgressItem } from './OperationWorkflowPlaybookProgressItem.js';
/**
 * OperationWorkflowPlaybookProgress. Durable advisory checklist coordination, independent of application business success.
 */
export type OperationWorkflowPlaybookProgress = {
  /**
   * OperationWorkflowPlaybookProgress.applicability. Live advisory observation included in case overviews only; absent from stored progress and mutation responses. Frozen guidance, revision and history remain unchanged.
   */
  applicability?: OperationWorkflowPlaybookApplicability;
  /**
   * OperationWorkflowPlaybookProgress.id. Stable checklist identity; repeated starts for the same pinned case and definition return the existing record.
   */
  id: string;
  /**
   * OperationWorkflowPlaybookProgress.revision. Optimistic concurrency revision.
   */
  revision: number;
  /**
   * OperationWorkflowPlaybookProgress.playbook. Frozen full application guidance; deployment updates never upgrade this record.
   */
  playbook: OperationWorkflowPlaybook;
  /**
   * OperationWorkflowPlaybookProgress.contract_version. Pinned workflow contract revision.
   */
  contract_version: number;
  /**
   * OperationWorkflowPlaybookProgress.definition_id. Immutable original reporting definition.
   */
  definition_id: string;
  /**
   * OperationWorkflowPlaybookProgress.reporting_operation_id. Original reporting operation used when the checklist started.
   */
  reporting_operation_id: string;
  /**
   * OperationWorkflowPlaybookProgress.items. Frozen evidence labels and their latest review records.
   */
  items: Array<OperationWorkflowPlaybookProgressItem>;
  /**
   * OperationWorkflowPlaybookProgress.completed_count. Number of checklist items reviewed as completed.
   */
  completed_count: number;
  /**
   * OperationWorkflowPlaybookProgress.checklist_complete. Every item in a nonempty checklist is completed; conveys no application readiness or business outcome.
   */
  checklist_complete: boolean;
  /**
   * OperationWorkflowPlaybookProgress.created_at. Server start clock.
   */
  created_at: string;
  /**
   * OperationWorkflowPlaybookProgress.created_by. Authenticated checklist creator.
   */
  created_by: string;
  /**
   * OperationWorkflowPlaybookProgress.updated_at. Latest review clock.
   */
  updated_at: string;
  /**
   * OperationWorkflowPlaybookProgress.history. Most recent 64 committed item review snapshots, in revision order.
   */
  history: Array<OperationWorkflowPlaybookProgressChange>;
  /**
   * OperationWorkflowPlaybookProgress.history_truncated. Older review changes were pruned from the bounded history.
   */
  history_truncated: boolean;
};

