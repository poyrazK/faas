/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
/**
 * Explicit workflow instance selection. Self requires app_id and omits tenant_id; account requires tenant_id and omits app_id. Operation optionally filters candidate edges. Revision/version expectations are evaluated per candidate.
 */
export type OperationWorkflowActionPreviewRequest = {
  /**
   * Required in customer-self mode only.
   */
  app_id?: string;
  /**
   * Required in account mode only.
   */
  tenant_id?: string;
  scope: string;
  subject: OperationSubject;
  workflow: string;
  instance_id: string;
  operation?: string;
  /**
   * Optional expected retained source revision.
   */
  state_revision?: number;
  /**
   * Optional expected selected contract version.
   */
  contract_version?: number;
};

