/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectStorageCharge } from './ObjectStorageCharge.js';
import type { ObjectStoragePolicy } from './ObjectStoragePolicy.js';
import type { ObjectStorageUsage } from './ObjectStorageUsage.js';
/**
 * Current UTC-month accounting and operator safety policy.
 */
export type ObjectStorageUsageResponse = {
  usage: ObjectStorageUsage;
  policy: ObjectStoragePolicy;
  charges?: ObjectStorageCharge;
};

