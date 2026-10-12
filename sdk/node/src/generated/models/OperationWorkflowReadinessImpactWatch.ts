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

