/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantSelfConsumerResponse } from './PlatformTenantSelfConsumerResponse.js';
/**
 * Revoked identities with the number of active keys newly revoked by this operation; exact retries return zero.
 */
export type PlatformTenantSelfConsumerRevocationResponse = {
  consumers: Array<PlatformTenantSelfConsumerResponse>;
  revoked_keys: number;
};

