/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectBucket } from './ObjectBucket.js';
import type { ObjectStorageComputeBinding } from './ObjectStorageComputeBinding.js';
/**
 * Safe metadata for an active automatic developer bucket binding. No credential secret or provider placement is returned.
 */
export type DevBucketResponse = {
  bucket: ObjectBucket;
  binding: ObjectStorageComputeBinding;
};

