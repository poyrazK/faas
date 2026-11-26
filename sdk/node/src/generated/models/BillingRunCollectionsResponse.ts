/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingRunCollectionTotal } from './BillingRunCollectionTotal.js';
/**
 * Recorded Stripe payment totals for a completed billing run, grouped by currency and Stripe mode.
 */
export type BillingRunCollectionsResponse = {
  totals: Array<BillingRunCollectionTotal>;
};

