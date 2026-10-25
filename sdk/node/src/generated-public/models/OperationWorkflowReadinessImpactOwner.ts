/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowReadinessImpactOwner. Effective queue ownership within a retained business impact group.
 */
export type OperationWorkflowReadinessImpactOwner = {
  /**
   * OperationWorkflowReadinessImpactOwner.owner. Current inbox owner label; empty means unassigned.
   */
  owner: string;
  /**
   * OperationWorkflowReadinessImpactOwner.task_count. Matching retained inbox delivery tasks for this owner.
   */
  task_count: number;
  /**
   * OperationWorkflowReadinessImpactOwner.overdue_count. Matching tasks with overdue queue attention.
   */
  overdue_count: number;
};

