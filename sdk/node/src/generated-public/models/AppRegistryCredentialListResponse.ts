/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppRegistryCredentialResponse } from './AppRegistryCredentialResponse.js';
/**
 * Wrapped list response: rows + quota metadata. The Free plan
 * returns 403 on PUT and an empty list on GET.
 *
 */
export type AppRegistryCredentialListResponse = {
  credentials: Array<AppRegistryCredentialResponse>;
  /**
   * Per-app cap from the customer's plan (Free 0, Hobby 2, Pro 5, Scale 20).
   */
  quota_max: number;
  count: number;
};

