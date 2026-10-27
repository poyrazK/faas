/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRetainedMilestonePath } from './OperationWorkflowRetainedMilestonePath.js';
import type { OperationWorkflowRetainedMilestoneRequirement } from './OperationWorkflowRetainedMilestoneRequirement.js';
/**
 * Named alternatives in a pinned workflow edge. Declare either a nonempty any_of list or nonempty named paths. Any verified fact satisfies a flat group; every fact in one complete path satisfies a path group. All groups and mandatory prerequisites must pass. Across named paths a group permits at most 16 fact occurrences including repeated selectors. Changes require a new contract version.
 */
export type OperationWorkflowRetainedMilestoneGroup = {
  id: string;
  any_of?: Array<OperationWorkflowRetainedMilestoneRequirement>;
  paths?: Array<OperationWorkflowRetainedMilestonePath>;
};

