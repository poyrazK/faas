/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectBucketCORSRule } from './ObjectBucketCORSRule.js';
/**
 * A bucket's CORS rules; revision 0 means none are set.
 */
export type ObjectBucketCORS = {
  bucket_id: string;
  rules: Array<ObjectBucketCORSRule>;
  revision: number;
  updated_at: string;
};

