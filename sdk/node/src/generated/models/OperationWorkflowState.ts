/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
import type { OperationWorkflowBlockerResolution } from './OperationWorkflowBlockerResolution.js';
import type { OperationWorkflowDependency } from './OperationWorkflowDependency.js';
import type { OperationWorkflowEvidenceMilestone } from './OperationWorkflowEvidenceMilestone.js';
/**
 * Latest app-reported state for one declared workflow instance, including terminal and staleness indicators.
 */
export type OperationWorkflowState = {
  /**
   * In the latest retained state, full replacement snapshot of direct prerequisite references.
   */
  depends_on?: Array<OperationWorkflowDependency>;
  /**
   * In the latest retained state, same-state metadata update mutually exclusive with other metadata-only flags.
   */
  dependencies_only?: boolean;
  /**
   * In the latest retained state, explicit application-defined business result for a declared terminal state.
   */
  outcome_code?: string;
  /**
   * In the latest retained state, public UTF-8 description limited to 512 bytes without control characters. Required when outcome_code is supplied.
   */
  outcome_description?: string;
  /**
   * In the latest retained state, same-state terminal outcome report. Requires from_state equal to state and no milestone evidence. Mutually exclusive with deadline_only and blockers_only. SDKs preserve current blockers and deadline.
   */
  outcome_only?: boolean;
  /**
   * True for an active workflow at or after its application-reported deadline.
   */
  overdue: boolean;
  /**
   * Whole elapsed seconds since the missed due time.
   */
  overdue_seconds?: number;
  /**
   * In the latest retained state, optional application-reported due time. Omitted or empty in a report clears the deadline. Transactional SDKs inherit it from their counter unless explicitly updated.
   */
  deadline_at?: string;
  /**
   * In the latest retained state, same-state deadline snapshot update. Requires from_state equal to state and no milestone evidence. Mutually exclusive with blockers_only. SDKs preserve current blockers.
   */
  deadline_only?: boolean;
  blocker_resolutions?: Array<OperationWorkflowBlockerResolution>;
  /**
   * Identity of the latest reported snapshot.
   */
  report_id?: string;
  /**
   * Operation that published the latest snapshot.
   */
  operation_id?: string;
  blockers?: Array<OperationWorkflowBlocker>;
  /**
   * In the latest retained state, same-state blocker replacement. Requires from_state equal to state and no milestone evidence; not a business transition.
   */
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

