/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowEvidenceMilestone } from './OperationWorkflowEvidenceMilestone.js';
/**
 * Idempotent app-reported state update already committed with the business write. Revision is assigned transactionally by the application SDK. Contract version is filled from the pinned definition when omitted.
 */
export type OperationWorkflowStateReport = {
  id: string;
  workflow: string;
  instance_id: string;
  /**
   * Current app database state before the requested declared transition. Required when the pinned workflow declares transitions.
   */
  from_state?: string;
  state: string;
  revision: number;
  occurred_at: string;
  /**
   * Optional version precondition; the server fills this from the immutable Operation definition.
   */
  contract_version?: number;
  /**
   * Facts committed in the same application transaction as this transition.
   */
  evidence_milestones?: Array<OperationWorkflowEvidenceMilestone>;
};

