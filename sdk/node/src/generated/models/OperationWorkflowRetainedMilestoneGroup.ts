/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRetainedMilestoneRequirement } from './OperationWorkflowRetainedMilestoneRequirement.js';
/**
 * Named alternatives in a pinned workflow edge. Any one verified retained fact satisfies this group; all groups and mandatory prerequisites must pass. Group IDs and selectors are canonicalized and require a new contract version when changed.
 */
export type OperationWorkflowRetainedMilestoneGroup = {
  id: string;
  any_of: Array<OperationWorkflowRetainedMilestoneRequirement>;
};

