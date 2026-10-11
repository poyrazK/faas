/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
import type { OperationWorkflowBlockerResolution } from './OperationWorkflowBlockerResolution.js';
import type { OperationWorkflowDependency } from './OperationWorkflowDependency.js';
import type { OperationWorkflowEvidenceMilestone } from './OperationWorkflowEvidenceMilestone.js';
/**
 * Acknowledgement returned after the platform records an app-reported workflow-state update.
 */
export type OperationWorkflowStateReportResponse = {
  /**
   * In the accepted report acknowledgement, full replacement snapshot of direct prerequisite references.
   */
  depends_on?: Array<OperationWorkflowDependency>;
  /**
   * In the accepted report acknowledgement, same-state metadata update mutually exclusive with other metadata-only flags.
   */
  dependencies_only?: boolean;
  /**
   * In the accepted report acknowledgement, explicit application-defined business result for a declared terminal state.
   */
  outcome_code?: string;
  /**
   * In the accepted report acknowledgement, public UTF-8 description limited to 512 bytes without control characters. Required when outcome_code is supplied.
   */
  outcome_description?: string;
  /**
   * In the accepted report acknowledgement, same-state terminal outcome report. Requires from_state equal to state and no milestone evidence. Mutually exclusive with deadline_only and blockers_only. SDKs preserve current blockers and deadline.
   */
  outcome_only?: boolean;
  /**
   * In the accepted report acknowledgement, optional application-reported due time. Omitted or empty in a report clears the deadline. Transactional SDKs inherit it from their counter unless explicitly updated.
   */
  deadline_at?: string;
  /**
   * In the accepted report acknowledgement, same-state deadline snapshot update. Requires from_state equal to state and no milestone evidence. Mutually exclusive with blockers_only. SDKs preserve current blockers.
   */
  deadline_only?: boolean;
  blocker_resolutions?: Array<OperationWorkflowBlockerResolution>;
  blockers?: Array<OperationWorkflowBlocker>;
  /**
   * In the accepted report acknowledgement, same-state blocker replacement. Requires from_state equal to state and no milestone evidence; not a business transition.
   */
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

