/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDependentInstance } from './OperationWorkflowDependentInstance.js';
/**
 * One-hop reverse dependency impact within the selected customer/application/environment. Counts cover all current retained sources; items are capped at 100 with affected sources first. Omitted when no customer can be identified for an unknown account-side prerequisite. Independent of milestone and history pagination.
 */
export type OperationWorkflowDependencyImpact = {
  items: Array<OperationWorkflowDependentInstance>;
  workflow_count: number;
  impacted_workflow_count: number;
  /**
   * True when the full retained relationship count exceeds the displayed 100 items.
   */
  has_more: boolean;
};

