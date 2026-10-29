/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowReadinessImpactWatchRequest. Protected business result monitoring of retained unresolved human reviews in a scoped application environment; advisory confidence only.
 */
export type OperationWorkflowReadinessImpactWatchRequest = {
  /**
   * OperationWorkflowReadinessImpactWatchRequest.acknowledge_after_seconds. Optional response window from delivery of critical or overdue impact alerts, requires a nonempty owner. Zero omits the target; clearance notices have no target. Acknowledgment is independent of resolving source review obligations.
   */
  acknowledge_after_seconds?: number;
  /**
   * OperationWorkflowReadinessImpactWatchRequest.fallback_owner. Optional distinct fallback team, requires a positive response window. Guarded handoff preserves manual assignment history and the original deadline.
   */
  fallback_owner?: string;
  /**
   * OperationWorkflowReadinessImpactWatchRequest.workflow. Exact workflow identity.
   */
  workflow: string;
  /**
   * OperationWorkflowReadinessImpactWatchRequest.kind. Protected result category.
   */
  kind: 'milestone' | 'outcome';
  /**
   * OperationWorkflowReadinessImpactWatchRequest.code. Exact protected result slug.
   */
  code: string;
  /**
   * OperationWorkflowReadinessImpactWatchRequest.owner. Optional team assigned new impact notifications; does not filter review queue source owners.
   */
  owner?: string;
};

