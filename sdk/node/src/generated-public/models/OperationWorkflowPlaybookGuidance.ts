/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybook } from './OperationWorkflowPlaybook.js';
/**
 * OperationWorkflowPlaybookGuidance. Application-owned advisory guidance; evidence requirements are human checklists and links are never executed by Gregale.
 */
export type OperationWorkflowPlaybookGuidance = {
  /**
   * OperationWorkflowPlaybookGuidance.playbook. Matched advisory rule from the exact reporting definition.
   */
  playbook: OperationWorkflowPlaybook;
  /**
   * OperationWorkflowPlaybookGuidance.contract_version. Workflow contract version of the retained current report.
   */
  contract_version: number;
  /**
   * OperationWorkflowPlaybookGuidance.reporting_operation_id. Operation whose immutable definition supplied this playbook.
   */
  reporting_operation_id: string;
};

