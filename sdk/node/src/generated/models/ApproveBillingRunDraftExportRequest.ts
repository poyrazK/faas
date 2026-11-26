/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The exact Stripe draft and its amount due as shown in the review.
 */
export type ApproveBillingRunDraftExportRequest = {
  consumer_id: string;
  statement_id: string;
  revision: number;
  currency: string;
  amount_due_millicents: number;
  stripe_customer: string;
  livemode: boolean;
  connection_updated_at: string;
};

