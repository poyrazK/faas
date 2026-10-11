/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectStorageComputeTrigger } from './ObjectStorageComputeTrigger.js';
/**
 * Opt-in private bucket in the developer workspace, bound in the default secret scope. Bucket and binding names are unique within the batch.
 */
export type DevBucketRequest = {
  name: string;
  /**
   * Omit to reuse existing placement or the current creation default.
   */
  region?: string;
  permission?: 'read' | 'write' | 'read_write';
  /**
   * Defaults to GREGALE_S3_ followed by the normalized bucket name.
   */
  prefix?: string;
  /**
   * Opt-in ObjectCreated triggers targeting this developer app. Omission preserves managed intent; an empty array clears it.
   */
  triggers?: Array<ObjectStorageComputeTrigger>;
};

