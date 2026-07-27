/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Invoice } from './Invoice.js';
/**
 * Paginated invoice list. Items is one page (period_end DESC, id
 * DESC order). next_before is the cursor the caller passes on the
 * next request to fetch the older page. Empty Items with 200 OK
 * is the empty-history shape — never 404.
 *
 */
export type InvoiceListResponse = {
  items: Array<Invoice>;
  next_before?: string | null;
};

