/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable receipt linking a finalized usage statement to a customer-owned external invoice.
 */
export type APIConsumerUsageStatementHandoffResponse = {
  id: string;
  statement_id: string;
  external_invoice_id: string;
  currency?: string;
  amount_millicents: number;
  created_at: string;
};

