/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CommitEventPublication } from './CommitEventPublication.js';
/**
 * Newly created sources return operation_id for a managed Gregale Operation. Retained legacy internal receipts return invocation_id instead. Event destinations instead return publication. Exactly one invocation, operation or publication identity is present. The publication receipt survives event retention and does not imply subscriber success.
 */
export type CommitReceiptResponse = {
  receipt_id: string;
  source_id: string;
  event_id: string;
  /**
   * Present for operation or legacy invocation destinations.
   */
  operation_url?: string;
  invocation_id?: string;
  operation_id?: string;
  accepted_at: string;
  publication?: CommitEventPublication;
};

