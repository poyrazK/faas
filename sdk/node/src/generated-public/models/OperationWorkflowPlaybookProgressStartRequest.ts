/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookProgressStartRequest. Durable advisory checklist coordination, independent of application business success.
 */
export type OperationWorkflowPlaybookProgressStartRequest = {
  /**
   * OperationWorkflowPlaybookProgressStartRequest.playbook_id. Exact matched rule identifier.
   */
  playbook_id: string;
  /**
   * OperationWorkflowPlaybookProgressStartRequest.playbook_version. Pinned rule revision.
   */
  playbook_version: number;
  /**
   * OperationWorkflowPlaybookProgressStartRequest.contract_version. Pinned workflow contract version.
   */
  contract_version: number;
  /**
   * OperationWorkflowPlaybookProgressStartRequest.reporting_operation_id. Exact operation reporting the matched current guidance.
   */
  reporting_operation_id: string;
};

