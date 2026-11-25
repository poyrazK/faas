/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConsumerSignupPlan } from './ConsumerSignupPlan.js';
/**
 * What the upgrade page shows an upgrade link's customer.
 */
export type ConsumerUpgradeResponse = {
  app: string;
  display_name: string;
  current_plan_id?: string;
  livemode: boolean;
  plans: Array<ConsumerSignupPlan>;
  /**
   * A card is saved: a paid plan needs no checkout.
   */
  has_card: boolean;
};

