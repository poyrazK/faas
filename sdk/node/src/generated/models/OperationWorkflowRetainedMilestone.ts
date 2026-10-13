/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowInstanceMilestoneRef } from './OperationWorkflowInstanceMilestoneRef.js';
import type { OperationWorkflowPrerequisiteRecovery } from './OperationWorkflowPrerequisiteRecovery.js';
import type { OperationWorkflowRetainedMilestoneFreshness } from './OperationWorkflowRetainedMilestoneFreshness.js';
import type { OperationWorkflowRetainedMilestoneInvalidation } from './OperationWorkflowRetainedMilestoneInvalidation.js';
import type { OperationWorkflowRetainedMilestoneRequirement } from './OperationWorkflowRetainedMilestoneRequirement.js';
/**
 * Verification of a retained app-reported prerequisite fact within the selected ownership boundary, subject, workflow instance, and contract version. Verified does not attest to an external provider or authorize business writes. Invalidated means an applicable retained fact occurred at or after the prerequisite. Expired means the declared maximum evidence age has been reached at evaluation time. Invalidation takes precedence over expiry. Awaiting verification means no matching eligible fact is currently retained and does not prove it never occurred.
 */
export type OperationWorkflowRetainedMilestone = {
  recovery?: OperationWorkflowPrerequisiteRecovery;
  freshness?: OperationWorkflowRetainedMilestoneFreshness;
  /**
   * Source and evidence identity of the latest applicable invalidating fact. Present only for invalidated prerequisites.
   */
  invalidation?: OperationWorkflowRetainedMilestoneInvalidation;
  requirement: OperationWorkflowRetainedMilestoneRequirement;
  status: 'verified' | 'awaiting_verification' | 'invalidated' | 'expired';
  /**
   * Matching retained prerequisite fact; present for verified, invalidated, and expired results. Edges with invalidation or freshness policies select the latest occurrence at or before evaluation time, with publication and identities breaking ties; other edges retain publication order.
   */
  evidence?: OperationWorkflowInstanceMilestoneRef;
};

