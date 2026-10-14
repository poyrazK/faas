/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybook } from './OperationWorkflowPlaybook.js';
import type { OperationWorkflowPlaybookApplicability } from './OperationWorkflowPlaybookApplicability.js';
import type { OperationWorkflowPlaybookBusinessOutcome } from './OperationWorkflowPlaybookBusinessOutcome.js';
import type { OperationWorkflowPlaybookOutcomeFollowUp } from './OperationWorkflowPlaybookOutcomeFollowUp.js';
import type { OperationWorkflowPlaybookProgressChange } from './OperationWorkflowPlaybookProgressChange.js';
import type { OperationWorkflowPlaybookProgressItem } from './OperationWorkflowPlaybookProgressItem.js';
import type { OperationWorkflowPlaybookReview } from './OperationWorkflowPlaybookReview.js';
/**
 * OperationWorkflowPlaybookProgress. Durable advisory checklist coordination, independent of application business success.
 */
export type OperationWorkflowPlaybookProgress = {
  /**
   * OperationWorkflowPlaybookProgress.outcome_follow_ups. Human-reviewed outcome-specific follow-up lists; original checklist counts and business reports remain independent.
   */
  outcome_follow_ups?: Array<OperationWorkflowPlaybookOutcomeFollowUp>;
  /**
   * OperationWorkflowPlaybookProgress.outcome_deadline_at. Immutable checklist creation time plus the pinned application wait duration; human edits and watch enrollment do not reschedule it.
   */
  outcome_deadline_at?: string;
  /**
   * OperationWorkflowPlaybookProgress.start_state_revision. Original case state revision pinned when a playbook with an expected outcome starts; earlier reports cannot establish its business result.
   */
  start_state_revision?: number;
  /**
   * OperationWorkflowPlaybookProgress.business_outcome. Live exact-case result in case reads only; absent from durable progress, review history and mutation responses.
   */
  business_outcome?: OperationWorkflowPlaybookBusinessOutcome;
  /**
   * OperationWorkflowPlaybookProgress.disposition. Coordination disposition. Omitted legacy values mean active. Closed checklists reject item edits and further decisions; this is not business success.
   */
  disposition?: 'active' | 'retired' | 'replaced';
  /**
   * OperationWorkflowPlaybookProgress.latest_review. Latest committed review decision, retained even when earlier decision history is truncated.
   */
  latest_review?: OperationWorkflowPlaybookReview;
  /**
   * OperationWorkflowPlaybookProgress.reviews. Most recent 32 durable review decisions in shared revision order; independent of item review history.
   */
  reviews?: Array<OperationWorkflowPlaybookReview>;
  /**
   * OperationWorkflowPlaybookProgress.reviews_truncated. Older human review decisions were pruned from the bounded journal.
   */
  reviews_truncated?: boolean;
  /**
   * OperationWorkflowPlaybookProgress.replaces_progress_ids. Case-read-only inverse links from replaced predecessors. Derived from retained exact-case records and never persisted or accepted in requests.
   */
  replaces_progress_ids?: Array<string>;
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

