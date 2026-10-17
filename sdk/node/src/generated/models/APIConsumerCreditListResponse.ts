/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerCreditBalance } from './APIConsumerCreditBalance.js';
import type { APIConsumerCreditResponse } from './APIConsumerCreditResponse.js';
/**
 * A consumer's credit grants, newest first, with balances per currency.
 */
export type APIConsumerCreditListResponse = {
  credits: Array<APIConsumerCreditResponse>;
  balances: Array<APIConsumerCreditBalance>;
};

