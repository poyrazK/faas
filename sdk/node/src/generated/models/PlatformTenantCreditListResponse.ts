/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerCreditBalance } from './APIConsumerCreditBalance.js';
import type { PlatformTenantCreditResponse } from './PlatformTenantCreditResponse.js';
/**
 * A tenant's credit grants, newest first, with balances per currency.
 */
export type PlatformTenantCreditListResponse = {
  credits: Array<PlatformTenantCreditResponse>;
  balances: Array<APIConsumerCreditBalance>;
};

