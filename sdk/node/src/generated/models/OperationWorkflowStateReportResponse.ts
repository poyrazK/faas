import type {OperationWorkflowDependency} from './OperationWorkflowDependency.js';
import type { OperationWorkflowBlockerResolution } from './OperationWorkflowBlockerResolution.js';
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowEvidenceMilestone } from './OperationWorkflowEvidenceMilestone.js';
/**
 * Acknowledgement returned after the platform records an app-reported workflow-state update.
 */
export type OperationWorkflowStateReportResponse = {
 depends_on?: OperationWorkflowDependency[]; dependencies_only?: boolean;
 outcome_code?: string; outcome_description?: string; outcome_only?: boolean;
 deadline_at?: string;
 deadline_only?: boolean;
  blocker_resolutions?: OperationWorkflowBlockerResolution[];
  blockers?: OperationWorkflowBlocker[];
  blockers_only?: boolean;
  id: string;
  operation_id: string;
  workflow: string;
  instance_id: string;
  /**
   * Previous app state when a declared transition was reported.
   */
  from_state?: string;
  state: string;
  revision: number;
  contract_version: number;
  evidence_milestones?: Array<OperationWorkflowEvidenceMilestone>;
};

