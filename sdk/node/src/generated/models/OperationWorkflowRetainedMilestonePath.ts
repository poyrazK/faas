/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRetainedMilestoneRequirement } from './OperationWorkflowRetainedMilestoneRequirement.js';
/**
 * One uniquely named business path within an alternative group. Every declared retained fact must be verified together at the evaluation time; selectors are canonicalized and unique within the path.
 */
export type OperationWorkflowRetainedMilestonePath = {
  id: string;
  all_of: Array<OperationWorkflowRetainedMilestoneRequirement>;
};

