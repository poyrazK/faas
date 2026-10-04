/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectLockDefaultRetention } from './ObjectLockDefaultRetention.js';
/**
 * Native observation may report disabled. PUT requires enabled true. Omitting default_retention clears defaults while keeping Object Lock enabled.
 */
export type ObjectBucketObjectLockConfiguration = {
  enabled: boolean;
  default_retention?: ObjectLockDefaultRetention;
};

