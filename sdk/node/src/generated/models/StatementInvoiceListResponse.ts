/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StatementInvoiceExportResponse } from './StatementInvoiceExportResponse.js';
/**
 * One page of an account's exported invoices, newest first.
 */
export type StatementInvoiceListResponse = {
  invoices: Array<StatementInvoiceExportResponse>;
  /**
   * Pass as cursor for the next invoice page; absent on the last page.
   */
  next_cursor?: string;
};

