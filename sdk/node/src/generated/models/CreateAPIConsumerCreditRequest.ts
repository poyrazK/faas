/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Prepaid credit for one consumer.
 */
export type CreateAPIConsumerCreditRequest = {
  currency: string;
  /**
   * Credit in millicents; 100000 is 1.00.
   */
  amount_millicents: number;
  /**
   * When unused credit lapses; omitted, it never does.
   */
  expires_at?: string;
  note?: string;
};

