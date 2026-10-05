/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectWriteReceipt } from './ObjectWriteReceipt.js';
/**
 * One live page of tracked bucket write receipts with an optional next-page cursor.
 */
export type ObjectWriteReceiptList = {
  items: Array<ObjectWriteReceipt>;
  next_cursor?: string;
};

