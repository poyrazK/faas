/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISecondInvoiceReview } from './APISecondInvoiceReview.js';
/**
 * Usage, invoicing, and payment milestones for a recent app consumer registration cohort.
 */
export type APIMonetizationFunnelResponse = {
  /**
   * Inclusive registration cohort start in UTC.
   */
  since: string;
  /**
   * Exclusive registration cohort end in UTC.
   */
  until: string;
  /**
   * All registrations in the cohort before applying the sample limit.
   */
  total_registered: number;
  /**
   * Registrations included in the milestone counts.
   */
  registered: number;
  /**
   * Customers with positively priced usage during their first 30 days.
   */
  priced_in_first_30_days: number;
  /**
   * Customers with a live Stripe invoice send recorded by Gregale.
   */
  live_invoice_sent: number;
  /**
   * Customers with a recorded positive live Stripe invoice payment.
   */
  live_payment: number;
  /**
   * Paying customers with a later non-overlapping billing month that has a finalized positive statement balance.
   */
  next_billable_month: number;
  /**
   * Customers with another positive live payment in their next billable UTC month.
   */
  next_month_live_payment: number;
  /**
   * One next-cycle review row per customer with a first live payment and a later billable statement or actionable draft.
   */
  second_invoice_review: Array<APISecondInvoiceReview>;
};

