/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Finishes an upgrade after Checkout saved the card.
 */
export type CompleteConsumerUpgradeRequest = {
  token: string;
  complete_token: string;
  checkout_session_id: string;
};

