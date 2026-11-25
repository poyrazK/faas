/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A changed plan, or the Stripe Checkout to save a card first.
 */
export type ConsumerUpgradeResult = {
  plan_id: string;
  effective_from?: string;
  checkout_url?: string;
  /**
   * Completes the upgrade after Checkout.
   */
  complete_token?: string;
};

