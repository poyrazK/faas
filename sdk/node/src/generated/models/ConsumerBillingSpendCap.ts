/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The consumer's monthly spend cap; once the amount due passes it, requests are refused until the month ends or credit is added.
 */
export type ConsumerBillingSpendCap = {
  currency: string;
  monthly_cap_millicents: number;
  capped: boolean;
  amount_due_millicents?: number;
};

