import type { OperationWorkflowDependentInstance } from './OperationWorkflowDependentInstance.js';
export type OperationWorkflowDependencyImpact = {
  items: OperationWorkflowDependentInstance[];
  workflow_count: number;
  impacted_workflow_count: number;
  has_more: boolean;
};
