/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A started sign-up. Send the customer to checkout_url; keep claim_token to complete it.
 */
export type CreateConsumerSignupResponse = {
  id: string;
  checkout_url: string;
  claim_token: string;
  expires_at: string;
};

