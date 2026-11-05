/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
import type { OperationWorkflowInstanceTransition } from './OperationWorkflowInstanceTransition.js';
import type { OperationWorkflowResolutionVerification } from './OperationWorkflowResolutionVerification.js';
/**
 * One declared current-state transition evaluated without planned evidence; reporting is advisory.
 */
export type OperationWorkflowReadinessQueueEntry = {
  /**
   * Customer identity present only on the account surface.
   */
  platform_tenant_id?: string;
  subject: OperationSubject;
  /**
   * Declared workflow name.
   */
  workflow: string;
  /**
   * Application workflow instance identifier.
   */
  instance_id: string;
  /**
   * Current reporting Operation identifier.
   */
  operation_id: string;
  /**
   * Evaluated current state revision.
   */
  state_revision: number;
  /**
   * Evaluated pinned workflow contract version.
   */
  contract_version: number;
  transition: OperationWorkflowInstanceTransition;
  /**
   * Shared reporting and retained-evidence evaluation time for this workflow.
   */
  evaluated_at: string;
  /**
   * Whether declared requirements are met without planned evidence.
   */
  ready: boolean;
  /**
   * Unmet declared requirement codes.
   */
  reasons: Array<string>;
  /**
   * Reporting or verification context that does not change readiness.
   */
  advisories: Array<string>;
  /**
   * Sorted unique blocker and pending verification owners for the target Operation.
   */
  owners: Array<string>;
  /**
   * Whether a matching blocker or pending verification has no owner.
   */
  unassigned: boolean;
  /**
   * Application-reported blockers for this transition Operation.
   */
  blockers: Array<OperationWorkflowBlocker>;
  /**
   * Required planned milestone names absent from the empty plan.
   */
  missing_milestones: Array<string>;
  /**
   * Declared prerequisite workflows without a reported dependency reference.
   */
  missing_dependency_workflows: Array<string>;
  /**
   * Canonical preview of pending evidence obligations for this transition Operation.
   */
  pending_verifications?: Array<OperationWorkflowResolutionVerification>;
  /**
   * More pending evidence obligations exist than the queue preview contains.
   */
  pending_verifications_truncated: boolean;
  /**
   * Exact pending obligation count before previews.
   */
  pending_verification_count: number;
  /**
   * Current-state reporting coverage status; unknown coverage does not invent missing evidence.
   */
  reporting_status: string;
  /**
   * Known missing condition reports when reporting coverage is comparable.
   */
  missing_report_count: number;
  /**
   * Known overdue condition reports when reporting coverage is comparable.
   */
  overdue_report_count: number;
};

