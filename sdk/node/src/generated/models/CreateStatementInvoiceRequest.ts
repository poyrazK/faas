/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingProvider } from './BillingProvider.js';
/**
 * Target of a statement invoice export.
 */
export type CreateStatementInvoiceRequest = {
  provider?: BillingProvider;
  /**
   * The provider's customer: a Stripe cus_… ID, a Paddle ctm_… ID, or a Lago external customer ID. Omitted, the consumer's or tenant's external_ref is used when it is one.
   */
  customer?: string;
};

