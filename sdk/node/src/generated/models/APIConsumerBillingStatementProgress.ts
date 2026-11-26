/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Statement associated with the chosen invoice, or the most relevant statement to review.
 */
export type APIConsumerBillingStatementProgress = {
  id: string;
  status: 'draft' | 'finalized';
  amount_due_millicents: number;
  unpriced_units: number;
};

