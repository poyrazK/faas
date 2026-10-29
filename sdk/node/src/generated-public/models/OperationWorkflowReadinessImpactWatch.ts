/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessImpactSummary } from './OperationWorkflowReadinessImpactSummary.js';
/**
 * OperationWorkflowReadinessImpactWatch. Protected business result monitoring of retained unresolved human reviews in a scoped application environment; advisory confidence only.
 */
export type OperationWorkflowReadinessImpactWatch = {
  /**
   * OperationWorkflowReadinessImpactWatch.acknowledge_after_seconds. Optional response window from delivery of critical or overdue impact alerts, requires a nonempty owner. Zero omits the target; clearance notices have no target. Acknowledgment is independent of resolving source review obligations.
   */
  acknowledge_after_seconds?: number;
  /**
   * OperationWorkflowReadinessImpactWatch.fallback_owner. Optional distinct fallback team, requires a positive response window. Guarded handoff preserves manual assignment history and the original deadline.
   */
  fallback_owner?: string;
  /**
   * OperationWorkflowReadinessImpactWatch.workflow. Exact workflow identity.
   */
  workflow: string;
  /**
   * OperationWorkflowReadinessImpactWatch.kind. Protected result category.
   */
  kind: 'milestone' | 'outcome';
  /**
   * OperationWorkflowReadinessImpactWatch.code. Exact protected result slug.
   */
  code: string;
  /**
   * OperationWorkflowReadinessImpactWatch.owner. Optional team assigned new impact notifications; does not filter review queue source owners.
   */
  owner?: string;
  /**
   * OperationWorkflowReadinessImpactWatch.current. Last accepted retained review baseline, absent for zero reviews. Preserved when queue disappearance cannot be confirmed against tracked source progress.
   */
  current?: OperationWorkflowReadinessImpactSummary;
  /**
   * OperationWorkflowReadinessImpactWatch.evaluated_at. Time of the last accepted baseline observation.
   */
  evaluated_at: string;
  /**
   * OperationWorkflowReadinessImpactWatch.coverage_incomplete. Candidate-window or 64-obligation tracking limit, missing source, or unresolved source after queue disappearance suppresses a clearance assertion.
   */
  coverage_incomplete: boolean;
};

