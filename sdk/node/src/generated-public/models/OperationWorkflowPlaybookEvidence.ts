/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookEvidence. Durable advisory checklist coordination, independent of application business success.
 */
export type OperationWorkflowPlaybookEvidence = {
  /**
   * OperationWorkflowPlaybookEvidence.operation_id. Operation checked against exact customer, application, environment and business subject.
   */
  operation_id: string;
  /**
   * OperationWorkflowPlaybookEvidence.milestone_id. Optional retained fact additionally checked against pinned workflow, instance and contract version.
   */
  milestone_id?: string;
};

