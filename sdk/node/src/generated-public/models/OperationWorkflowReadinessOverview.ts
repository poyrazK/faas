/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowTransitionReadiness } from './OperationWorkflowTransitionReadiness.js';
/**
 * At most 100 current-state declared edges evaluated without planned milestones. Counts cover all declared current-state edges; history cursors do not paginate this overview.
 */
export type OperationWorkflowReadinessOverview = {
  items: Array<OperationWorkflowTransitionReadiness>;
  transition_count: number;
  has_more: boolean;
};

