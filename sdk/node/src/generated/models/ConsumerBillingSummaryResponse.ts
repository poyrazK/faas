/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerCreditBalance } from './APIConsumerCreditBalance.js';
import type { ConsumerBillingPlan } from './ConsumerBillingPlan.js';
import type { ConsumerBillingSpendCap } from './ConsumerBillingSpendCap.js';
/**
 * A consumer's own month to date, priced like its next statement.
 */
export type ConsumerBillingSummaryResponse = {
  consumer_id: string;
  external_ref: string;
  name: string;
  /**
   * The app's slug.
   */
  app: string;
  month: string;
  currency?: string;
  billable_units: number;
  unpriced_units: number;
  /**
   * Month-to-date amount; 100000 millicents is 1.00.
   */
  amount_millicents: number;
  /**
   * False while some usage has no effective rate card.
   */
  priced: boolean;
  plan?: ConsumerBillingPlan;
  as_of: string;
  /**
   * Prepaid credit the consumer can still use, per currency.
   */
  credits?: Array<APIConsumerCreditBalance>;
  spend_cap?: ConsumerBillingSpendCap;
};

