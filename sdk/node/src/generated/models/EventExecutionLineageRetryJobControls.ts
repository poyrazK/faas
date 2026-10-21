/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageRetryJobControlReceipt } from './EventExecutionLineageRetryJobControlReceipt.js';
/**
 * Observed scoped page of retained bulk-job control receipts and its continuation cursor.
 */
export type EventExecutionLineageRetryJobControls = {
  controls: Array<EventExecutionLineageRetryJobControlReceipt>;
  next_after?: number;
};

