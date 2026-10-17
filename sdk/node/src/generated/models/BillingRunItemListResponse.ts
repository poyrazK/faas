/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingRunItemResponse } from './BillingRunItemResponse.js';
/**
 * One page of a billing run's customers, ordered by kind and ID.
 */
export type BillingRunItemListResponse = {
  items: Array<BillingRunItemResponse>;
  /**
   * Pass as cursor to read the next page; absent on the last page.
   */
  next_cursor?: string;
};

