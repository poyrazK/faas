/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectLifecycleRule } from './ObjectLifecycleRule.js';
/**
 * Durable normalized lifecycle policy; removal retains revision history.
 */
export type ObjectBucketLifecycle = {
  bucket_id: string;
  revision: number;
  rules: Array<ObjectLifecycleRule>;
  updated_at: string;
};

