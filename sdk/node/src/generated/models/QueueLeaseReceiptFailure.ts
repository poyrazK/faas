/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A receipt that was not applied. lease_expired means it no longer owns its message.
 */
export type QueueLeaseReceiptFailure = {
  receipt: string;
  reason: 'lease_expired' | 'invalid_receipt';
};

