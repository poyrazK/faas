/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A consumer's spend cap and its last check.
 */
export type APIConsumerSpendCapResponse = {
  consumer_id: string;
  currency: string;
  monthly_cap_millicents: number;
  /**
   * True while the gateway refuses the consumer this month.
   */
  capped: boolean;
  /**
   * UTC month of the last check.
   */
  month?: string;
  /**
   * The month's amount due after prepaid credit at the last check.
   */
  amount_due_millicents?: number;
  checked_at?: string;
  created_at: string;
  updated_at: string;
};

