/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A consumer's monthly spend cap after prepaid credit.
 */
export type PutAPIConsumerSpendCapRequest = {
  currency: string;
  /**
   * 0 allows prepaid usage only.
   */
  monthly_cap_millicents: number;
};

