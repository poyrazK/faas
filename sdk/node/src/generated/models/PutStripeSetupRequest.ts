/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Restricted Stripe credential and optional reviewed connection version for guided setup.
 */
export type PutStripeSetupRequest = {
  /**
   * Stripe restricted key. Returned connection metadata contains only the last four characters.
   */
  secret_key: string;
  /**
   * The reviewed connection version for rotation. Omit to create only if there is no connected Stripe key.
   */
  expected_updated_at?: string;
};

