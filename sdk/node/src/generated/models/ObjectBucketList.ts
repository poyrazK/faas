/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectBucket } from './ObjectBucket.js';
/**
 * App bucket catalog and operator-configured preview capabilities.
 */
export type ObjectBucketList = {
  items: Array<ObjectBucket>;
  enabled: boolean;
  regions: Array<string>;
  default_region: string;
  max_upload_bytes: number;
  max_buckets_per_app: number;
};

