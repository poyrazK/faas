/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowReadinessImpactWatchRequest. Protected business result monitoring of retained unresolved human reviews in a scoped application environment; advisory confidence only.
 */
export type OperationWorkflowReadinessImpactWatchRequest = {
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

