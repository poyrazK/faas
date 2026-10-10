/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowResolutionVerification } from './OperationWorkflowResolutionVerification.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
/**
 * Current state and verification findings for one complete-history contributor. Customer responses omit tenant identifiers. Observed seconds measure the selected historical group rather than current ownership alone.
 */
export type OperationWorkflowPerformanceInstance = {
  platform_tenant_id?: string;
  subject: OperationSubject;
  operation_id: string;
  state: OperationWorkflowState;
  observed_seconds: number;
  resolution_verifications: Array<OperationWorkflowResolutionVerification>;
  awaiting_verification_count: number;
  resolution_verification_count: number;
};

