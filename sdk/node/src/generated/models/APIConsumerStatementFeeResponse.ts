/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A statement's monthly base fee or minimum-spend top-up.
 */
export type APIConsumerStatementFeeResponse = {
  kind: 'base_fee' | 'minimum';
  rate_card_id: string;
  /**
   * Negative only for a minimum top-up an adjustment reduces because late usage covers it.
   */
  amount_millicents: number;
};

