/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRetainedMilestoneRequirement } from './OperationWorkflowRetainedMilestoneRequirement.js';
/**
 * Versioned link between a declared retained prerequisite and a distinct invalidating source bound to a transaction-backed workflow step. Sources must share ownership, subject, workflow instance, and contract version.
 */
export type OperationWorkflowRetainedMilestoneInvalidationRule = {
  prerequisite: OperationWorkflowRetainedMilestoneRequirement;
  invalidated_by: OperationWorkflowRetainedMilestoneRequirement;
};

