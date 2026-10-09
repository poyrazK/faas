/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
import type { OperationWorkflowBlockerResolution } from './OperationWorkflowBlockerResolution.js';
import type { OperationWorkflowDependency } from './OperationWorkflowDependency.js';
import type { OperationWorkflowEvidenceMilestone } from './OperationWorkflowEvidenceMilestone.js';
import type { OperationWorkflowResolutionVerification } from './OperationWorkflowResolutionVerification.js';
/**
 * One retained app-reported state update. Pages are ordered by revision, then stable publication and report identifiers.
 */
export type OperationWorkflowStateHistoryEntry = {
  /**
   * Current retained-evidence status for verification obligations in this history report.
   */
  resolution_verifications?: Array<OperationWorkflowResolutionVerification>;
  /**
   * At this retained historical revision, full replacement snapshot of direct prerequisite references.
   */
  depends_on?: Array<OperationWorkflowDependency>;
  /**
   * At this retained historical revision, same-state metadata update mutually exclusive with other metadata-only flags.
   */
  dependencies_only?: boolean;
  /**
   * At this retained historical revision, explicit application-defined business result for a declared terminal state.
   */
  outcome_code?: string;
  /**
   * At this retained historical revision, public UTF-8 description limited to 512 bytes without control characters. Required when outcome_code is supplied.
   */
  outcome_description?: string;
  /**
   * At this retained historical revision, same-state terminal outcome report. Requires from_state equal to state and no milestone evidence. Mutually exclusive with deadline_only and blockers_only. SDKs preserve current blockers and deadline.
   */
  outcome_only?: boolean;
  /**
   * At this retained historical revision, optional application-reported due time. Omitted or empty in a report clears the deadline. Transactional SDKs inherit it from their counter unless explicitly updated.
   */
  deadline_at?: string;
  /**
   * At this retained historical revision, same-state deadline snapshot update. Requires from_state equal to state and no milestone evidence. Mutually exclusive with blockers_only. SDKs preserve current blockers.
   */
  deadline_only?: boolean;
  blocker_resolutions?: Array<OperationWorkflowBlockerResolution>;
  blockers?: Array<OperationWorkflowBlocker>;
  /**
   * At this retained historical revision, same-state blocker replacement. Requires from_state equal to state and no milestone evidence; not a business transition.
   */
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

