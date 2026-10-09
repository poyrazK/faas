/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Separate observation for the pending head only; absent when no head is pending. Unknown includes unavailable/pruned history, not proof of non-acceptance.
 */
export type DurableEntityHeadDelivery = {
  status: 'unknown' | 'pending' | 'in_flight' | 'succeeded' | 'failed' | 'dead';
};

