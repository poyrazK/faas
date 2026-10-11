/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationAcceptedResponse } from './OperationAcceptedResponse.js';
/**
 * Read-only observation; accepted includes receipt and accepted_at. unresolved never proves rejection.
 */
export type OperationSubmissionLookupResponse = {
  state: 'accepted' | 'unresolved' | 'expired';
  receipt?: OperationAcceptedResponse;
  accepted_at?: string;
  idempotency_expires_at?: string;
};

