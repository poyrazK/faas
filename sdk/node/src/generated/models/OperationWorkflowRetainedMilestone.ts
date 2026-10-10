/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowInstanceMilestoneRef } from './OperationWorkflowInstanceMilestoneRef.js';
import type { OperationWorkflowRetainedMilestoneRequirement } from './OperationWorkflowRetainedMilestoneRequirement.js';
/**
 * Verification of a retained app-reported prerequisite fact within the selected ownership boundary, subject, workflow instance, and contract version. Verified does not attest to an external provider or authorize business writes. Awaiting verification means no matching fact is currently retained and does not prove it never occurred.
 */
export type OperationWorkflowRetainedMilestone = {
  requirement: OperationWorkflowRetainedMilestoneRequirement;
  status: 'verified' | 'awaiting_verification';
  /**
   * Latest matching retained fact by publication time; present only for verified prerequisites.
   */
  evidence?: OperationWorkflowInstanceMilestoneRef;
};

