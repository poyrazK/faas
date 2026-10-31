/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConsumerSignupPlan } from './ConsumerSignupPlan.js';
/**
 * What the sign-up page shows.
 */
export type ConsumerSignupFormResponse = {
  app: string;
  display_name: string;
  terms_url?: string;
  /**
   * False while the owner's Stripe key is a test key.
   */
  livemode: boolean;
  trial_days?: number;
  plans: Array<ConsumerSignupPlan>;
};

