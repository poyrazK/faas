/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConsumerSignupPrice } from './ConsumerSignupPrice.js';
/**
 * A plan customers can sign up for; no id is the app's default plan.
 */
export type ConsumerSignupPlan = {
  id?: string;
  name: string;
  max_requests_per_minute?: number;
  max_units_per_month?: number;
  prices: Array<ConsumerSignupPrice>;
};

