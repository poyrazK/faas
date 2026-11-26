/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Provider-confirmed terms shown for owner approval before manual Stripe delivery.
 */
export type StripeInvoiceSendReview = {
  invoice_id: string;
  stripe_customer: string;
  recipient_email: string;
  currency: string;
  amount_due_minor: number;
  due_at: string;
  livemode: boolean;
  review_token: string;
};

