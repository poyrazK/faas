/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Counts and cursor for one provider invoice-history page.
 */
export type InvoiceHistoryBackfillResponse = {
  provider: 'stripe' | 'paddle' | 'polar';
  scanned: number;
  imported: number;
  skipped: number;
  has_more: boolean;
  next_cursor?: string;
};

