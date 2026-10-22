/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConsumerSignupPlan } from './ConsumerSignupPlan.js';
/**
 * The plans a consumer can switch between (ADR-20261021183000).
 */
export type ConsumerBillingPlansResponse = {
  /**
   * Omitted on the app's default plan.
   */
  current_plan_id?: string;
  can_change: boolean;
  can_close: boolean;
  plans: Array<ConsumerSignupPlan>;
};

