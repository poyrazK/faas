/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationTenantIdentity } from './OperationTenantIdentity.js';
/**
 * Selectors for an authenticated customer's retained idempotency identity; bounded to 4096 bytes.
 */
export type OperationSubmissionLookupRequest = {
  app_id: string;
  scope: string;
  name: string;
  idempotency_key: string;
  expected_identity?: OperationTenantIdentity;
};

