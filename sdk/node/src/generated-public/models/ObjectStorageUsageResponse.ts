/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectStorageCharge } from './ObjectStorageCharge.js';
import type { ObjectStoragePolicy } from './ObjectStoragePolicy.js';
import type { ObjectStorageUsage } from './ObjectStorageUsage.js';
/**
 * Current UTC-month accounting, customer charge estimate, billing rollout state, and operator safety policy.
 */
export type ObjectStorageUsageResponse = {
  usage: ObjectStorageUsage;
  policy: ObjectStoragePolicy;
  charges?: ObjectStorageCharge;
  /**
   * Whether finalized object-storage charges are disabled, audited locally, or sent to the billing provider.
   */
  billing_mode: 'off' | 'shadow' | 'live';
  /**
   * UTC month boundary at which shadow/live handling starts. Periods before it are never charged.
   */
  billing_from?: string;
};

