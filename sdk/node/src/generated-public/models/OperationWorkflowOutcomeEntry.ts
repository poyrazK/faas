/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
/**
 * One retained terminal workflow instance with its explicitly reported business result and ownership context.
 */
export type OperationWorkflowOutcomeEntry = {
  app_id: string;
  scope: string;
  /**
   * Present only in account-operator responses.
   */
  platform_tenant_id?: string;
  subject: OperationSubject;
  /**
   * Operation that published the current state report.
   */
  operation_id: string;
  state: OperationWorkflowState;
};

