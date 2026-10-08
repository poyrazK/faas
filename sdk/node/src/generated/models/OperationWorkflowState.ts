import type {OperationWorkflowDependency} from './OperationWorkflowDependency.js';
import type { OperationWorkflowBlockerResolution } from './OperationWorkflowBlockerResolution.js';
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowEvidenceMilestone } from './OperationWorkflowEvidenceMilestone.js';
/**
 * Latest app-reported state for one declared workflow instance, including terminal and staleness indicators.
 */
export type OperationWorkflowState = {
 depends_on?: OperationWorkflowDependency[]; dependencies_only?: boolean;
 outcome_code?: string; outcome_description?: string; outcome_only?: boolean;
 deadline_at?: string;
 deadline_only?: boolean;
 overdue: boolean;
 overdue_seconds?: number;
  report_id?: string;
  operation_id?: string;
  blocker_resolutions?: OperationWorkflowBlockerResolution[];
  blockers?: OperationWorkflowBlocker[];
  blockers_only?: boolean;
  workflow: string;
  instance_id: string;
  state: string;
  /**
   * True when the state is listed in terminal_states on the pinned workflow definition that reported it.
   */
  terminal: boolean;
  /**
   * True when the app-reported occurrence time plus the pinned state_stale_after threshold is at or before the read time.
   */
  stale: boolean;
  /**
   * App-reported time when the current state became true; used for stale-state age.
   */
  occurred_at: string;
  /**
   * App-declared age threshold for the current state when one is configured.
   */
  stale_after_seconds?: number;
  revision: number;
  contract_version: number;
  evidence_milestones?: Array<OperationWorkflowEvidenceMilestone>;
  updated_at: string;
  /**
   * Included only for account operator feeds.
   */
  platform_tenant_id?: string;
};

