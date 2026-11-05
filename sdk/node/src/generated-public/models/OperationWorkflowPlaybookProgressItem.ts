/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookEvidence } from './OperationWorkflowPlaybookEvidence.js';
import type { OperationWorkflowPlaybookEvidenceCheck } from './OperationWorkflowPlaybookEvidenceCheck.js';
import type { OperationWorkflowPlaybookReviewImpact } from './OperationWorkflowPlaybookReviewImpact.js';
import type { OperationWorkflowPlaybookStepReadiness } from './OperationWorkflowPlaybookStepReadiness.js';
/**
 * OperationWorkflowPlaybookProgressItem. Durable advisory checklist coordination, independent of application business success.
 */
export type OperationWorkflowPlaybookProgressItem = {
  /**
   * OperationWorkflowPlaybookProgressItem.review_impact. Pinned application-declared business result and priority protected by validity review; does not assert actual business failure or change reported outcome.
   */
  review_impact?: OperationWorkflowPlaybookReviewImpact;
  /**
   * OperationWorkflowPlaybookProgressItem.dependency_review_due_at. Frozen review deadline calculated at prerequisite reopening from the pinned assignment policy. Recovery and inbox rescheduling do not reset it; an explicit review clears it with the obligation.
   */
  dependency_review_due_at?: string;
  /**
   * OperationWorkflowPlaybookProgressItem.dependency_review_revision. Progress revision that reopened a prerequisite; persists until an explicit step review confirms completed work with evidence or requests redo by marking it pending. Prerequisite recovery alone does not clear this obligation.
   */
  dependency_review_revision?: number;
  /**
   * OperationWorkflowPlaybookProgressItem.readiness. Ephemeral human-step prerequisite check, separate from application transition readiness.
   */
  readiness?: OperationWorkflowPlaybookStepReadiness;
  /**
   * OperationWorkflowPlaybookProgressItem.owner. Outcome follow-up team label; human reassignment is recorded with the step review.
   */
  owner?: string;
  /**
   * OperationWorkflowPlaybookProgressItem.due_at. Pinned completion deadline from first list review, independent of business success.
   */
  due_at?: string;
  /**
   * OperationWorkflowPlaybookProgressItem.evidence_check. Optional live requirement check in case overviews only; never stored in reviews, history or mutation responses.
   */
  evidence_check?: OperationWorkflowPlaybookEvidenceCheck;
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

