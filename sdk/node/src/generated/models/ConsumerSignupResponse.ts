/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One sign-up as the app owner sees it.
 */
export type ConsumerSignupResponse = {
  id: string;
  email: string;
  name: string;
  plan_id?: string;
  status: 'pending' | 'completed';
  livemode: boolean;
  /**
   * The Stripe customer, also the consumer's external_ref.
   */
  provider_customer_id?: string;
  consumer_id?: string;
  created_at: string;
  expires_at: string;
  completed_at?: string;
};

