/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRetainedMilestone } from './OperationWorkflowRetainedMilestone.js';
/**
 * Evaluation of one retained alternative group. Every alternative includes existing freshness and invalidation and recovery diagnostics; unusable alternatives do not deny a group with a verified alternative.
 */
export type OperationWorkflowRetainedMilestoneGroupResult = {
  id: string;
  satisfied: boolean;
  alternatives: Array<OperationWorkflowRetainedMilestone>;
};

