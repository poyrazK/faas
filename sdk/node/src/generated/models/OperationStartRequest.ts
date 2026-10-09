/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubmissionScope } from './OperationSubmissionScope.js';
import type { OperationTenantIdentity } from './OperationTenantIdentity.js';
/**
 * Submission owned by the authenticated platform tenant.
 */
export type OperationStartRequest = {
  definition_id: string;
  /**
   * JSON input matching the pinned definition.
   */
  input: any;
  expected_identity?: OperationTenantIdentity;
  expected_scope?: OperationSubmissionScope;
};

