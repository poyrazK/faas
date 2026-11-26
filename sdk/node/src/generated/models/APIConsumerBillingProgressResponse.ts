/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerBillingInvoiceProgress } from './APIConsumerBillingInvoiceProgress.js';
import type { APIConsumerBillingStatementProgress } from './APIConsumerBillingStatementProgress.js';
import type { APIConsumerBillingUsageProgress } from './APIConsumerBillingUsageProgress.js';
/**
 * Current onboarding milestones for one app consumer billing identity.
 */
export type APIConsumerBillingProgressResponse = {
  consumer_id: string;
  registered_at: string;
  usage: APIConsumerBillingUsageProgress;
  /**
   * First minute with a positive priced charge in the customer's first 30 days.
   */
  first_priced_at?: string;
  statement?: APIConsumerBillingStatementProgress;
  invoice?: APIConsumerBillingInvoiceProgress;
  /**
   * Earliest recorded positive invoice payment in live mode for this customer.
   */
  first_live_paid_at?: string;
};

