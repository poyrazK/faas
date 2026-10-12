/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowInstanceMilestoneRef } from './OperationWorkflowInstanceMilestoneRef.js';
import type { OperationWorkflowRetainedMilestoneRequirement } from './OperationWorkflowRetainedMilestoneRequirement.js';
/**
 * Identity of the retained fact that invalidated a prerequisite by business occurrence time. Equal occurrence timestamps invalidate conservatively; a later prerequisite fact can satisfy the requirement again. Normal retention applies to both sources.
 */
export type OperationWorkflowRetainedMilestoneInvalidation = {
  source: OperationWorkflowRetainedMilestoneRequirement;
  evidence: OperationWorkflowInstanceMilestoneRef;
};

