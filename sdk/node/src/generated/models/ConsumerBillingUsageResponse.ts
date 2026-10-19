/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConsumerBillingDay } from './ConsumerBillingDay.js';
/**
 * A consumer's daily usage over at most 31 days.
 */
export type ConsumerBillingUsageResponse = {
  since: string;
  until: string;
  days: Array<ConsumerBillingDay>;
};

