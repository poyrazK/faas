/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Receipts for ack, nack or extend. delay_seconds and error apply to nack; visibility_timeout_seconds to extend.
 */
export type QueueLeaseReceiptsRequest = {
  receipts: Array<string>;
  delay_seconds?: number;
  error?: string;
  visibility_timeout_seconds?: number;
};

