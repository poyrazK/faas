/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppSecretResponse } from './AppSecretResponse.js';
/**
 * Paginated list of sealed-secret envelopes (no plaintext).
 */
export type AppSecretListResponse = {
  secrets: Array<AppSecretResponse>;
  quota_max: number;
  count: number;
};

