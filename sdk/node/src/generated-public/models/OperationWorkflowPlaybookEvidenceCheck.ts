/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowInstanceMilestoneRef } from './OperationWorkflowInstanceMilestoneRef.js';
import type { OperationWorkflowPlaybookEvidenceRequirement } from './OperationWorkflowPlaybookEvidenceRequirement.js';
import type { OperationWorkflowRetainedMilestone } from './OperationWorkflowRetainedMilestone.js';
/**
 * OperationWorkflowPlaybookEvidenceCheck. Ephemeral case-read observation of a pinned requirement, separate from human review and attachment validity. Contract or guidance changes, missing declarations and incomplete coverage remain unavailable. Does not change progress or authorize execution.
 */
export type OperationWorkflowPlaybookEvidenceCheck = {
  /**
   * OperationWorkflowPlaybookEvidenceCheck.requirement. Frozen application-defined selector.
   */
  requirement: OperationWorkflowPlaybookEvidenceRequirement;
  /**
   * OperationWorkflowPlaybookEvidenceCheck.status. Current retained evidence observation, not external proof or business success.
   */
  status: 'satisfied' | 'missing' | 'stale' | 'invalidated' | 'unavailable';
  /**
   * OperationWorkflowPlaybookEvidenceCheck.reason. Stable diagnostic code explaining the observation or uncertainty.
   */
  reason: string;
  /**
   * OperationWorkflowPlaybookEvidenceCheck.checked_at. Case evidence observation clock; prerequisite freshness retains its original evaluation clock.
   */
  checked_at: string;
  /**
   * OperationWorkflowPlaybookEvidenceCheck.state_revision. Current report revision when available.
   */
  state_revision?: number;
  /**
   * OperationWorkflowPlaybookEvidenceCheck.reporting_operation_id. Current application state-report operation.
   */
  reporting_operation_id?: string;
  /**
   * OperationWorkflowPlaybookEvidenceCheck.review_suggested. Completed human item currently has an unsatisfied or unavailable requirement; no review is modified.
   */
  review_suggested: boolean;
  /**
   * OperationWorkflowPlaybookEvidenceCheck.evidence. Current retained milestone reference, including stale or invalidated facts when present.
   */
  evidence?: OperationWorkflowInstanceMilestoneRef;
  /**
   * OperationWorkflowPlaybookEvidenceCheck.prerequisite. Exact existing readiness diagnostic, including freshness and invalidation evidence.
   */
  prerequisite?: OperationWorkflowRetainedMilestone;
};

