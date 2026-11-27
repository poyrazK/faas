/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Summary of recent durable requests and priced usage through the last complete minute.
 */
export type APIConsumerBillingUsageProgress = {
  since: string;
  until: string;
  status: 'waiting' | 'not_billable' | 'unpriced' | 'included' | 'priced' | 'mixed_currency';
  request_count: number;
  unpriced_units: number;
  amount_millicents: number;
  currency?: string;
};

