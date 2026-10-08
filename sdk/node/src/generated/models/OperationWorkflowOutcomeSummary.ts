/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowOutcomeGroup } from './OperationWorkflowOutcomeGroup.js';
export type OperationWorkflowOutcomeSummary = {
  group_by: 'outcome' | 'workflow' | 'customer';
  evaluated_at: string;
  workflow_count: number;
  groups: Array<OperationWorkflowOutcomeGroup>;
  next_cursor?: string;
};

