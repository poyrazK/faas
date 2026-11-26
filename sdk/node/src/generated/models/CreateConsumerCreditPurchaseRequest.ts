/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A pack selection and the UUID used to recover the purchase on retry.
 */
export type CreateConsumerCreditPurchaseRequest = {
  /**
   * A unique client-generated identifier. Reuse it to retry this purchase.
   */
  purchase_id: string;
  pack_id: string;
};

