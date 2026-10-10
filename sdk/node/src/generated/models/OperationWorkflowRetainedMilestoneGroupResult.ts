/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRetainedMilestone } from './OperationWorkflowRetainedMilestone.js';
import type { OperationWorkflowRetainedMilestonePathResult } from './OperationWorkflowRetainedMilestonePathResult.js';
/**
 * Evaluation of one retained alternative group. Flat groups return alternatives; compound groups return paths. Every fact includes freshness and invalidation and recovery diagnostics. A satisfied group requires at least one verified alternative or fully satisfied path.
 */
export type OperationWorkflowRetainedMilestoneGroupResult = {
  id: string;
  satisfied: boolean;
  alternatives?: Array<OperationWorkflowRetainedMilestone>;
  paths?: Array<OperationWorkflowRetainedMilestonePathResult>;
};

