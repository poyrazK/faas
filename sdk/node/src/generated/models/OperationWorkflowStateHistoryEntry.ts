import type {OperationWorkflowDependency} from './OperationWorkflowDependency.js';
import type { OperationWorkflowBlockerResolution } from './OperationWorkflowBlockerResolution.js';
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowEvidenceMilestone } from './OperationWorkflowEvidenceMilestone.js';
/**
 * One retained app-reported state update. Pages are ordered by revision, then stable publication and report identifiers.
 */
export type OperationWorkflowStateHistoryEntry = {
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
   * App state immediately before this retained revision
   */
  from_state?: string;
  state: string;
  revision: number;
  contract_version: number;
  evidence_milestones?: Array<OperationWorkflowEvidenceMilestone>;
  occurred_at: string;
  published_at: string;
  /**
   * Account-owner tenant identifier attached to this report in operator feeds.
   */
  platform_tenant_id?: string;
};

