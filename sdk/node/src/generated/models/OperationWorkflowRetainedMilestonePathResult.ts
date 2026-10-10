/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRetainedMilestone } from './OperationWorkflowRetainedMilestone.js';
/**
 * Complete evidence findings for one named alternative path. Satisfied means every requirement is verified at the shared evaluation time; passing facts from separate paths cannot combine to satisfy a group.
 */
export type OperationWorkflowRetainedMilestonePathResult = {
  id: string;
  satisfied: boolean;
  requirements: Array<OperationWorkflowRetainedMilestone>;
};

