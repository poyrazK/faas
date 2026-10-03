/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Newly created sources return operation_id for a managed Gregale Operation. Retained legacy internal receipts return invocation_id instead. Exactly one work identity is present.
 */
export type CommitReceiptResponse = {
  receipt_id: string;
  source_id: string;
  event_id: string;
  invocation_id?: string;
  operation_id?: string;
  accepted_at: string;
  operation_url: string;
};

