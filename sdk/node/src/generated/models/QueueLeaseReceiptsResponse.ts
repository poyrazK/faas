/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { QueueLeaseReceiptFailure } from './QueueLeaseReceiptFailure.js';
export type QueueLeaseReceiptsResponse = {
  succeeded: Array<string>;
  failed: Array<QueueLeaseReceiptFailure>;
};

