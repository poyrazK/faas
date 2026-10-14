/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { QueueLeaseReceiptFailure } from './QueueLeaseReceiptFailure.js';
/**
 * Per-receipt outcome of an ack, nack or extend call.
 */
export type QueueLeaseReceiptsResponse = {
  succeeded: Array<string>;
  failed: Array<QueueLeaseReceiptFailure>;
};

