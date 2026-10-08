import type { OperationWorkflowDependencyFinding } from './OperationWorkflowDependencyFinding.js';
export interface OperationWorkflowDependencyTrace {
  findings: OperationWorkflowDependencyFinding[];
  visited_workflow_count: number;
  examined_dependency_count: number;
  depth_limit: number;
  workflow_limit: number;
  finding_limit: number;
  dependency_limit: number;
  truncated: boolean;
  limits_reached?: Array<'depth' | 'workflows' | 'findings' | 'dependencies'>;
}
