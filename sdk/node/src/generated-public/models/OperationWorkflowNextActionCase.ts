/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
/**
 * Exact workflow case affected by a current source action.
 */
export type OperationWorkflowNextActionCase = {
  subject: OperationSubject;
  workflow: string;
  instance_id: string;
  /**
   * Included only for authorized account operator views; platform tenant scope is implicit.
   */
  platform_tenant_id?: string;
};

